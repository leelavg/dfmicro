package odf

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var uninstallCmds = []string{
	"annotate storagecluster ocs-storagecluster -n openshift-storage uninstall.ocs.openshift.io/confirm-deletion=true --overwrite",
	"delete storagecluster ocs-storagecluster -n openshift-storage --ignore-not-found",
	"delete configmap ocs-client-operator-config -n openshift-storage --ignore-not-found",
	"delete clusterserviceversions --all -n openshift-storage --ignore-not-found",
	"delete subscription --all -n openshift-storage --ignore-not-found",
	"delete operatorgroup odf -n openshift-storage --ignore-not-found",
	"delete catalogsource odf-catsrc -n openshift-marketplace --ignore-not-found",
}

var uninstallFinalCmds = []string{
	"delete mutatingwebhookconfiguration csv.odf.openshift.io --ignore-not-found",
	"delete namespace openshift-storage --ignore-not-found",
}

func (o *odf) Uninstall(ctx context.Context, attempt bool) error {
	if !attempt {
		fmt.Println("# Run the following to uninstall:")
		for _, c := range uninstallCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		fmt.Println("# for each csiaddonsnodes.csiaddons.openshift.io in openshift-storage:")
		fmt.Println(o.kubectl + " patch <name> -n openshift-storage --type=merge -p '{\"metadata\":{\"finalizers\":null}}' --kubeconfig " + o.kubeconfig)
		for _, c := range uninstallFinalCmds {
			fmt.Println(o.kubectl + " " + c + " --kubeconfig " + o.kubeconfig)
		}
		return nil
	}

	for _, c := range uninstallCmds {
		args := append(strings.Fields(c), "--kubeconfig", o.kubeconfig)
		o.logger.Info("running", "cmd", o.kubectl, "args", args)
		if _, err := o.runner.Run(ctx, o.kubectl, args...); err != nil {
			o.logger.Warn("failed", "cmd", c, "error", err)
		}
		time.Sleep(time.Second)
	}

	o.removeCsiAddonsNodeFinalizers(ctx)

	for _, c := range uninstallFinalCmds {
		args := append(strings.Fields(c), "--kubeconfig", o.kubeconfig)
		o.logger.Info("running", "cmd", o.kubectl, "args", args)
		if _, err := o.runner.Run(ctx, o.kubectl, args...); err != nil {
			o.logger.Warn("failed", "cmd", c, "error", err)
		}
		time.Sleep(time.Second)
	}
	return nil
}

func (o *odf) removeCsiAddonsNodeFinalizers(ctx context.Context) {
	result, err := o.runner.Run(ctx, o.kubectl, "get", "csiaddonsnodes.csiaddons.openshift.io",
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
