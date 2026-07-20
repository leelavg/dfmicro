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

		params := map[string]any{
			"q": "*:*",
			"fq": []string{
				`documentKind:("Errata")`,
				fmt.Sprintf(`portal_product_filter:Red\ Hat\ OpenShift\ Data\ Foundation|*|%s|*`, ver),
				`language:("en")`,
			},
			"sort": "portal_publication_date desc",
			"rows": "2",
			"fl":   "id,language,lastModifiedDate,portal_description,portal_solution,view_uri",
		}

		resp, err := solrClient.Query(ctx, params)
		if err != nil {
			return fmt.Errorf("failed to query Solr: %w", err)
		}

		f.logger.Info("found advisories", "count", resp.Response.NumFound, "product", f.product, "version", ver)

		manifest := &manifest{
			DownloadDate: time.Now().UTC(),
			Files:        []file{},
			Stats: stats{
				Total:   int(resp.Response.NumFound),
				BaseURL: "https://access.redhat.com/errata",
			},
		}

		for _, doc := range resp.Response.Docs {
			docID := doc["id"].(string)
			fullParams := map[string]any{
				"q":  fmt.Sprintf("id:%s", docID),
				"fq": `documentKind:("Errata")`,
				"fl": "*",
			}
			fullResp, err := solrClient.Query(ctx, fullParams)
			if err != nil {
				f.logger.Error("failed to fetch full advisory", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if len(fullResp.Response.Docs) == 0 {
				f.logger.Error("advisory not found", "id", docID)
				manifest.Stats.Failed++
				continue
			}
			fullDocRaw := fullResp.Response.Docs[0]
			var fullDoc solrDocAdvisory
			docBytes, err := json.Marshal(fullDocRaw)
			if err != nil {
				f.logger.Error("failed to marshal advisory raw", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &fullDoc); err != nil {
				f.logger.Error("failed to unmarshal advisory", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			doc := make(map[string]interface{})
			docBytes, err = json.Marshal(fullDoc)
			if err != nil {
				f.logger.Error("failed to marshal typed advisory", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &doc); err != nil {
				f.logger.Error("failed to unmarshal to map", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			advisoryFile := filepath.Join(advisoryDir, docID+".json")

			if existingManifest != nil {
				for _, fileRec := range existingManifest.Files {
					if fileRec.Name == docID+".json" {
						f.logger.Debug("advisory already cached", "id", docID)
						manifest.Stats.Skipped++
						manifest.Files = append(manifest.Files, file{
							Name: fileRec.Name,
							URL:  fmt.Sprintf("https://access.redhat.com/errata/%s", docID),
						})
						continue
					}
				}
			}

			if err := os.MkdirAll(filepath.Dir(advisoryFile), 0755); err != nil {
				f.logger.Error("failed to create directory", "error", err)
				manifest.Stats.Failed++
				continue
			}

			data, err := json.Marshal(doc)
			if err != nil {
				f.logger.Error("failed to marshal advisory", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			if err := os.WriteFile(advisoryFile, data, 0644); err != nil {
				f.logger.Error("failed to save advisory", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			manifest.Stats.Successful++
			manifest.Files = append(manifest.Files, file{
				Name: docID + ".json",
				URL:  fmt.Sprintf("https://access.redhat.com/errata/%s", docID),
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
