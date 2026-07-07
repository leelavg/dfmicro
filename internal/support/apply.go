package support

import (
	"bytes"
	"context"
	"embed"
	"io/fs"
	"os"
	"text/template"

	"dfmicro/internal/execx"
)

func ApplyDir(ctx context.Context, runner execx.Runner, kubectl, kubeconfig string, fsys embed.FS, dir string) error {
	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		return ApplyYAML(ctx, runner, kubectl, kubeconfig, string(data))
	})
}

func ApplyYAML(ctx context.Context, runner execx.Runner, kubectl, kubeconfig, yaml string) error {
	f, err := os.CreateTemp("", "dfmicro-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(yaml); err != nil {
		return err
	}
	f.Close()
	args := []string{"apply", "--server-side", "-f", f.Name()}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	_, err = runner.Run(ctx, kubectl, args...)
	return err
}

func Render(tmpl string, vars map[string]string) (string, error) {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
