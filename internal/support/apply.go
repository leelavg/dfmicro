package support

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"text/template"

	"dfmicro/internal/execx"
)

func ApplyDir(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig string, fsys embed.FS, dir string) error {
	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		return ApplyYAML(ctx, runner, kubeClient, kubeconfig, string(data))
	})
}

func ApplyDirIfMissing(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig string, fsys embed.FS, dir string) error {
	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp("", "dfmicro-*.yaml")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		args := []string{"get", "-f", f.Name(), "--ignore-not-found", "-o", "name"}
		if kubeconfig != "" {
			args = append(args, "--kubeconfig", kubeconfig)
		}
		result, err := runner.Run(ctx, kubeClient, args...)
		if err != nil {
			return fmt.Errorf("check shim %s: %w", path, err)
		}
		if strings.TrimSpace(result.Stdout) != "" {
			return nil
		}
		args = []string{"apply", "--server-side", "-f", f.Name()}
		if kubeconfig != "" {
			args = append(args, "--kubeconfig", kubeconfig)
		}
		if _, err := runner.Run(ctx, kubeClient, args...); err != nil {
			return fmt.Errorf("apply shim %s: %w", path, err)
		}
		return nil
	})
}

func ApplyYAML(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, yaml string) error {
	f, err := os.CreateTemp("", "dfmicro-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(yaml); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := []string{"apply", "--server-side", "-f", f.Name()}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	_, err = runner.Run(ctx, kubeClient, args...)
	return err
}

func ApplyKustomization(ctx context.Context, runner execx.Runner, container, path string) error {
	_, err := RunPodmanPrivileged(ctx, runner, "exec", container, "oc", "apply", "-k", path)
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
