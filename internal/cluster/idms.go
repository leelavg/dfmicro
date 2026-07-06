package cluster

import (
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

func convertIDMSFiles(paths []string) (string, error) {
	var sb strings.Builder

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read idms %s: %w", path, err)
		}

		var policy idms
		if err := yaml.Unmarshal(data, &policy); err != nil {
			return "", fmt.Errorf("parse idms %s: %w", path, err)
		}

		for _, entry := range policy.Spec.ImageDigestMirrors {
			fmt.Fprintf(&sb, "[[registry]]\nlocation = %q\nmirror-by-digest-only = true\n", entry.Source)
			for _, mirror := range entry.Mirrors {
				fmt.Fprintf(&sb, "\n  [[registry.mirror]]\n  location = %q\n", mirror)
			}
			sb.WriteByte('\n')
		}
	}

	return sb.String(), nil
}
