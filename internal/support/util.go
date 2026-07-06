package support

import (
	"os"
	"path/filepath"
)

var BinaryName string

func init() {
	exe, err := os.Executable()
	if err != nil {
		BinaryName = "dfmicro"
		return
	}
	BinaryName = filepath.Base(exe)
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
