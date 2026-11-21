//go:build windows

package cli

import (
	"syscall"
	"unsafe"
)

const enableVirtualTerminalProcessing = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// enableVT enables Windows Console Virtual Terminal Processing so ANSI escape
// color sequences render properly in PowerShell, cmd.exe, and conhost.
func enableVT(fd uintptr) {
	var mode uint32
	r1, _, _ := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&mode)))
	if r1 == 0 {
		return
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return
	}
	_, _, _ = procSetConsoleMode.Call(fd, uintptr(mode|enableVirtualTerminalProcessing))
}
