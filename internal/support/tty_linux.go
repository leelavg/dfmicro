//go:build linux

package support

import (
	"os"
	"syscall"
	"unsafe"
)

func stdoutIsTTY() bool {
	var termios [256]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios[0])))
	return errno == 0
}
