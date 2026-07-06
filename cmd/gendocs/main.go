package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"dfmicro/internal/app"
	"dfmicro/internal/execx"

	docs "github.com/urfave/cli-docs/v3"
)

func main() {
	out := "internal/docs/cli.md"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	cmd := app.Command(slog.Default(), execx.PanicRunner{})
	md, err := docs.ToMarkdown(cmd)
	if err != nil {
		slog.Error("failed to generate docs", "error", err)
		os.Exit(1)
	}

	md = strings.ReplaceAll(md, filepath.Base(os.Args[0]), "dfmicro")
	if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
		slog.Error("failed to write docs", "error", err)
		os.Exit(1)
	}
}
