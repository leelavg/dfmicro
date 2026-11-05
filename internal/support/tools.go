package support

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
)

const clusteradmVersion = "v1.4.0"
const maxClusteradmArchiveSize = 50_000_000

// Pin both release archives and executables so only verified bytes reach tar or execution.
var clusteradmReleases = map[string]struct {
	archive    string
	archiveSHA string
	binarySHA  string
}{
	"darwin/amd64": {
		archive:    "clusteradm_darwin_amd64.tar.gz",
		archiveSHA: "fb269e7516dedd5c456639918a33000640df72db473755a1fa9a650a78fd7f97",
		binarySHA:  "83fb31c00082c5f66b9bb61714db6d85a70b23e55ddf87ba780cea52829182b2",
	},
	"darwin/arm64": {
		archive:    "clusteradm_darwin_arm64.tar.gz",
		archiveSHA: "fe9bc31db7911093d75fefebda98cda3dc894eee922ea2b9538a31232ff2b960",
		binarySHA:  "5e515b23025b9ed620f510d77ca4e83e9abf4154daad6cbf48a3e92ca94a9565",
	},
	"linux/amd64": {
		archive:    "clusteradm_linux_amd64.tar.gz",
		archiveSHA: "aa94241caf353a515a0778e0ce608b4c68ced9b3e7f6fb5fe23f48393926153b",
		binarySHA:  "eb85d5289d31da341833cd75185aed0acd05784823a3e138654e38f86576359e",
	},
	"linux/arm64": {
		archive:    "clusteradm_linux_arm64.tar.gz",
		archiveSHA: "37fa11ea802f526ed3226af892b4066f9e8224ac9d620849f6cdc1ecd074560b",
		binarySHA:  "ad735f3f8341a21ecc2850d176d67d46c28fb53329ff403a6c4f13b684537a74",
	},
}

// ClusteradmPath returns the managed clusteradm binary path.
func ClusteradmPath() string {
	return filepath.Join(rootconfig.ConfigDir(), ",tools", "clusteradm")
}

func EnsureClusteradm(ctx context.Context, runner execx.Runner, logger *slog.Logger) (string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	release, ok := clusteradmReleases[platform]
	if !ok {
		return "", fmt.Errorf("clusteradm %s has no release for %s", clusteradmVersion, platform)
	}

	binaryPath := ClusteradmPath()
	toolsDir := filepath.Dir(binaryPath)
	if err := ensureToolsDir(toolsDir); err != nil {
		return "", err
	}
	if info, err := os.Lstat(binaryPath); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("clusteradm tool path %q is not a regular file", binaryPath)
		}
		if safeExecutable(info) {
			if err := verifySHA256(binaryPath, release.binarySHA); err == nil {
				return binaryPath, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect clusteradm tool path: %w", err)
	}

	logger.Info("downloading clusteradm", "version", clusteradmVersion, "platform", platform)
	tempDir, err := os.MkdirTemp(toolsDir, ".clusteradm-")
	if err != nil {
		return "", fmt.Errorf("create temporary clusteradm directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	archive := filepath.Join(tempDir, release.archive)
	url := "https://github.com/open-cluster-management-io/clusteradm/releases/download/" + clusteradmVersion + "/" + release.archive
	if err := downloadClusteradm(ctx, runner, url, archive); err != nil {
		return "", err
	}
	info, err := os.Lstat(archive)
	if err != nil {
		return "", fmt.Errorf("inspect clusteradm archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxClusteradmArchiveSize {
		return "", fmt.Errorf("clusteradm archive has invalid type or size: %d bytes", info.Size())
	}
	if err := verifySHA256(archive, release.archiveSHA); err != nil {
		return "", fmt.Errorf("verify clusteradm archive: %w", err)
	}
	if _, err := execx.Run(ctx, runner, "tar", "-xzf", archive, "-C", tempDir, "clusteradm"); err != nil {
		return "", fmt.Errorf("extract clusteradm binary: %w", err)
	}
	tempBinary := filepath.Join(tempDir, "clusteradm")
	info, err = os.Lstat(tempBinary)
	if err != nil {
		return "", fmt.Errorf("inspect extracted clusteradm binary: %w", err)
	}
	if !safeExecutable(info) {
		return "", fmt.Errorf("extracted clusteradm has unsafe file type or permissions")
	}
	if err := verifySHA256(tempBinary, release.binarySHA); err != nil {
		return "", fmt.Errorf("verify clusteradm binary: %w", err)
	}
	if err := os.Rename(tempBinary, binaryPath); err != nil {
		return "", fmt.Errorf("install clusteradm binary: %w", err)
	}

	return binaryPath, nil
}

func safeExecutable(info os.FileInfo) bool {
	mode := info.Mode()
	return mode.IsRegular() && mode.Perm()&0o100 != 0 && mode.Perm()&0o022 == 0 && mode&os.ModeSetuid == 0 && mode&os.ModeSetgid == 0
}

func ensureToolsDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create dfmicro config directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create dfmicro tools directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect dfmicro tools directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dfmicro tools path %q is not a directory", dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("dfmicro tools directory %q is writable by other users", dir)
	}
	return nil
}

func downloadClusteradm(ctx context.Context, runner execx.Runner, url, dest string) error {
	command, err := exec.LookPath("curl")
	if err != nil {
		return fmt.Errorf("download clusteradm: curl is required")
	}

	args := []string{"--fail", "--location", "--silent", "--show-error", "--max-filesize", fmt.Sprint(maxClusteradmArchiveSize), "--proto", "=https", "--proto-redir", "=https", "--output", dest, url}
	downloadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := execx.Run(downloadCtx, runner, command, args...); err != nil {
		return fmt.Errorf("download clusteradm: %w", err)
	}
	return nil
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if actual := hex.EncodeToString(h.Sum(nil)); actual != expected {
		return fmt.Errorf("checksum mismatch: got %s", actual)
	}
	return nil
}
