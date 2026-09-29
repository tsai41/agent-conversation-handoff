//go:build darwin || linux

package menu

import (
	"os"
	"syscall"
	"unsafe"
)

// ioctlWidth returns stdout's column count, or 0 when stdout is not a
// terminal.
func ioctlWidth() int {
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
