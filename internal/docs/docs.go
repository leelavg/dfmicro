package docs

import _ "embed"

//go:generate go run ../../cmd/gendocs ../../internal/docs/cli.md

//go:embed cli.md
var CLI string
