package support

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

type idms struct {
	Spec struct {
		ImageDigestMirrors []struct {
			Source  string   `yaml:"source"`
			Mirrors []string `yaml:"mirrors"`
		} `yaml:"imageDigestMirrors"`
	} `yaml:"spec"`
}

type IDMSResult struct {
	RegistriesConf string
	PolicyJSON     string
}

type policyRule struct {
	Type string `json:"type"`
}

type policyScopes map[string][]policyRule

type policy struct {
	Default    []policyRule            `json:"default"`
	Transports map[string]policyScopes `json:"transports"`
}

func ConvertIDMSFiles(paths []string) (IDMSResult, error) {
	var mirrors strings.Builder
	sources := map[string]struct{}{}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return IDMSResult{}, fmt.Errorf("read idms %s: %w", path, err)
		}

		var doc idms
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return IDMSResult{}, fmt.Errorf("parse idms %s: %w", path, err)
		}

		for _, entry := range doc.Spec.ImageDigestMirrors {
			fmt.Fprintf(&mirrors, "[[registry]]\nlocation = %q\nmirror-by-digest-only = true\n", entry.Source)
			for _, mirror := range entry.Mirrors {
				fmt.Fprintf(&mirrors, "\n  [[registry.mirror]]\n  location = %q\n", mirror)
			}
			mirrors.WriteByte('\n')
			parts := strings.SplitN(entry.Source, "/", 2)
			sources[parts[0]] = struct{}{}
		}
	}

	accept := []policyRule{{Type: "insecureAcceptAnything"}}
	scopes := policyScopes{}
	for src := range sources {
		scopes[src] = accept
	}
	p := policy{
		Default:    accept,
		Transports: map[string]policyScopes{"docker": scopes},
	}
	policyData, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return IDMSResult{}, err
	}

	return IDMSResult{RegistriesConf: mirrors.String(), PolicyJSON: string(policyData) + "\n"}, nil
}
