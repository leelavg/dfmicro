//go:build !linux && !darwin

package support

func stdoutIsTTY() bool {
	return false
}
