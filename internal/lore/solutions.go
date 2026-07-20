package lore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (f *fetcher) downloadSolutions(ctx context.Context) error {
	workDir := filepath.Join("internal/lore/data", f.product)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	solrClient := newSolrClient(f.cfg)

	f.logger.Info("downloading KCS solutions", "product", f.product)

	solutionsDir := filepath.Join(workDir, "solutions")
	if err := os.MkdirAll(solutionsDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	manifestPath := filepath.Join(solutionsDir, "manifest.json")
	existingManifest, err := loadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	fq := `documentKind:("Solution") AND language:("en") AND ModerationState:("published") AND accessState:("active")`
	if f.product == "odf" {
		fq += ` AND (product:("Red Hat OpenShift Data Foundation" OR "Red Hat OpenShift Container Storage") OR sbr:("OCS"))`
	} else {
		fq += fmt.Sprintf(` AND product:"%s"`, f.product)
	}

	const pageSize = 2
	totalFetched := 0
	pageNum := 0

	manifest := &manifest{
		DownloadDate: time.Now().UTC(),
		Files:        []file{},
		Stats:        stats{},
	}

	for {
		params := map[string]any{
			"q":     "*:*",
			"fq":    fq,
			"sort":  "lastModifiedDate desc",
			"start": fmt.Sprintf("%d", pageNum*pageSize),
			"rows":  fmt.Sprintf("%d", pageSize),
			"fl":    "id,lastModifiedDate,publishedAbstract,publishedTitle,setLanguage,view_uri",
		}

		resp, err := solrClient.Query(ctx, params)
		if err != nil {
			return fmt.Errorf("failed to query solutions: %w", err)
		}

		if pageNum == 0 {
			manifest.Stats.Total = int(resp.Response.NumFound)
			f.logger.Info("found solutions", "count", manifest.Stats.Total, "product", f.product)
		}

		if len(resp.Response.Docs) == 0 {
			break
		}

		for _, doc := range resp.Response.Docs {
			docID := doc["id"].(string)
			fullParams := map[string]any{
				"q":  fmt.Sprintf("id:%s", docID),
				"fq": `documentKind:("Solution")`,
				"fl": "*",
			}
			fullResp, err := solrClient.Query(ctx, fullParams)
			if err != nil {
				f.logger.Error("failed to fetch full solution", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if len(fullResp.Response.Docs) == 0 {
				f.logger.Error("solution not found", "id", docID)
				manifest.Stats.Failed++
				continue
			}
			fullDocRaw := fullResp.Response.Docs[0]
			var fullDoc solrDocSolution
			docBytes, err := json.Marshal(fullDocRaw)
			if err != nil {
				f.logger.Error("failed to marshal solution raw", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &fullDoc); err != nil {
				f.logger.Error("failed to unmarshal solution", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			doc := make(map[string]interface{})
			docBytes, err = json.Marshal(fullDoc)
			if err != nil {
				f.logger.Error("failed to marshal typed solution", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &doc); err != nil {
				f.logger.Error("failed to unmarshal to map", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			solutionFile := filepath.Join(solutionsDir, docID+".json")

			cached := false
			if existingManifest != nil {
				for _, fileRec := range existingManifest.Files {
					if fileRec.Name == docID+".json" {
						f.logger.Debug("solution already cached", "id", docID)
						manifest.Stats.Skipped++
						manifest.Files = append(manifest.Files, file{
							Name: fileRec.Name,
							URL:  doc["view_uri"].(string),
						})
						cached = true
						break
					}
				}
			}
			if cached {
				continue
			}

			if err := os.MkdirAll(filepath.Dir(solutionFile), 0755); err != nil {
				f.logger.Error("failed to create directory", "error", err)
				manifest.Stats.Failed++
				continue
			}

			data, err := json.Marshal(doc)
			if err != nil {
				f.logger.Error("failed to marshal solution", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			if err := os.WriteFile(solutionFile, data, 0644); err != nil {
				f.logger.Error("failed to save solution", "id", docID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			manifest.Stats.Successful++
			manifest.Files = append(manifest.Files, file{
				Name: docID + ".json",
				URL:  doc["view_uri"].(string),
			})
		}

		totalFetched += len(resp.Response.Docs)
		if totalFetched >= manifest.Stats.Total {
			break
		}

		if pageNum == 0 {
			break
		}

		pageNum++
	}

	if err := saveManifest(manifestPath, manifest); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	f.logger.Info("solutions download complete",
		"successful", manifest.Stats.Successful,
		"failed", manifest.Stats.Failed,
		"skipped", manifest.Stats.Skipped,
		"location", solutionsDir)

	return nil
}
