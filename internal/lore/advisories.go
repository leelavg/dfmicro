package lore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (f *fetcher) downloadAdvisories(ctx context.Context) error {
	workDir := filepath.Join("internal/lore/data", f.product)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	solrClient := newSolrClient(f.cfg)

	for _, ver := range f.versions {
		f.logger.Info("downloading advisories", "product", f.product, "version", ver)

		advisoryDir := filepath.Join(workDir, "advisories", ver)
		if err := os.MkdirAll(advisoryDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		manifestPath := filepath.Join(advisoryDir, "manifest.json")
		existingManifest, _ := loadManifest(manifestPath)

		fqFilters := []string{
			`documentKind:("Errata")`,
			fmt.Sprintf(`portal_product_filter:Red\ Hat\ OpenShift\ Data\ Foundation|*|%s|*`, ver),
			`language:("en")`,
		}

		if existingManifest != nil {
			dateStr := existingManifest.DownloadDate.UTC().Format("2006-01-02T15:04:05Z")
			fqFilters = append(fqFilters, fmt.Sprintf(`lastModifiedDate:[%s TO NOW]`, dateStr))
		}

		params := map[string]any{
			"q":    "*:*",
			"fq":   fqFilters,
			"sort": "portal_publication_date desc",
			"rows": "2",
			"fl":   "id,language,lastModifiedDate,portal_description,portal_solution,view_uri",
		}

		resp, err := solrClient.Query(ctx, params)
		if err != nil {
			return fmt.Errorf("failed to query Solr: %w", err)
		}

		foundCount := int(resp.Response.NumFound)
		f.logger.Info("found advisories", "count", foundCount, "product", f.product, "version", ver)

		manifest := &manifest{
			DownloadDate: time.Now().UTC(),
			Files:        []file{},
			Stats: stats{
				Total:   foundCount,
				BaseURL: "https://access.redhat.com/errata",
			},
		}

		if existingManifest != nil {
			manifest.Files = existingManifest.Files
		}

		for _, doc := range resp.Response.Docs {
			var advisory solrDocAdvisory
			docBytes, err := json.Marshal(doc)
			if err != nil {
				f.logger.Error("failed to marshal advisory raw", "id", doc["id"], "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &advisory); err != nil {
				f.logger.Error("failed to unmarshal advisory", "id", doc["id"], "error", err)
				manifest.Stats.Failed++
				continue
			}

			advisoryFile := filepath.Join(advisoryDir, advisory.ID+".json")

			if err := os.MkdirAll(filepath.Dir(advisoryFile), 0755); err != nil {
				f.logger.Error("failed to create directory", "error", err)
				manifest.Stats.Failed++
				continue
			}

			data, err := json.Marshal(advisory)
			if err != nil {
				f.logger.Error("failed to marshal advisory", "id", advisory.ID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			if err := os.WriteFile(advisoryFile, data, 0644); err != nil {
				f.logger.Error("failed to save advisory", "id", advisory.ID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			manifest.Stats.Successful++
			manifest.Files = append(manifest.Files, file{
				Name: advisory.ID + ".json",
				URL:  fmt.Sprintf("https://access.redhat.com/errata/%s", advisory.ID),
			})
		}

		if err := saveManifest(manifestPath, manifest); err != nil {
			return fmt.Errorf("failed to save manifest: %w", err)
		}

		f.logger.Info("advisory download complete",
			"successful", manifest.Stats.Successful,
			"failed", manifest.Stats.Failed,
			"skipped", manifest.Stats.Skipped,
			"location", advisoryDir)
	}

	return nil
}
