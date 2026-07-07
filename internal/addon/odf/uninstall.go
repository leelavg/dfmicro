package odf

import (
	"context"
	"fmt"
	"strings"
)

var uninstallCmds = []string{
	"delete storagecluster ocs-storagecluster -n openshift-storage --ignore-not-found",
	"delete configmap ocs-client-operator-config -n openshift-storage --ignore-not-found",
	"delete clusterserviceversions --all -n openshift-storage --ignore-not-found",
	"delete subscription --all -n openshift-storage --ignore-not-found",
	"delete operatorgroup odf -n openshift-storage --ignore-not-found",
	"delete catalogsource odf-catsrc -n openshift-marketplace --ignore-not-found",
	"delete namespace openshift-storage --ignore-not-found",
}

func (o *odf) Uninstall(ctx context.Context, attempt bool) error {
	if !attempt {
		fmt.Println("# Run the following to uninstall:")
		for _, c := range uninstallCmds {
			line := o.kubectl + " " + c
			if o.kubeconfig != "" {
				line += " --kubeconfig " + o.kubeconfig
			}
			fmt.Println(line)
		}
		return nil
	}

	for _, c := range uninstallCmds {
		args := strings.Fields(c)
		if o.kubeconfig != "" {
			args = append(args, "--kubeconfig", o.kubeconfig)
		}
		o.logger.Info("running", "cmd", o.kubectl, "args", args)
		if _, err := o.runner.Run(ctx, o.kubectl, args...); err != nil {
			o.logger.Warn("failed", "cmd", c, "error", err)
		}
	}
	return nil
}
