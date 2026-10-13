package locals3

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
)

func loadState() ([]string, error) {
	data, err := os.ReadFile(statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var clusters []string
	if err := json.Unmarshal(data, &clusters); err != nil {
		return nil, err
	}
	return clusters, nil
}

func saveState(clusters []string) error {
	path := statePath()
	if len(clusters) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(clusters, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func statePath() string {
	return filepath.Join(rootconfig.ConfigDir(), "local-s3.json")
}
