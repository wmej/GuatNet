//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procReleaseCapture        = user32.NewProc("ReleaseCapture")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
)

const (
	gwlStyle      = ^uintptr(15) // -16 的无符号表示
	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000

	swHide     = 0
	swShow     = 5
	swMinimize = 6
	swRestore  = 9

	wmNCLButtonDown = 0x00A1
	wmSetIcon       = 0x0080
	iconSmall       = 0
	iconBig         = 1
	htCaption       = 2

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	dwmwaWindowCornerPreference = 33
	dwmcpRound                  = 2

	mbIconInfo = 0x40

	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == -4
	dpiPerMonitorV2 = ^uintptr(3)
)

// enableDPIAwareness 必须在任何窗口创建之前调用。
//
// 不声明 DPI 感知的话，系统会把整个窗口当成低分辨率位图拉伸，
// 在 125%/150% 缩放的屏幕上文字就是糊的。
func enableDPIAwareness() {
	if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiPerMonitorV2); r != 0 {
		return // Windows 10 1703+
	}
	procSetProcessDPIAware.Call() // 更老的系统
}

// setWindowIcon 把 exe 里嵌的图标设成窗口图标（任务栏、Alt+Tab 用）。
func setWindowIcon(hwnd uintptr) {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	hIcon, _, _ := procLoadIconW.Call(hInst, 1)
	if hIcon == 0 {
		return
	}
	procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIcon)
	procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIcon)
}

// makeBorderless 去掉系统标题栏和边框，只留内容区。
func makeBorderless(hwnd uintptr) {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
	style &^= wsCaption | wsThickFrame | wsSysMenu | wsMaximizeBox
	style |= wsMinimizeBox
	procSetWindowLongPtrW.Call(hwnd, gwlStyle, style)

	// 让新样式立刻生效，并触发一次 WM_SIZE 让 WebView2 重新铺满
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)

	// Windows 11 圆角
	pref := int32(dwmcpRound)
	procDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
}

// dragWindow 让无边框窗口跟随鼠标拖动：
// 把自己伪装成"按住标题栏"，交给系统移动窗口。
func dragWindow(hwnd uintptr) {
	procReleaseCapture.Call()
	procSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
}

// systemDPI 返回主屏的 DPI（96 为 100% 缩放）。
//
// 因为程序声明了 per-monitor DPI 感知，CreateWindowEx 收到的尺寸是**物理像素**。
// 想按"逻辑尺寸"开窗，就必须自己乘上缩放比，否则 150% 的屏幕上窗口会小一圈。
func systemDPI() float64 {
	if dpi, _, _ := procGetDpiForSystem.Call(); dpi != 0 {
		return float64(dpi)
	}
	return 96
}

// screenSize 返回主屏物理分辨率。
func screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	return int(w), int(h)
}

// minimizeWindow 正常最小化到任务栏（不是收到托盘）。
func minimizeWindow(hwnd uintptr) {
	procShowWindow.Call(hwnd, swMinimize)
}

// openExternal 用系统默认浏览器打开链接。
// 只允许 https://github.com/ ，避免设置里被塞进别的地址。
func openExternal(url string) {
	if !strings.HasPrefix(url, "https://github.com/") {
		return
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(url)
	procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, 1) // SW_SHOWNORMAL
}

func hideWindow(hwnd uintptr) {
	procShowWindow.Call(hwnd, swHide)
}

func showWindow(hwnd uintptr) {
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindow.Call(hwnd)
}

// messageBox 在没有 WebView2 运行时给用户一个看得见的提示。
func messageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconInfo)
}
