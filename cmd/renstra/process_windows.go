//go:build windows

package main

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	createNewProcessGroup          = 0x00000200
	detachedProcess                = 0x00000008
)

var (
	winKernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetExitCodeProcess = winKernel32.NewProc("GetExitCodeProcess")
)

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	r1, _, _ := procGetExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&code)))
	if r1 == 0 {
		return false
	}
	return code == stillActive
}

func stopProcess(pid int, wait time.Duration) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Kill(); err != nil && isProcessAlive(pid) {
		return err
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
		HideWindow:    true,
	}
}
