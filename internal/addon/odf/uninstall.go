package odf

import (
	"context"
	"fmt"
	"strings"
)

var uninstallCmds = []string{
	"delete storageclient --all --ignore-not-found --wait=false",
	"delete storageconsumer --all -n openshift-storage --ignore-not-found --wait=false",
	"annotate storagecluster --all -n openshift-storage uninstall.ocs.openshift.io/confirm-deletion=true --overwrite",
	"delete storagecluster --all -n openshift-storage --ignore-not-found",
	"delete deployment rook-ceph-tools -n openshift-storage --ignore-not-found",
	"delete cephfilesystem --all -n openshift-storage --ignore-not-found",
	"delete cephblockpool --all -n openshift-storage --ignore-not-found",
}

const deleteCephClusterCmd = "delete cephcluster --all -n openshift-storage --ignore-not-found"

var cleanupJobCmds = []string{
	"wait --for=create --timeout=120s job -l rook-ceph-cleanup=true -n openshift-storage",
	"wait --for=condition=complete --timeout=300s job -l rook-ceph-cleanup=true -n openshift-storage",
}

var uninstallPostCleanupCmds = []string{
	"delete configmap ocs-client-operator-config -n openshift-storage --ignore-not-found",
	"delete clusterserviceversions --all -n openshift-storage --ignore-not-found",
	"delete subscription --all -n openshift-storage --ignore-not-found",
	"delete operatorgroup odf -n openshift-storage --ignore-not-found",
	"delete catalogsource odf-catsrc -n openshift-marketplace --ignore-not-found",
}

var uninstallFinalCmds = []string{
	"delete mutatingwebhookconfiguration csv.odf.openshift.io --ignore-not-found",
	"delete scc rook-ceph --ignore-not-found",
	"delete namespace openshift-storage --ignore-not-found",
}

func (o *odf) uninstall(ctx context.Context, attempt bool) error {
	cleanupPolicyCmd := fmt.Sprintf(
		"patch cephcluster %s -n openshift-storage --type=merge --patch={\"spec\":{\"cleanupPolicy\":{\"confirmation\":\"yes-really-destroy-data\"}}}",
		newProviderNames(o.clusterName).cephCluster,
	)
	if !attempt {
		fmt.Println("# Run the following to uninstall and cleanup commands if you ran single/{odf,rook}-provider configurations:")
		for _, c := range uninstallCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		fmt.Println(o.kubectl + " " + cleanupPolicyCmd + " --kubeconfig " + o.kubeconfig)
		fmt.Println(o.kubectl + " " + deleteCephClusterCmd + " --kubeconfig " + o.kubeconfig)
		for _, c := range cleanupJobCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		for _, c := range uninstallPostCleanupCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		fmt.Println("# for each csiaddonsnodes.csiaddons.openshift.io in openshift-storage:")
		fmt.Println(o.kubectl + " patch <name> -n openshift-storage --type=merge -p '{\"metadata\":{\"finalizers\":null}}' --kubeconfig " + o.kubeconfig)
		fmt.Println("# for each clientprofiles.ocs.openshift.io in openshift-storage:")
		fmt.Println(o.kubectl + " patch <name> -n openshift-storage --type=merge -p '{\"metadata\":{\"finalizers\":null}}' --kubeconfig " + o.kubeconfig)
		for _, c := range uninstallFinalCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		return nil
	}

	cephClusterExists := o.hasResource(ctx, "cephcluster")
	cleanupJobExists := o.hasResource(ctx, "jobs", "-l", "app=rook-ceph-cleanup")
	for _, c := range uninstallCmds {
		o.runUninstallCommand(ctx, c)
	}
	o.runUninstallCommand(ctx, cleanupPolicyCmd)
	o.runUninstallCommand(ctx, deleteCephClusterCmd)
	if cephClusterExists {
		o.runUninstallCommand(ctx, cleanupJobCmds[0])
	}
	if cephClusterExists || cleanupJobExists {
		o.runUninstallCommand(ctx, cleanupJobCmds[1])
	}
	for _, c := range uninstallPostCleanupCmds {
		o.runUninstallCommand(ctx, c)
	}

	// TODO: find why some of these are left behind
	o.removeFinalizers(ctx, "clientprofiles.ocs.openshift.io")
	o.removeFinalizers(ctx, "csiaddonsnodes.csiaddons.openshift.io")
	o.removeFinalizers(ctx, "clientprofiles.csi.ceph.io")

	for _, c := range uninstallFinalCmds {
		o.runUninstallCommand(ctx, c)
	}
	return nil
}

func (o *odf) hasResource(ctx context.Context, resource string, extra ...string) bool {
	args := append([]string{"get", resource}, extra...)
	args = append(args, "-n", "openshift-storage", "--ignore-not-found", "-o", "name", "--kubeconfig", o.kubeconfig)
	result, err := o.runner.Run(ctx, o.kubectl, args...)
	return err == nil && strings.TrimSpace(result.Stdout) != ""
}

func (o *odf) runUninstallCommand(ctx context.Context, command string) {
	args := append(strings.Fields(command), "--kubeconfig", o.kubeconfig)
	o.logger.Info("running", "cmd", o.kubectl, "args", args)
	if _, err := o.runner.Run(ctx, o.kubectl, args...); err != nil {
		o.logger.Warn("failed", "cmd", command, "error", err)
	}
}

func (o *odf) removeFinalizers(ctx context.Context, resource string) {
	result, err := o.runner.Run(ctx, o.kubectl, "get", resource,
		"-n", "openshift-storage", "-o", "name", "--kubeconfig", o.kubeconfig)
	if err != nil {
		return
	}
	for name := range strings.FieldsSeq(result.Stdout) {
		o.logger.Info("removing finalizers", "resource", name)
		if _, err := o.runner.Run(ctx, o.kubectl, "patch", name, "-n", "openshift-storage",
			"--type=merge", "-p", `{"metadata":{"finalizers":null}}`,
			"--kubeconfig", o.kubeconfig); err != nil {
			o.logger.Warn("failed to remove finalizers", "resource", name, "error", err)
		}
	}
}
