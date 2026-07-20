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

	fqFilters := []string{
		`documentKind:("Solution")`,
		`language:("en")`,
		`ModerationState:("published")`,
		`accessState:("active")`,
	}

	if f.product == "odf" {
		fqFilters = append(fqFilters, `(product:("Red Hat OpenShift Data Foundation" OR "Red Hat OpenShift Container Storage") OR sbr:("OCS"))`)
	} else {
		fqFilters = append(fqFilters, fmt.Sprintf(`product:"%s"`, f.product))
	}

	if existingManifest != nil {
		dateStr := existingManifest.DownloadDate.UTC().Format("2006-01-02T15:04:05Z")
		fqFilters = append(fqFilters, fmt.Sprintf(`lastModifiedDate:[%s TO NOW]`, dateStr))
	}

	manifest := &manifest{
		DownloadDate: time.Now().UTC(),
		Files:        []file{},
		Stats:        stats{},
	}

	if existingManifest != nil {
		manifest.Files = existingManifest.Files
	}

	const pageSize = 30
	pageNum := 0

	for {
		params := map[string]any{
			"q":     "*:*",
			"fq":    fqFilters,
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
			foundCount := int(resp.Response.NumFound)
			manifest.Stats.Total = foundCount
			f.logger.Info("found solutions", "count", foundCount, "product", f.product)
		}

		if len(resp.Response.Docs) == 0 {
			break
		}

		for _, doc := range resp.Response.Docs {
			var solution solrDocSolution
			docBytes, err := json.Marshal(doc)
			if err != nil {
				f.logger.Error("failed to marshal solution raw", "id", doc["id"], "error", err)
				manifest.Stats.Failed++
				continue
			}
			if err := json.Unmarshal(docBytes, &solution); err != nil {
				f.logger.Error("failed to unmarshal solution", "id", doc["id"], "error", err)
				manifest.Stats.Failed++
				continue
			}

			solutionFile := filepath.Join(solutionsDir, solution.ID+".json")

			if err := os.MkdirAll(filepath.Dir(solutionFile), 0755); err != nil {
				f.logger.Error("failed to create directory", "error", err)
				manifest.Stats.Failed++
				continue
			}

			data, err := json.Marshal(solution)
			if err != nil {
				f.logger.Error("failed to marshal solution", "id", solution.ID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			if err := os.WriteFile(solutionFile, data, 0644); err != nil {
				f.logger.Error("failed to save solution", "id", solution.ID, "error", err)
				manifest.Stats.Failed++
				continue
			}

			manifest.Stats.Successful++
			manifest.Files = append(manifest.Files, file{
				Name: solution.ID + ".json",
				URL:  solution.ViewURI,
			})
		}

		pageNum++
		time.Sleep(time.Second)
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
