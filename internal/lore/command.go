package lore

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"dfmicro/internal/buildinfo"
	"dfmicro/internal/support"
	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger) *cli.Command {
	cmd := &cli.Command{
		Name:                  "fetch",
		Usage:                 "Download Red Hat knowledge base content",
		Version:               buildinfo.String(),
		EnableShellCompletion: true,
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if _, err := os.Stat("internal/lore"); err != nil {
				return ctx, fmt.Errorf("fetch must be run from repository root (internal/lore/ not found)")
			}
			return ctx, nil
		},
		Commands: []*cli.Command{
			docsCmd(logger),
			advisoriesCmd(logger),
			articlesCmd(logger),
			solutionsCmd(logger),
			runbooksCmd(logger),
		},
	}

	support.SortCommand(cmd)
	return cmd
}

func docsCmd(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "docs",
		Usage:     "Download ODF documentation PDFs",
		UsageText: "fetch docs --version 4.21 --version 4.22",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "version",
				Usage:    "Version to download (repeatable: --version 4.21 --version 4.22)",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return newFetcher(loadConfig(), logger).withVersions(cmd.StringSlice("version")).docs(ctx)
		},
	}
}

func advisoriesCmd(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "advisories",
		Usage:     "Download Red Hat errata/advisories",
		UsageText: "fetch advisories --product odf --version 4.21 --version 4.22",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "product",
				Usage: "Product name",
				Value: loadConfig().DefaultProduct,
			},
			&cli.StringSliceFlag{
				Name:     "version",
				Usage:    "Version to download (repeatable: --version 4.21 --version 4.22)",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return newFetcher(loadConfig(), logger).
				withProduct(cmd.String("product")).
				withVersions(cmd.StringSlice("version")).
				advisory(ctx)
		},
	}
}

func articlesCmd(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "articles",
		Usage:     "Download Red Hat KCS articles",
		UsageText: "fetch articles --product odf",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "product",
				Usage: "Product name",
				Value: loadConfig().DefaultProduct,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return newFetcher(loadConfig(), logger).withProduct(cmd.String("product")).articles(ctx)
		},
	}
}

func solutionsCmd(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "solutions",
		Usage:     "Download Red Hat KCS solutions",
		UsageText: "fetch solutions --product odf",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "product",
				Usage: "Product name",
				Value: loadConfig().DefaultProduct,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return newFetcher(loadConfig(), logger).withProduct(cmd.String("product")).solutions(ctx)
		},
	}
}

func runbooksCmd(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "runbooks",
		Usage:     "Download OpenShift runbooks",
		UsageText: "fetch runbooks --product odf",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "product",
				Usage: "Product name",
				Value: loadConfig().DefaultProduct,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return newFetcher(loadConfig(), logger).withProduct(cmd.String("product")).runbooks(ctx)
		},
	}
}
