package support

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var BinaryName string
var IsMacOS bool
var KubeClient string

func init() {
	exe, err := os.Executable()
	if err != nil {
		BinaryName = "dfmicro"
	} else {
		BinaryName = filepath.Base(exe)
	}

	IsMacOS = runtime.GOOS == "darwin"
	if _, err := exec.LookPath("oc"); err == nil {
		KubeClient = "oc"
	} else {
		KubeClient = "kubectl"
	}
}

func Must[T any](value T, err error) T {
	MustOK(err)
	return value
}

func MustOK(err error) {
	if err != nil {
		panic(err)
	}
}
