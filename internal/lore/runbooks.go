package lore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dfmicro/internal/support"
)

type githubFile struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	DownloadURL string `json:"download_url"`
	URL         string `json:"url"`
}

func (f *fetcher) downloadRunbooks(ctx context.Context) error {
	runbookDir := filepath.Join("internal/lore/data", f.product, "runbooks")
	if err := os.MkdirAll(runbookDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	f.logger.Info("downloading runbooks", "product", f.product)

	latestCommitTime, err := f.getLatestCommitTime(ctx)
	if err != nil {
		return fmt.Errorf("failed to get latest commit time: %w", err)
	}

	existingManifest, err := loadManifest(filepath.Join(runbookDir, "manifest.json"))
	if err != nil {
		f.logger.Error("failed to load existing manifest", "error", err)
	}

	if existingManifest != nil && existingManifest.DownloadDate.After(latestCommitTime) {
		existingManifest.DownloadDate = time.Now().UTC()
		if err := saveManifest(filepath.Join(runbookDir, "manifest.json"), existingManifest); err != nil {
			f.logger.Error("failed to update manifest", "error", err)
		}
		f.logger.Info("runbooks already up to date", "product", f.product)
		return nil
	}

	files, err := f.listAllRunbookFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list runbook files: %w", err)
	}

	f.logger.Info("found runbook files", "count", len(files), "product", f.product)

	manifest := &manifest{
		DownloadDate: time.Now().UTC(),
		Files:        []file{},
		Stats: stats{
			Total: len(files),
		},
	}

	for _, ghfile := range files {
		if strings.HasSuffix(ghfile.Path, ".md") == false {
			continue
		}

		cached := false
		if existingManifest != nil {
			for _, fbk := range existingManifest.Files {
				if fbk.Name == ghfile.Name {
					f.logger.Debug("runbook already cached", "name", ghfile.Name)
					manifest.Stats.Skipped++
					manifest.Files = append(manifest.Files, fbk)
					cached = true
					break
				}
			}
		}
		if cached {
			continue
		}

		fileContent, err := f.downloadRunbookFile(ctx, ghfile.DownloadURL)
		if err != nil {
			f.logger.Error("failed to download runbook file", "path", ghfile.Path, "error", err)
			manifest.Stats.Failed++
			continue
		}

		relPath := strings.TrimPrefix(ghfile.Path, "alerts/openshift-container-storage-operator/")
		filePath := filepath.Join(runbookDir, relPath)

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			f.logger.Error("failed to create directory", "path", filePath, "error", err)
			manifest.Stats.Failed++
			continue
		}

		if err := os.WriteFile(filePath, fileContent, 0644); err != nil {
			f.logger.Error("failed to save runbook file", "path", ghfile.Path, "error", err)
			manifest.Stats.Failed++
			continue
		}

		manifest.Stats.Successful++
		manifest.Files = append(manifest.Files, file{
			Name: ghfile.Name,
			URL:  ghfile.DownloadURL,
		})

	}

	if err := saveManifest(filepath.Join(runbookDir, "manifest.json"), manifest); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	f.logger.Info("runbooks download complete",
		"successful", manifest.Stats.Successful,
		"failed", manifest.Stats.Failed,
		"skipped", manifest.Stats.Skipped,
		"location", runbookDir)

	return nil
}

func (f *fetcher) getLatestCommitTime(ctx context.Context) (time.Time, error) {
	client := support.NewHTTPClient(f.cfg.HTTPTimeout())
	url := "https://api.github.com/repos/openshift/runbooks/commits?path=alerts/openshift-container-storage-operator&per_page=1"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("User-Agent", "dfmicro/fetch")

	resp, err := client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return time.Time{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var commits []struct {
		Commit struct {
			Author struct {
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return time.Time{}, err
	}

	if len(commits) == 0 {
		return time.Time{}, fmt.Errorf("no commits found")
	}

	commitTime, err := time.Parse(time.RFC3339, commits[0].Commit.Author.Date)
	if err != nil {
		return time.Time{}, err
	}

	return commitTime, nil
}

func (f *fetcher) listAllRunbookFiles(ctx context.Context) ([]githubFile, error) {
	client := support.NewHTTPClient(f.cfg.HTTPTimeout())
	url := "https://api.github.com/repos/openshift/runbooks/contents/alerts/openshift-container-storage-operator"

	var allFiles []githubFile
	if err := f.walkGitHubDir(ctx, client, url, &allFiles); err != nil {
		return nil, err
	}

	return allFiles, nil
}

func (f *fetcher) walkGitHubDir(ctx context.Context, client *http.Client, url string, allFiles *[]githubFile) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "dfmicro/fetch")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var items []githubFile
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return err
	}

	for _, item := range items {
		if item.Type == "file" && strings.HasSuffix(item.Name, ".md") {
			*allFiles = append(*allFiles, item)
		} else if item.Type == "dir" && item.Name != "screenshots" {
			if err := f.walkGitHubDir(ctx, client, item.URL, allFiles); err != nil {
				return err
			}
		}
	}

	return nil
}

func (f *fetcher) downloadRunbookFile(ctx context.Context, url string) ([]byte, error) {
	client := support.NewHTTPClient(f.cfg.HTTPTimeout())
	var lastErr error

	for attempt := 1; attempt <= f.cfg.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "dfmicro/fetch")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d/%d: %w", attempt, f.cfg.MaxRetries, err)
			if attempt < f.cfg.MaxRetries {
				selectTimeout(ctx, f.cfg.RetryDelay())
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if attempt < f.cfg.MaxRetries {
				selectTimeout(ctx, f.cfg.RetryDelay())
			}
			continue
		}

		return io.ReadAll(resp.Body)
	}

	return nil, fmt.Errorf("all retries exhausted: %v", lastErr)
}
