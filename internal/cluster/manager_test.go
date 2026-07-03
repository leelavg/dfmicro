package cluster

import "testing"

func TestGetIPAddress(t *testing.T) {
	t.Parallel()

	got, err := getIPAddress("10.89.0.0", 1)
	if err != nil {
		t.Fatalf("getIPAddress returned error: %v", err)
	}
	if got != "10.89.0.11" {
		t.Fatalf("unexpected ip address: got %q want %q", got, "10.89.0.11")
	}
}

func TestGetIPAddressRejectsInvalidIP(t *testing.T) {
	t.Parallel()

	if _, err := getIPAddress("not-an-ip", 1); err == nil {
		t.Fatal("expected error for invalid ip")
	}
}

func TestFilterClusterContainers(t *testing.T) {
	t.Parallel()

	output := "microshift-okd-1\nmicroshift-okd-2\nother-1\nmicroshift-okd-alpha\n"
	got := filterClusterContainers(output, "microshift-okd-")

	if len(got) != 2 {
		t.Fatalf("unexpected container count: got %d want %d", len(got), 2)
	}
	if got[0] != "microshift-okd-1" || got[1] != "microshift-okd-2" {
		t.Fatalf("unexpected containers: %#v", got)
	}
}

func TestParseLoopDevice(t *testing.T) {
	t.Parallel()

	got := parseLoopDevice("/dev/loop0: []: (/var/lib/microshift-okd/lvmdisk.image)\n")
	if got != "/dev/loop0" {
		t.Fatalf("unexpected loop device: got %q want %q", got, "/dev/loop0")
	}
}
