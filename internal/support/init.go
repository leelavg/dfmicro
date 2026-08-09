package support

import (
	"os"
	"path/filepath"
	"runtime"
)

var BinaryName string
var IsMacOS bool

func init() {
	exe, err := os.Executable()
	if err != nil {
		BinaryName = "dfmicro"
	} else {
		BinaryName = filepath.Base(exe)
	}

	IsMacOS = runtime.GOOS == "darwin"
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
