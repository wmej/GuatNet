//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"runtime"
	"sync"

	"github.com/jchv/go-webview2"
)

//go:embed ui/index.html
var indexHTML string

//go:embed assets/icon.ico
var trayIcon []byte

var (
	uiMu   sync.Mutex
	uiHwnd uintptr
)

func showMainWindow() {
	uiMu.Lock()
	h := uiHwnd
	uiMu.Unlock()
	if h != 0 {
		showWindow(h)
	}
}

func hideMainWindow() {
	uiMu.Lock()
	h := uiHwnd
	uiMu.Unlock()
	if h != 0 {
		hideWindow(h)
	}
}

// runUI 在独立线程上创建窗口并跑消息循环。
// 窗口、消息循环必须同线程，所以这里锁线程。
func runUI(a *App) {
	runtime.LockOSThread()

	// 配置里存的是逻辑尺寸，CreateWindowEx 要物理像素，按系统 DPI 换算
	cfg := a.Config()
	scale := systemDPI() / 96.0
	winW := float64(cfg.WindowW) * scale
	winH := float64(cfg.WindowH) * scale

	// 别超出屏幕
	if sw, sh := screenSize(); sw > 0 && sh > 0 {
		if maxW := float64(sw) * 0.94; winW > maxW {
			winW = maxW
		}
		if maxH := float64(sh) * 0.92; winH > maxH {
			winH = maxH
		}
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "GuatNet",
			Width:  uint(winW),
			Height: uint(winH),
			Center: true,
		},
	})
	if w == nil {
		messageBox("GuatNet", "无法初始化 WebView2。\n请先安装 Microsoft Edge WebView2 Runtime 后重试。")
		return
	}

	hwnd := uintptr(w.Window())
	uiMu.Lock()
	uiHwnd = hwnd
	uiMu.Unlock()

	makeBorderless(hwnd)
	setWindowIcon(hwnd)

	// 前端通过 window.state() 等全局函数调用这里
	_ = w.Bind("state", func() (string, error) {
		b, err := json.Marshal(a.st.snapshot(a.Config()))
		return string(b), err
	})
	_ = w.Bind("reconnect", func() (string, error) {
		a.Force()
		return "", nil
	})
	_ = w.Bind("save", func(payload string) (string, error) {
		return a.saveFromUI(payload), nil
	})
	_ = w.Bind("drag", func() (string, error) {
		dragWindow(hwnd)
		return "", nil
	})
	_ = w.Bind("openURL", func(url string) (string, error) {
		openExternal(url)
		return "", nil
	})
	_ = w.Bind("firstRun", func() (string, error) {
		return a.applyFirstRun(), nil
	})
	_ = w.Bind("minimize", func() (string, error) {
		minimizeWindow(hwnd)
		return "", nil
	})
	_ = w.Bind("hide", func() (string, error) {
		hideWindow(hwnd)
		return "", nil
	})
	_ = w.Bind("quit", func() (string, error) {
		a.quitApp()
		return "", nil
	})

	w.SetHtml(indexHTML)
	w.Run()
}
