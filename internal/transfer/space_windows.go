//go:build windows

package transfer

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceExWProc = kernel32.NewProc("GetDiskFreeSpaceExW")

func availableSpace(filePath string) (uint64, error) {
	pathPointer, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return 0, err
	}
	var available uint64
	result, _, callErr := getDiskFreeSpaceExWProc.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if result == 0 {
		return 0, callErr
	}
	return available, nil
}
