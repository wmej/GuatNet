//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// 单实例互斥量。句柄留着不关，进程退出时由系统释放。
var singleInstanceMutex windows.Handle

var (
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procFindWindowW  = user32.NewProc("FindWindowW")
)

// ensureSingleInstance 保证同一登录会话里只跑一个实例。
//
// 双开会让两个守护线程各跑一套 45 秒冷却：任何一方重新认证
// 都会把对方顶下线，双方再来回抢登录，正好踩中门户的强制注销。
func ensureSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\GuatNet-single-instance`)
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return true // 互斥量都建不出来就别挡着启动
	}

	// x/sys 的 CreateMutex 封装只在句柄为 0 时返回错误，
	// 这里自己判 ERROR_ALREADY_EXISTS。
	if errno, ok := callErr.(windows.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(windows.Handle(h))
		// 已有实例的窗口还在（没被 Alt+F4 销毁）就直接拉到前台
		if !activateExistingWindow() {
			messageBox("GuatNet", "GuatNet 已经在运行，请查看系统托盘。")
		}
		return false
	}

	singleInstanceMutex = windows.Handle(h)
	return true
}

// activateExistingWindow 按标题找到已运行实例的窗口并恢复显示。
func activateExistingWindow() bool {
	title, _ := windows.UTF16PtrFromString("GuatNet")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if h == 0 {
		return false
	}
	procShowWindow.Call(h, swRestore)
	procSetForegroundWindow.Call(h)
	return true
}
