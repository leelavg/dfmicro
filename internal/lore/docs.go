package lore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"dfmicro/internal/support"
)

func (f *fetcher) downloadDocs(ctx context.Context, version string) error {
	docsDir := filepath.Join("internal/lore/data", f.product, "docs", version)
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	manifestPath := filepath.Join(docsDir, "manifest.json")
	existingManifest, err := loadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	f.logger.Info("fetching documentation TOC", "version", version)
	tocHTML, err := f.fetchTOC(ctx, version)
	if err != nil {
		return fmt.Errorf("failed to fetch TOC: %w", err)
	}

	sections, err := extractSections(tocHTML, version)
	if err != nil {
		return fmt.Errorf("failed to extract sections: %w", err)
	}

	f.logger.Info("found documentation sections", "count", len(sections), "version", version)

	manifest := &manifest{
		DownloadDate: time.Now().UTC(),
		Files:        []file{},
		Stats:        stats{Total: len(sections)},
	}

	if existingManifest != nil {
		manifest.Files = existingManifest.Files
	}

	for i, section := range sections {
		pdfURL := fmt.Sprintf("%s/%s/pdf/%s/%s.pdf", f.cfg.DocsBaseURL, version, section, section)
		outputFile := filepath.Join(docsDir, section+".pdf")

		cached := false
		if existingManifest != nil {
			for _, fileRec := range existingManifest.Files {
				if fileRec.Name == section+".pdf" {
					f.logger.Debug("doc already cached", "section", section)
					manifest.Stats.Skipped++
					cached = true
					break
				}
			}
		}
		if cached {
			continue
		}

		f.logger.Info("downloading PDF", "section", section, "progress", fmt.Sprintf("%d/%d", i+1, len(sections)))
		if err := f.downloadFileWithRetry(ctx, pdfURL, outputFile, section); err != nil {
			f.logger.Error("failed to download PDF", "section", section, "error", err)
			manifest.Stats.Failed++
		} else {
			manifest.Stats.Successful++
			manifest.Files = append(manifest.Files, file{
				Name: section + ".pdf",
				URL:  pdfURL,
			})
		}
	}

	if err := saveManifest(manifestPath, manifest); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	f.logger.Info("download complete",
		"successful", manifest.Stats.Successful,
		"failed", manifest.Stats.Failed,
		"skipped", manifest.Stats.Skipped,
		"location", docsDir)

	return nil
}

func (f *fetcher) fetchTOC(ctx context.Context, version string) (string, error) {
	client := support.NewHTTPClient(f.cfg.HTTPTimeout())
	tocURL := fmt.Sprintf("%s/%s", f.cfg.DocsBaseURL, version)

	req, err := http.NewRequestWithContext(ctx, "GET", tocURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "dfmicro/fetch")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func (f *fetcher) downloadFileWithRetry(ctx context.Context, url, filePath, section string) error {
	client := support.NewHTTPClient(f.cfg.HTTPTimeout())
	var lastErr error
	for attempt := 1; attempt <= f.cfg.MaxRetries; attempt++ {

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", "dfmicro/fetch")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if attempt < f.cfg.MaxRetries {
				f.logger.Warn("download failed, retrying", "section", section, "attempt", attempt, "error", err)
				time.Sleep(f.cfg.RetryDelay())
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if attempt < f.cfg.MaxRetries {
				f.logger.Warn("HTTP error, retrying", "section", section, "attempt", attempt, "status", resp.StatusCode)
				time.Sleep(f.cfg.RetryDelay())
			}
			continue
		}

		file, err := os.Create(filePath)
		if err != nil {
			return err
		}

		written, err := io.Copy(file, resp.Body)
		file.Close()

		if err != nil {
			os.Remove(filePath)
			lastErr = fmt.Errorf("write failed: %w", err)
			if attempt < f.cfg.MaxRetries {
				f.logger.Warn("write failed, retrying", "section", section, "attempt", attempt, "error", err)
				time.Sleep(f.cfg.RetryDelay())
			}
			continue
		}

		if written == 0 {
			os.Remove(filePath)
			lastErr = fmt.Errorf("empty file received")
			if attempt < f.cfg.MaxRetries {
				f.logger.Warn("empty file, retrying", "section", section, "attempt", attempt)
				time.Sleep(f.cfg.RetryDelay())
			}
			continue
		}

		f.logger.Info("downloaded successfully", "section", section, "size", written)
		return nil
	}

	return fmt.Errorf("failed after %d attempts: %v", f.cfg.MaxRetries, lastErr)
}

func extractSections(html string, version string) ([]string, error) {
	pattern := regexp.MustCompile(
		fmt.Sprintf(`"/documentation/red_hat_openshift_data_foundation/%s/html/([^"]+)`, version))

	var sections []string
	for _, match := range pattern.FindAllStringSubmatch(html, -1) {
		if len(match) > 1 {
			section := match[1]
			if section != "" {
				sections = append(sections, section)
			}
		}
	}

	seen := make(map[string]bool)
	var unique []string
	for _, s := range sections {
		if !seen[s] {
			unique = append(unique, s)
			seen[s] = true
		}
	}

	return unique, nil
}
