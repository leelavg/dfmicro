package lore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type manifest struct {
	DownloadDate time.Time `json:"download_date"`
	Files        []file    `json:"files"`
	Stats        stats     `json:"stats"`
}

type stats struct {
	Total      int    `json:"total"`
	Successful int    `json:"successful"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	BaseURL    string `json:"base_url,omitempty"`
}

type file struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

func (m *manifest) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"download_date": m.DownloadDate.UTC().Format("2006-01-02T15:04:05Z"),
		"files":         m.Files,
		"stats":         m.Stats,
	})
}

func loadManifest(path string) (*manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &m, nil
}

func saveManifest(path string, m *manifest) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}
