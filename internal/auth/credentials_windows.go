//go:build windows

package auth

import (
	"syscall"
	"unsafe"
)

var (
	credentialKernel32 = syscall.NewLazyDLL("kernel32.dll")
	credentialMoveFile = credentialKernel32.NewProc("MoveFileExW")
)

func replaceCredentialFile(source, target string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPointer, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := credentialMoveFile.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(targetPointer)),
		0x1|0x8,
	)
	if result == 0 {
		return callErr
	}
	return nil
}
