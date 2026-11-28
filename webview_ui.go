//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jchv/go-webview2"
)

//go:embed ui/index.html
var indexHTML string

//go:embed assets/icon.ico
var trayIcon []byte

var (
	uiMu   sync.Mutex
	uiHwnd uintptr
	uiView webview2.WebView
	uiDone = make(chan struct{})
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
	defer close(uiDone)

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
	uiView = w
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
	// 密码只在打开设置时按需取，不进每秒轮询的 state 快照
	_ = w.Bind("password", func() (string, error) {
		return a.Config().Password, nil
	})
	_ = w.Bind("checkUpdate", func() (string, error) {
		a.CheckForUpdate(true)
		return "", nil
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
		// 回调跑在界面线程上，quitApp 要等 Run 返回，必须换到别的线程
		go a.quitApp()
		return "", nil
	})

	w.SetHtml(strings.ReplaceAll(indexHTML, "{{VERSION}}", appVersion))
	w.Run()

	uiMu.Lock()
	uiView = nil
	uiMu.Unlock()
}

// shutdownUI 让界面线程销毁 WebView 并等它退出，最多等 timeout。
// 这样退出进程前先正常拆掉窗口和 WebView2，而不是直接 os.Exit。
func shutdownUI(timeout time.Duration) {
	uiMu.Lock()
	w := uiView
	uiMu.Unlock()
	if w == nil {
		return
	}

	w.Destroy() // 只是投递 WM_CLOSE，真正的销毁在界面线程完成

	select {
	case <-uiDone:
	case <-time.After(timeout):
	}
}
