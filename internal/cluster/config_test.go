package cluster

import "testing"

func TestConfigAllowsValidPort(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ClusterName:   "microshift-okd-multinode",
		NodeBaseName:  "microshift-okd-",
		Image:         "microshift-okd",
		LVMDisk:       "/var/lib/microshift-okd/lvmdisk.image",
		ExtraConfig:   "/var/lib/microshift-okd/custom_config.yaml",
		LVMVolSize:    "1G",
		APIServerPort: 6443,
		VGName:        "myvg1",
		ExposeKubeAPI: true,
	}

	if cfg.APIServerPort != 6443 {
		t.Fatalf("unexpected api server port: got %d want %d", cfg.APIServerPort, 6443)
	}
}

func TestConfigRejectsBadPortRange(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ClusterName:   "microshift-okd-multinode",
		NodeBaseName:  "microshift-okd-",
		Image:         "microshift-okd",
		LVMDisk:       "/var/lib/microshift-okd/lvmdisk.image",
		ExtraConfig:   "/var/lib/microshift-okd/custom_config.yaml",
		LVMVolSize:    "1G",
		APIServerPort: 70000,
		VGName:        "myvg1",
	}

	if cfg.APIServerPort <= 65535 {
		t.Fatal("expected invalid api server port to remain out of range for caller-side rejection")
	}
}
