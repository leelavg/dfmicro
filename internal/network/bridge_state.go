package network

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
)

type BridgeState struct {
	Name         string `json:"name"`
	Subnet       string `json:"subnet"`
	SegmentCount int    `json:"segmentCount"`
}

func LoadBridgeState(stateDir, bridgeName string) (*BridgeState, error) {
	path := filepath.Join(stateDir, fmt.Sprintf("bridge-%s.json", bridgeName))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("bridge state not found for %s", bridgeName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state BridgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (b *BridgeState) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(stateDir, fmt.Sprintf("bridge-%s.json", b.Name))
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func bridgeStateDir() string {
	return filepath.Join(rootconfig.ConfigDir(), "-networks")
}
