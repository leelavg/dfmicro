package lore

import (
	"context"
	"log/slog"
)

type fetcher struct {
	cfg      config
	product  string
	versions []string
	logger   *slog.Logger
}

func newFetcher(cfg config, logger *slog.Logger) *fetcher {
	return &fetcher{
		cfg:     cfg,
		product: cfg.DefaultProduct,
		logger:  logger,
	}
}

func (f *fetcher) withProduct(p string) *fetcher {
	f.product = p
	return f
}

func (f *fetcher) withVersions(versions []string) *fetcher {
	f.versions = versions
	return f
}

func (f *fetcher) docs(ctx context.Context) error {
	for _, v := range f.versions {
		if err := f.downloadDocs(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

func (f *fetcher) advisory(ctx context.Context) error {
	return f.downloadAdvisories(ctx)
}

func (f *fetcher) articles(ctx context.Context) error {
	return f.downloadArticles(ctx)
}

func (f *fetcher) solutions(ctx context.Context) error {
	return f.downloadSolutions(ctx)
}

func (f *fetcher) runbooks(ctx context.Context) error {
	return f.downloadRunbooks(ctx)
}
