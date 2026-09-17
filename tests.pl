#!/usr/bin/perl

# Run with a built binary on a clean Linux host with rootful Podman.
# Examples: DFMICRO_BIN=$HOME/.local/bin/dfmicro perl tests.pl --upto 3
#           DFMICRO_BIN=$HOME/.local/bin/dfmicro perl tests.pl --upto 2 --etcd

use strict;
use warnings;
use Getopt::Long qw(GetOptions);
use Text::ParseWords qw(shellwords);

my $upto = 0;
my $etcd = 0;
my $list_mode;
my $keep = 0;
my $pause = 0;
my $fail_fast = 0;
my $cleanup_only = 0;
my $timeout;
my $show_help = 0;

sub usage {
    print "Usage:\tperl tests.pl [options]\n\n";
    printf "  %-16s\t%s\n", '--upto N', 'Run through level N (0-3) (default: 0)';
    printf "  %-16s\t%s\n", '--etcd', 'Create clusters with etcd';
    printf "  %-16s\t%s\n", '--list [MODE]', 'List "tests" or "cmds" (default: tests)';
    printf "  %-16s\t%s\n", '--keep', 'Leave resources for inspection';
    printf "  %-16s\t%s\n", '--pause', 'Wait for Enter before cleanup';
    printf "  %-16s\t%s\n", '--fail-fast', 'Stop after the first failed check';
    printf "  %-16s\t%s\n", '--cleanup', 'Remove suite resources without testing';
    printf "  %-16s\t%s\n", '--timeout DURATION', 'Limit total runtime, for example 15m';
    printf "  %-16s\t%s\n", '--help', 'Show this help';
}

GetOptions(
	'upto=i' => \$upto,
	'etcd'   => \$etcd,
	'list:s' => \$list_mode,
	'keep'   => \$keep,
	'pause'  => \$pause,
	'fail-fast' => \$fail_fast,
	'cleanup' => \$cleanup_only,
	'timeout=s' => \$timeout,
	'help|h' => \$show_help,
) or die "use --help for usage\n";
if ($show_help) {
	usage();
	exit 0;
}
$list_mode = 'tests' if defined $list_mode && $list_mode eq '';
die "--list must be tests or cmds\n" if defined $list_mode && $list_mode ne 'tests' && $list_mode ne 'cmds';
my $list_only = defined $list_mode;
die "--upto must be between 0 and 3\n" unless $upto >= 0 && $upto <= 3;
die "--pause and --keep cannot be used together\n" if $pause && $keep;
die "--cleanup cannot be combined with test options\n"
	if $cleanup_only && ($upto != 0 || $etcd || $list_only || $keep || $pause || $fail_fast);

sub duration_seconds {
	my ($value) = @_;
	return 0 unless defined $value;
	die "--timeout must be a positive duration such as 15m or 900s\n"
		unless $value =~ /^(\d+)([smh]?)$/ && $1 > 0;
	my ($amount, $unit) = ($1, $2);
	return $amount * ($unit eq 'h' ? 3600 : $unit eq 'm' ? 60 : 1);
}

my $timeout_seconds = duration_seconds($timeout);

my $dfmicro = $ENV{DFMICRO_BIN} || 'dfmicro';
my $micro = 'micro';
my $first = 'first';
my $network = 'backbone';
my $api_port = 16443;
my $config_home = $ENV{XDG_CONFIG_HOME} || "$ENV{HOME}/.config";
my $config_dir = "$config_home/dfmicro";
my $work_dir = '/tmp/dfmicro-test';
my ($tests, $failed) = (0, 0);
my $started = 0;
my $started_at = time;
my %level_elapsed;
my ($current_level, $current_level_started);
my ($micro_kubeconfig, $first_kubeconfig);
my $backend_option = $etcd ? '--etcd' : '';

sub check {
    my ($passed, $name) = @_;
    if ($list_only) {
        print "\t$name\n" if $list_mode eq 'tests';
        return;
    }
    $tests++;
    $failed++ unless $passed;
    print($passed ? "ok" : "not ok", " $tests - $name\n");
	if (!$passed && $fail_fast) {
		cleanup_on_exit();
		finish();
	}
}

sub finish {
	return exit($failed ? 1 : 0) if $list_only;
	if (defined $current_level) {
		$level_elapsed{$current_level} = int(time - $current_level_started);
	}
	my $passed = $tests - $failed;
	my $elapsed = int(time - $started_at);
	my $duration = format_duration($elapsed);
	print "1..$tests\n";
	print "# total: $tests, ok: $passed, not ok: $failed, elapsed: $duration\n";
	for my $level (sort keys %level_elapsed) {
		print "# $level elapsed: ", format_duration($level_elapsed{$level}), "\n";
	}
	print "# logs: $work_dir/dfmicro.log\n";
	print "# commands: $work_dir/commands.log\n";
	exit($failed ? 1 : 0);
}

sub format_duration {
	my ($seconds) = @_;
	return $seconds >= 3600
		? sprintf('%dh%02dm%02ds', int($seconds / 3600), int($seconds / 60) % 60, $seconds % 60)
		: sprintf('%dm%02ds', int($seconds / 60), $seconds % 60);
}

sub section {
    print $list_only ? "\n---- $_[0] ----\n" : "\n## $_[0]\n";
}

sub level_section {
	if (!$list_only) {
		if (defined $current_level) {
			$level_elapsed{$current_level} = int(time - $current_level_started);
		}
		$current_level = $_[0];
		$current_level_started = time;
	}
	print $list_only ? "\n==== $_[0] ====\n" : "\n# $_[0]\n";
}

sub list_command {
    my ($program, @args) = @_;
    print "\t$program ", join(' ', @args), "\n";
}

sub shell_quote {
    my ($value) = @_;
    $value =~ s/'/'\\''/g;
    return "'$value'";
}

sub shell_command {
    return join(' ', map { shell_quote($_) } @_);
}

sub command_args {
    return shellwords($_[0]);
}

sub run_program {
    my ($program, $command) = @_;
    my @args = command_args($command);
    if ($list_only) {
        list_command($program, @args) if $list_mode eq 'cmds';
        return 1;
    }
    local $ENV{DFMICRO_CMD_LOG} = $ENV{DFMICRO_CMD_LOG} || "$work_dir/commands.log";
    my $status = system('sh', '-c', shell_command($program, @args) . " >> " . shell_quote("$work_dir/dfmicro.log") . " 2>&1");
    return $status == 0;
}

sub run_command {
    return run_program($dfmicro, $_[0]);
}

sub run_ok {
    my ($name, $command) = @_;
    check(run_command($command), $name);
}

sub run_fail {
    my ($name, $command) = @_;
    check(!run_command($command), $name);
}

sub run_with_input {
	my ($name, $input, $command) = @_;
	my @args = command_args($command);
	if ($list_only) {
		list_command($dfmicro, @args) if $list_mode eq 'cmds';
		check(1, $name);
		return;
	}
    local $ENV{DFMICRO_CMD_LOG} = $ENV{DFMICRO_CMD_LOG} || "$work_dir/commands.log";
    open my $child, '|-', 'sh', '-c', shell_command($dfmicro, @args) . " >> " . shell_quote("$work_dir/dfmicro.log") . " 2>&1"
        or die "start $dfmicro: $!";
    print {$child} $input;
    close $child;
    check($? == 0, $name);
}

sub capture {
	my ($program, @args) = @_;
	if ($list_only) {
		list_command($program, @args) if $list_mode eq 'cmds';
		return (1, '');
    }
    open my $pipe, '-|', 'sh', '-c', shell_command($program, @args) . " 2>> " . shell_quote("$work_dir/dfmicro.log")
        or die "start $program: $!";
    local $/;
    my $output = <$pipe> // '';
    close $pipe;
    my $status = $?;
    return ($status == 0, $output);
}

sub capture_ok {
    my ($name, $program, @args) = @_;
    my ($passed, $output) = capture($program, @args);
    check($passed, $name);
    return $output;
}

sub kubectl {
    my ($kubeconfig, $command) = @_;
    return run_program('kubectl', "--kubeconfig $kubeconfig $command");
}

sub kubectl_ok {
    my ($name, $kubeconfig, $command) = @_;
    check(kubectl($kubeconfig, $command), $name);
}

sub kubectl_with_input {
    my ($name, $kubeconfig, $input) = @_;
    return list_command('kubectl', '--kubeconfig', $kubeconfig, 'apply', '-f', '-') if $list_only;
    local $ENV{DFMICRO_CMD_LOG} = $ENV{DFMICRO_CMD_LOG} || "$work_dir/commands.log";
    open my $child, '|-', 'sh', '-c', shell_command('kubectl', '--kubeconfig', $kubeconfig, 'apply', '-f', '-')
        . " >> " . shell_quote("$work_dir/dfmicro.log") . " 2>&1"
        or die "start kubectl: $!";
    print {$child} $input;
    close $child;
    check($? == 0, $name);
}

sub run_netshoot {
    my ($name, $kubeconfig, $node, $nad) = @_;
    my $annotations = $nad eq '' ? '' : "  annotations:\n    k8s.v1.cni.cncf.io/networks: $nad\n";
    my $manifest = "apiVersion: v1\n"
        . "kind: Pod\n"
        . "metadata:\n"
        . "  name: $name\n"
        . $annotations
        . "spec:\n"
        . "  nodeName: $node\n"
        . "  containers:\n"
        . "  - name: $name\n"
        . "    image: docker.io/nicolaka/netshoot:v0.16\n"
        . "    command: [\"sleep\", \"infinity\"]\n"
        . "    securityContext:\n"
        . "      capabilities:\n"
        . "        add: [\"NET_RAW\"]\n";
    kubectl_with_input("create netshoot $name", $kubeconfig, $manifest);
    kubectl_ok("wait for netshoot $name", $kubeconfig, "wait --for=condition=Ready pod/$name --timeout=60s");
}

sub pod_ip {
    my ($kubeconfig, $pod) = @_;
    my ($passed, $ip) = capture('kubectl', '--kubeconfig', $kubeconfig, 'get', 'pod', $pod, '-o', 'jsonpath={.status.podIP}');
    return $passed ? $ip : undef;
}

sub network_ip {
    my ($kubeconfig, $pod, $nad) = @_;
    my ($passed, $status) = capture(
        'kubectl', '--kubeconfig', $kubeconfig, 'get', 'pod', $pod,
        '-o', 'jsonpath={.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}',
    );
    return unless $passed;
    return $1 if $status =~ /"name"\s*:\s*"(?:[^"\/]+\/)?\Q$nad\E".*?"ips"\s*:\s*\[\s*"([^"]+)/s;
}

sub fping {
    my ($name, $kubeconfig, $source, $target, $should_work) = @_;
    my $command = "exec $source -- fping -c 1 -t 1000 $target";
    my $passed = kubectl($kubeconfig, $command);
    check($passed == $should_work, $name);
}

sub kubeconfig {
	my ($name) = @_;
	my $path = "$work_dir/$name-kubeconfig";
	if ($list_only) {
		list_command($dfmicro, 'cluster', 'kubeconfig', '--name', $name) if $list_mode eq 'cmds';
		check(1, "write $name kubeconfig");
		return $path;
    }
    my ($passed, $output) = capture($dfmicro, 'cluster', 'kubeconfig', '--name', $name);
    check($passed && $output ne '', "write $name kubeconfig");
    open my $file, '>', $path or die "write $path: $!";
    print {$file} $output;
    close $file or die "close $path: $!";
    return $path;
}

sub config_is_empty {
    return 1 unless -d $config_dir;
    opendir my $dir, $config_dir or die "open $config_dir: $!";
    my @entries = grep { $_ ne '.' && $_ ne '..' } readdir $dir;
    closedir $dir;
    return @entries == 0;
}

sub clean_slate {
    return 1 if $list_only;
    check(config_is_empty(), "dfmicro config directory is empty before testing");
    return $failed == 0;
}

sub remove_cluster {
    my ($name) = @_;
    run_command("cluster rm --name $name");
}

sub remove_network {
	run_command("network delete --name $network");
}

sub remove_netshoots {
	return unless defined $micro_kubeconfig && defined $first_kubeconfig;
	for my $pod (qw(first-default-a micro-gp1-a first-gp1-b micro-gp1-b first-default-b micro-default-b first-default-c micro-default-c plain-first plain-micro plain-micro-new)) {
		my $kubeconfig = $pod =~ /^first/ ? $first_kubeconfig : $micro_kubeconfig;
		run_program('kubectl', "--kubeconfig $kubeconfig delete pod $pod --ignore-not-found --grace-period=2 --wait=false");
	}
}

sub cleanup_level_1 {
    return if $list_only;
    remove_cluster($micro);
    check(config_is_empty(), 'level 1 cleanup removes cluster state');
    $started = 0;
}

sub pause_before_cleanup {
	return unless $pause && !$list_only;
	print "Press Enter to clean up...\n";
	<STDIN>;
}

sub cleanup_level_2 {
    return if $list_only;
    remove_cluster($first);
    remove_cluster($micro);
    check(config_is_empty(), 'level 2 cleanup removes cluster state');
    $started = 0;
}

sub cleanup_level_3 {
	return if $list_only;
	remove_netshoots();
	run_command("network unpeer --cluster $first,$micro");
    run_command("network detach --cluster $first:default/gp1,$micro:default/gp1 --from $network");
    remove_network();
    remove_cluster($first);
    remove_cluster($micro);
    check(!-d "$config_dir/,networks", 'network state directory is removed');
    check(config_is_empty(), 'level 3 cleanup removes all test state');
    $started = 0;
}

sub cleanup_on_exit {
	return if $list_only || $keep;
	remove_cluster($first);
	remove_cluster($micro);
	remove_network();
	$started = 0;
}

$SIG{INT} = sub { cleanup_on_exit() if $started; exit 130 };
$SIG{TERM} = sub { cleanup_on_exit() if $started; exit 143 };
$SIG{ALRM} = sub {
	print STDERR "suite timeout exceeded\n";
	cleanup_on_exit() if $started;
	exit 124;
};
END { cleanup_on_exit() if $started }

alarm($timeout_seconds) if $timeout_seconds && !$list_only;

if ($cleanup_only) {
	mkdir $work_dir unless -d $work_dir;
	unlink "$work_dir/commands.log", "$work_dir/dfmicro.log";
	$started = 1;
	cleanup_on_exit();
	print "cleanup complete\n";
	exit 0;
}

print "TAP version 13\n" unless $list_only;
unless ($list_only) {
    mkdir $work_dir unless -d $work_dir;
    unlink "$work_dir/commands.log", "$work_dir/dfmicro.log";
}
exit 1 unless clean_slate();
$started = 1;

# Level 0 validates commands that do not create cluster resources.
level_section('level 0: CLI surface');
# Documentation and defaults must remain usable before any cluster exists.
section('documentation and defaults');
run_ok('version', '-v');
run_ok('config', 'config');
run_ok('docs', 'docs');
run_ok('examples', 'docs --examples');
run_ok('devlog', 'devlog');
run_ok('help', '--help');
# Resource reporting should be safe when no cluster is running.
section('resource reporting');
run_fail('resources requires a running cluster', 'ops resources');
run_fail('invalid API port is rejected', 'cluster create --name invalid --api-server-port 1023');
run_fail('node add requires an existing cluster', 'node add --cluster missing');

if ($upto == 0) {
	finish();
}

# Level 1 validates the default SQLite cluster lifecycle.
level_section('level 1: default cluster');
# The saved cluster state and exported kubeconfig must describe the running cluster.
section('cluster start');
run_ok('create default cluster', "cluster create --no-topolvm --api-server-port $api_port $backend_option");
run_fail('duplicate cluster creation is rejected', "cluster create --name $micro --no-topolvm --api-server-port $api_port");
run_ok('cluster config', "cluster config --name $micro");
$micro_kubeconfig = kubeconfig($micro);
kubectl_ok('list default cluster resources', $micro_kubeconfig, 'get all -A');
run_with_input('cluster exec', "oc get all -A\nexit\n", "cluster exec --name $micro");
run_ok('cluster list', 'cluster ls');
run_ok('node config', "node config --cluster $micro");
# The control node is protected, and storage reporting must still work.
section('control-node safety and storage reporting');
run_fail('control node cannot be removed', "node rm --cluster $micro --name $micro-1");
run_ok('storage command', 'ops storage');

if ($upto == 1) {
	pause_before_cleanup();
	cleanup_level_1() unless $keep;
	$started = 0 if $keep;
	finish();
}

# Level 2 validates TopoLVM provisioning and worker onboarding.
level_section('level 2: TopoLVM and worker');
# A worker must join the control plane and remain inspectable through the CLI.
section('worker onboarding');
run_ok('create TopoLVM cluster', "cluster create --name $first --api-server-port " . ($api_port + 1) . " --cluster-cidr 10.52.0.0/16 --service-cidr 10.53.0.0/16 --lvm-volsize 2G $backend_option");
run_ok('add worker', "node add --cluster $first");
run_ok('worker node config', "node config --cluster $first");
$first_kubeconfig = kubeconfig($first);
kubectl_ok('list worker cluster resources', $first_kubeconfig, 'get all -A');
run_with_input('worker cluster exec', "oc get all -A\nexit\n", "cluster exec --name $first --container $first-2");
run_ok('show storage resources', "ops resources --name $first");

# A workload must provision, mount, write, and read persistent data.
section('persistent volume read and write');
my $storage_manifest = <<'YAML';
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: dfmicro-storage-test
spec:
  serviceName: dfmicro-storage-test
  replicas: 1
  selector:
    matchLabels:
      app: dfmicro-storage-test
  template:
    metadata:
      labels:
        app: dfmicro-storage-test
    spec:
      nodeSelector:
        kubernetes.io/hostname: first-1
      containers:
      - name: writer
        image: docker.io/nicolaka/netshoot:v0.16
        command: ["sleep", "infinity"]
        volumeMounts:
        - name: data
          mountPath: /data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
YAML
kubectl_with_input('create TopoLVM StatefulSet', $first_kubeconfig, $storage_manifest);
kubectl_ok('wait for StatefulSet', $first_kubeconfig, 'rollout status statefulset/dfmicro-storage-test --timeout=60s');
kubectl_ok('write to TopoLVM volume', $first_kubeconfig, "exec dfmicro-storage-test-0 -- sh -c 'printf dfmicro > /data/check'");
kubectl_ok('read from TopoLVM volume', $first_kubeconfig, "exec dfmicro-storage-test-0 -- sh -c 'test \$(cat /data/check) = dfmicro'");
unless ($keep) {
    kubectl_ok('delete storage StatefulSet', $first_kubeconfig, 'delete statefulset dfmicro-storage-test --wait=true --timeout=60s');
    kubectl_ok('delete storage PVC', $first_kubeconfig, 'delete pvc data-dfmicro-storage-test-0 --wait=true --timeout=60s');
}

if ($upto == 2) {
	pause_before_cleanup();
	cleanup_level_2() unless $keep;
	$started = 0 if $keep;
	finish();
}

# Level 3 validates bridge, Multus, pod traffic, and route management.
level_section('level 3: multi-cluster network');
# Bridge membership and NAD/IPAM state must be repeatable.
section('bridge connection and IPAM attachment');
run_ok('create bridge network', "network create --name $network --subnet 172.31.0.0/16");
run_fail('network connect rejects an unknown network', "network connect --cluster $first --to missing");
run_ok('connect clusters with comma syntax', "network connect --cluster $first,$micro --to $network");
run_fail('network attach rejects duplicate cluster flags', "network attach --cluster $first --cluster $first --to $network");
run_fail('network attach rejects an unknown cluster', "network attach --cluster missing --to $network");
run_ok('attach default and gp1 groups', "network attach --cluster $first,$micro:gp1 --to $network");
run_netshoot('first-default-a', $first_kubeconfig, "$first-1", "default/$network-default");
run_netshoot('micro-gp1-a', $micro_kubeconfig, "$micro-1", "default/$network-gp1");
my $first_default_ip = network_ip($first_kubeconfig, 'first-default-a', "$network-default");
my $micro_gp1_ip = network_ip($micro_kubeconfig, 'micro-gp1-a', "$network-gp1");
check(defined $first_default_ip && defined $micro_gp1_ip, 'read initial secondary network addresses');
fping('different groups do not reach each other', $first_kubeconfig, 'first-default-a', $micro_gp1_ip, 0) if defined $micro_gp1_ip;

run_ok('attach same group with repeated flags', "network attach --cluster $first:gp1 --cluster $micro:gp1 --to $network");
run_netshoot('first-gp1-b', $first_kubeconfig, "$first-1", "default/$network-gp1");
run_netshoot('micro-gp1-b', $micro_kubeconfig, "$micro-1", "default/$network-gp1");
my $first_gp1_ip = network_ip($first_kubeconfig, 'first-gp1-b', "$network-gp1");
my $micro_gp1_same_ip = network_ip($micro_kubeconfig, 'micro-gp1-b', "$network-gp1");
check(defined $first_gp1_ip && defined $micro_gp1_same_ip, 'read shared secondary network addresses');
fping('same group reaches across clusters', $first_kubeconfig, 'first-gp1-b', $micro_gp1_same_ip, 1) if defined $micro_gp1_same_ip;

run_fail('host-local attachment blocks worker add', "node add --cluster $micro");
for my $pod (qw(first-default-a micro-gp1-a first-gp1-b micro-gp1-b)) {
	    kubectl_ok("delete netshoot $pod", $pod =~ /^first/ ? $first_kubeconfig : $micro_kubeconfig, "delete pod $pod --ignore-not-found --grace-period=2 --wait=false");
}
run_ok('detach before worker add', "network detach --cluster $first:default/gp1,$micro:default/gp1 --from $network");
run_ok('add worker after detach', "node add --cluster $micro");
run_ok('reattach after worker add', "network attach --cluster $first:default/gp1,$micro:default/gp1 --to $network");
run_netshoot('first-default-b', $first_kubeconfig, "$first-2", "default/$network-default");
run_netshoot('micro-default-b', $micro_kubeconfig, "$micro-2", "default/$network-default");
run_netshoot('first-default-c', $first_kubeconfig, "$first-1", "default/$network-default");
run_netshoot('micro-default-c', $micro_kubeconfig, "$micro-1", "default/$network-default");
my $first_default_b_ip = network_ip($first_kubeconfig, 'first-default-b', "$network-default");
my $micro_default_b_ip = network_ip($micro_kubeconfig, 'micro-default-b', "$network-default");
my $first_default_c_ip = network_ip($first_kubeconfig, 'first-default-c', "$network-default");
my $micro_default_c_ip = network_ip($micro_kubeconfig, 'micro-default-c', "$network-default");
check(defined $first_default_c_ip && defined $micro_default_c_ip, 'read reattached secondary network addresses');
check(defined $first_default_b_ip && defined $micro_default_b_ip, 'read worker secondary network addresses');
fping('same cluster reaches between first nodes', $first_kubeconfig, 'first-default-c', $first_default_b_ip, 1) if defined $first_default_b_ip;
fping('same cluster reaches between micro nodes', $micro_kubeconfig, 'micro-default-c', $micro_default_b_ip, 1) if defined $micro_default_b_ip;

# Plain cluster traffic must fail before peering and work after peering.
section('plain pod routing before and after peering');
run_netshoot('plain-first', $first_kubeconfig, "$first-1", '');
run_netshoot('plain-micro', $micro_kubeconfig, "$micro-1", '');
my $plain_first_ip = pod_ip($first_kubeconfig, 'plain-first');
my $plain_micro_ip = pod_ip($micro_kubeconfig, 'plain-micro');
check(defined $plain_first_ip && defined $plain_micro_ip, 'read plain pod addresses');
fping('plain pods fail across clusters before peer', $first_kubeconfig, 'plain-first', $plain_micro_ip, 0) if defined $plain_micro_ip;

run_ok('peer with comma syntax', "network peer --cluster $first,$micro");
run_ok('peer again with repeated flags', "network peer --cluster $first --cluster $micro");
fping('plain pods reach across clusters after peer', $first_kubeconfig, 'plain-first', $plain_micro_ip, 1) if defined $plain_micro_ip;

# Newly added workers need the peer rules applied explicitly.
section('peer refresh after worker replacement');
for my $pod (qw(micro-default-b micro-default-c plain-micro)) {
	kubectl_ok("delete netshoot $pod before worker replacement", $micro_kubeconfig, "delete pod $pod --ignore-not-found --grace-period=2 --wait=false");
}
run_ok('remove worker after peer', "node rm --cluster $micro --name $micro-2");
run_ok('add worker after peer', "node add --cluster $micro");
run_netshoot('plain-micro-new', $micro_kubeconfig, "$micro-2", '');
my $plain_micro_new_ip = pod_ip($micro_kubeconfig, 'plain-micro-new');
check(defined $plain_micro_new_ip, 'read replacement worker pod address');
fping('new worker is not reached by old peer rules', $first_kubeconfig, 'plain-first', $plain_micro_new_ip, 0) if defined $plain_micro_new_ip;
run_ok('peer replacement worker', "network peer --cluster $first,$micro");
fping('replacement worker reaches after peer refresh', $first_kubeconfig, 'plain-first', $plain_micro_new_ip, 1) if defined $plain_micro_new_ip;

# Removing routes and rules repeatedly must remain successful.
section('idempotent peer removal');
run_ok('unpeer with comma syntax', "network unpeer --cluster $first,$micro");
run_ok('unpeer again with repeated flags', "network unpeer --cluster $first --cluster $micro");
run_ok('network config', "network config --name $network");

if ($upto == 3) {
	pause_before_cleanup();
	cleanup_level_3() unless $keep;
	$started = 0 if $keep;
	finish();
}
