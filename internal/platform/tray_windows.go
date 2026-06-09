//go:build windows

package platform

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmUserTray      = 0x0400 + 1
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	nifMessage      = 0x00000001
	nifIcon         = 0x00000002
	nifTip          = 0x00000004
	nimAdd          = 0x00000000
	nimDelete       = 0x00000002
	imageIcon       = 1
	lrLoadFromFile  = 0x00000010
	lrDefaultSize   = 0x00000040
	tpmRightButton  = 0x0002
	tpmBottomAlign  = 0x0020
	tpmNonotify     = 0x0080
	tpmReturNCmd    = 0x0100
	mfString        = 0x00000000
	mfSeparator     = 0x00000800
	trayOpenCommand = 1001
	trayExitCommand = 1002
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procRegisterClassEx = user32.NewProc("RegisterClassExW")
	procCreateWindowEx  = user32.NewProc("CreateWindowExW")
	procDefWindowProc   = user32.NewProc("DefWindowProcW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
	procPostQuitMessage = user32.NewProc("PostQuitMessage")
	procGetMessage      = user32.NewProc("GetMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMsg     = user32.NewProc("DispatchMessageW")
	procLoadImage       = user32.NewProc("LoadImageW")
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procAppendMenu      = user32.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32.NewProc("DestroyMenu")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	trayWindow          uintptr
	trayURL             string
	trayStop            context.CancelFunc
	trayWindowClass     = syscall.StringToUTF16Ptr("OneSyncTrayWindow")
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSmall  uintptr
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconData struct {
	size             uint32
	hwnd             uintptr
	id               uint32
	flags            uint32
	callbackMessage  uint32
	icon             uintptr
	tip              [128]uint16
	state            uint32
	stateMask        uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guidItem         [16]byte
	balloonIcon      uintptr
}

// StartTray creates a Windows notification area icon for opening and exiting OneSync.
func StartTray(ctx context.Context, managementURL string, stop context.CancelFunc) error {
	trayURL = managementURL
	trayStop = stop
	go runTray(ctx)
	return nil
}

func runTray(ctx context.Context) {
	instance, _, _ := procGetModuleHandle.Call(0)
	class := wndClassEx{
		size:      uint32(unsafe.Sizeof(wndClassEx{})),
		wndProc:   syscall.NewCallback(trayWndProc),
		instance:  instance,
		className: trayWindowClass,
	}
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class)))
	hwnd, _, _ := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(trayWindowClass)),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("OneSync"))),
		0,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return
	}
	trayWindow = hwnd
	addTrayIcon(hwnd)
	go func() {
		<-ctx.Done()
		removeTrayIcon(hwnd)
		procDestroyWindow.Call(hwnd)
	}()
	var message msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func trayWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmUserTray:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDblClk:
			_ = OpenBrowser(trayURL)
			return 0
		case wmRButtonUp:
			showTrayMenu(hwnd)
			return 0
		}
	case wmCommand:
		switch uint32(wParam) & 0xffff {
		case trayOpenCommand:
			_ = OpenBrowser(trayURL)
			return 0
		case trayExitCommand:
			if trayStop != nil {
				trayStop()
			}
			removeTrayIcon(hwnd)
			procDestroyWindow.Call(hwnd)
			return 0
		}
	case wmDestroy:
		removeTrayIcon(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

func addTrayIcon(hwnd uintptr) {
	var data notifyIconData
	data.size = uint32(unsafe.Sizeof(data))
	data.hwnd = hwnd
	data.id = 1
	data.flags = nifMessage | nifIcon | nifTip
	data.callbackMessage = wmUserTray
	data.icon = loadTrayIcon()
	copy(data.tip[:], syscall.StringToUTF16("OneSync"))
	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
}

func removeTrayIcon(hwnd uintptr) {
	var data notifyIconData
	data.size = uint32(unsafe.Sizeof(data))
	data.hwnd = hwnd
	data.id = 1
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func loadTrayIcon() uintptr {
	executable, err := os.Executable()
	if err != nil {
		return 0
	}
	iconPath := filepath.Join(filepath.Dir(executable), "OneSync.ico")
	icon, _, _ := procLoadImage.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(iconPath))),
		imageIcon,
		0,
		0,
		lrLoadFromFile|lrDefaultSize,
	)
	return icon
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	procAppendMenu.Call(menu, mfString, trayOpenCommand, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("打开 OneSync"))))
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	procAppendMenu.Call(menu, mfString, trayExitCommand, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("退出 OneSync"))))
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForeground.Call(hwnd)
	command, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmBottomAlign|tpmNonotify|tpmReturNCmd,
		uintptr(cursor.x),
		uintptr(cursor.y),
		0,
		hwnd,
		0,
	)
	if command != 0 {
		trayWndProc(hwnd, wmCommand, command, 0)
	}
}
