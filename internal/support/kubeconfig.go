package support

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

type kubeConfig struct {
	APIVersion     string        `yaml:"apiVersion"`
	Kind           string        `yaml:"kind"`
	Clusters       []clusterItem `yaml:"clusters"`
	Users          []userItem    `yaml:"users"`
	Contexts       []contextItem `yaml:"contexts"`
	CurrentContext string        `yaml:"current-context"`
}

type clusterItem struct {
	Name    string `yaml:"name"`
	Cluster struct {
		CertificateAuthorityData string `yaml:"certificate-authority-data"`
		Server                   string `yaml:"server"`
	} `yaml:"cluster"`
}

type userItem struct {
	Name string `yaml:"name"`
	User struct {
		ClientCertificateData string `yaml:"client-certificate-data"`
		ClientKeyData         string `yaml:"client-key-data"`
	} `yaml:"user"`
}

type contextItem struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster string `yaml:"cluster"`
		User    string `yaml:"user"`
	} `yaml:"context"`
}

func MergeKubeconfigs(clusterName string, port int, contents []string, clients []string, destPath string) error {
	merged := kubeConfig{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters:   []clusterItem{},
		Users:      []userItem{},
		Contexts:   []contextItem{},
	}

	var userCert, userKey string

	for i, content := range contents {
		var kc kubeConfig
		if err := yaml.Unmarshal([]byte(content), &kc); err != nil {
			continue
		}

		client := clients[i]
		name := fmt.Sprintf("%s-%s", clusterName, client)

		if len(kc.Clusters) > 0 {
			ci := clusterItem{Name: name}
			ci.Cluster.CertificateAuthorityData = kc.Clusters[0].Cluster.CertificateAuthorityData
			server := kc.Clusters[0].Cluster.Server
			server = strings.ReplaceAll(server, ":6443", fmt.Sprintf(":%d", port))
			ci.Cluster.Server = server
			merged.Clusters = append(merged.Clusters, ci)
		}

		if userCert == "" && len(kc.Users) > 0 {
			userCert = kc.Users[0].User.ClientCertificateData
			userKey = kc.Users[0].User.ClientKeyData
		}

		ui := contextItem{Name: name}
		ui.Context.Cluster = name
		ui.Context.User = "user"
		merged.Contexts = append(merged.Contexts, ui)
	}

	uu := userItem{Name: "user"}
	uu.User.ClientCertificateData = userCert
	uu.User.ClientKeyData = userKey
	merged.Users = append(merged.Users, uu)

	if len(merged.Clusters) > 0 && len(clients) > 0 {
		merged.CurrentContext = fmt.Sprintf("%s-%s", clusterName, clients[0])
	}

	data, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}

	return os.WriteFile(destPath, data, 0o600)
}
