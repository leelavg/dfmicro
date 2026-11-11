package odr

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dfmicro/internal/addon/ocp"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

const s3ConfigCRD = "s3configurations.multicluster.odf.openshift.io"

const (
	s3BucketName         = "dfmicro-odr"
	localS3ContainerName = "dfmicro-local-s3"
)

func (o *odr) configure(ctx context.Context, managedClusters []string, peerType string) error {
	for i, name := range managedClusters {
		managedClusters[i] = strings.TrimSpace(name)
	}
	for _, name := range managedClusters {
		o.logger.Info("labeling ManagedCluster with cluster ID", "cluster", name)
		if err := o.labelManagedClusterID(ctx, name); err != nil {
			return err
		}
	}

	for _, crd := range []string{s3ConfigCRD, "mirrorpeers.multicluster.odf.openshift.io"} {
		o.logger.Info("waiting for CRD", "name", crd, "cluster", o.clusterName)
		if err := ocp.WaitForCRD(ctx, o.runner, support.KubeClient, o.kubeconfig, crd); err != nil {
			return err
		}
	}

	o.logger.Info("waiting for ODF Multicluster Orchestrator CSV", "cluster", o.clusterName)
	csvName, err := ocp.WaitForCSV(ctx, o.runner, support.KubeClient, o.kubeconfig, "openshift-operators", "odf-multicluster-orchestrator")
	if err != nil {
		return err
	}
	if err := o.applyTrustedCABundle(ctx, o.clusterName, "openshift-operators", "odr-hub-operator", "ramen-hub-operator"); err != nil {
		return err
	}
	o.logger.Info("scaling down ODF Multicluster console", "cluster", o.clusterName, "csv", csvName)
	_, err = o.runner.Run(ctx, support.KubeClient,
		"patch", "csv", csvName, "-n", "openshift-operators",
		"--type=json", "-p", `[{"op":"test","path":"/spec/install/spec/deployments/1/name","value":"odf-multicluster-console"},{"op":"add","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("scale down ODF Multicluster console: %w", err)
	}

	for _, clusterName := range managedClusters {
		if err := o.applyTemplateAccess(ctx, clusterName); err != nil {
			return err
		}
		if err := o.installVolSync(ctx, clusterName); err != nil {
			return err
		}
		if err := o.installVelero(ctx, clusterName); err != nil {
			return err
		}
	}

	o.logger.Info("creating or confirming local S3 bucket", "cluster", o.clusterName, "bucket", s3BucketName)
	result, err := support.RunPodmanPrivileged(ctx, o.runner,
		"exec", rootconfig.NodeName(o.clusterName, 0), "curl",
		"--silent", "--show-error", "--max-time", "10",
		"--output", "/dev/null", "--write-out", "%{http_code}",
		"--request", "PUT", "http://"+localS3ContainerName+":9000/"+s3BucketName,
	)
	if err != nil {
		return fmt.Errorf("create local S3 bucket %q: %w", s3BucketName, err)
	}
	status, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil || status < 200 || status >= 300 {
		return fmt.Errorf("create local S3 bucket %q: unexpected HTTP status %q", s3BucketName, strings.TrimSpace(result.Stdout))
	}

	o.logger.Info("applying local-s3 credentials", "cluster", o.clusterName)
	secret, err := support.Render(localS3SecretTmpl, map[string]string{"Bucket": s3BucketName})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, secret); err != nil {
		return fmt.Errorf("apply local-s3 credentials: %w", err)
	}

	quotedNames := make([]string, 0, len(managedClusters))
	for _, name := range managedClusters {
		quotedNames = append(quotedNames, strconv.Quote(name))
	}
	configuration, err := support.Render(s3ConfigurationTmpl, map[string]string{
		"ManagedClusters": strings.Join(quotedNames, ", "),
	})
	if err != nil {
		return err
	}
	o.logger.Info("applying ODR S3Configuration", "cluster", o.clusterName, "managedClusters", managedClusters)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, configuration); err != nil {
		return fmt.Errorf("apply ODR S3Configuration: %w", err)
	}
	for _, clusterName := range managedClusters {
		kubeconfig, err := rootconfig.Kubeconfig(clusterName)
		if err != nil {
			return fmt.Errorf("load kubeconfig for cluster %q: %w", clusterName, err)
		}
		o.logger.Info("waiting for StorageCluster", "cluster", clusterName)
		if _, err := o.runner.Run(ctx, support.KubeClient,
			"wait", "--for=jsonpath={.status.phase}=Ready", "--timeout=600s",
			"storagecluster/ocs-storagecluster", "-n", "openshift-storage",
			"--kubeconfig", kubeconfig,
		); err != nil {
			return fmt.Errorf("wait for StorageCluster in cluster %q: %w", clusterName, err)
		}
	}
	if peerType == "sync" {
		if err := o.configureSyncCIDRs(ctx, managedClusters); err != nil {
			return err
		}
	}

	mirrorPeer, err := support.Render(mirrorPeerTmpl, map[string]string{
		"ClusterA": strconv.Quote(managedClusters[0]),
		"ClusterB": strconv.Quote(managedClusters[1]),
		"Type":     peerType,
	})
	if err != nil {
		return err
	}
	o.logger.Info("applying ODR MirrorPeer", "cluster", o.clusterName, "type", peerType, "managedClusters", managedClusters)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, mirrorPeer); err != nil {
		return fmt.Errorf("apply ODR MirrorPeer: %w", err)
	}
	for _, clusterName := range managedClusters {
		if err := o.applyTrustedCABundle(ctx, clusterName, "openshift-dr-system", "odr-cluster-operator", "ramen-dr-cluster-operator"); err != nil {
			return err
		}
	}
	return nil
}

func (o *odr) labelManagedClusterID(ctx context.Context, name string) error {
	kubeconfig, err := rootconfig.Kubeconfig(name)
	if err != nil {
		return fmt.Errorf("load kubeconfig for managed cluster %q: %w", name, err)
	}
	result, err := o.runner.Run(ctx, support.KubeClient,
		"get", "namespace", "kube-system",
		"-o=jsonpath={.metadata.uid}",
		"--kubeconfig", kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("get kube-system namespace UID for managed cluster %q: %w", name, err)
	}
	clusterID := strings.TrimSpace(result.Stdout)
	if clusterID == "" {
		return fmt.Errorf("kube-system namespace UID for managed cluster %q is empty", name)
	}
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"label", "managedcluster", name, "clusterID="+clusterID, "--overwrite",
		"--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("label ManagedCluster %q with cluster ID: %w", name, err)
	}
	return nil
}
