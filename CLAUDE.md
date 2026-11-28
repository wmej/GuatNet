# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# GuatNet 项目规则

## 打包与发布

- **推送必须同时发布 Release**，一次动作整套走完：推 `main` → 打 `v<版本>` tag 并推送 → 创建 GitHub Release → 上传资产
- 发布资产固定两个：`GuatNet-v<版本>-win-x64-setup.exe` 和 `GuatNet-v<版本>-win-x64-portable.exe`（绿色版就是 `guatnet.exe` 改名）
- Release 说明沿用 v1.0.3 的六段式模板：程序简介 → `## 下载哪一个？`（安装版/绿色版对照表）→ `## 本次更新（vX）` → `## 功能` → `## 环境要求` → 非官方声明；只改版本号与更新内容
- 不要自行决定推送；用户要求推送时再执行，但发布必须跟着推送一起做完
- 改代码时只做编译验证：`go build -o /tmp/check.exe .`

## 版本号

- 每次改动都要升 patch 版本号（1.0.3 → 1.0.4 → …）
- 同步两处：`ui/index.html` 关于页的「版本 x.y.z」、`installer/guatnet.iss` 的 `#define MyAppVersion`
- 产物名由 Inno 自动生成：`GuatNet-v<版本>-win-x64-setup.exe`

## 构建

```bash
go build -ldflags="-s -w -H=windowsgui" -o guatnet.exe .
ISCC.exe installer\guatnet.iss        # 输出到 dist\
```

- ISCC 不在 PATH，实际路径：`%LOCALAPPDATA%\Programs\Inno Setup 6\ISCC.exe`
- 替换 `assets/icon.ico` 后需重生成图标资源：
  `rsrc -arch amd64 -ico assets/icon.ico -o rsrc_windows_amd64.syso`
- `ui/index.html` 和 `assets/icon.ico` 通过 `go:embed` 编进 exe，改完必须重新 `go build` 才生效
- 项目**没有测试套件**，编译通过即最低验证

## 架构

单包 Go 程序（`package main`），无框架，仅支持 Windows：

- 依赖：`energye/systray`（托盘）、`jchv/go-webview2`（界面）、`golang.org/x/sys/windows`（syscall/注册表）。运行需要系统装有 WebView2 Runtime
- 入口 `main.go`，三个并发单元：
  - 主 goroutine 跑 `systray.Run`（托盘菜单）
  - `go a.daemon()`：探测 → 认证 → 保活循环
  - `go runUI()`：`runtime.LockOSThread` 后创建 WebView2 窗口（窗口与消息循环必须同线程）
- 启动时用 `Local\GuatNet-single-instance` 命名互斥量保证单实例（`singleinstance_windows.go`），第二个实例会把已有窗口叫到前台后退出
- 前端是单文件 `ui/index.html`，无构建步骤、无 npm 依赖
- 跨线程共享状态只有两处，新增共享数据必须走锁：
  - `App.cfg`（`sync.RWMutex`，`Config()` / `setConfig()`）
  - `AppState`（`sync.Mutex`，状态、在线时长、重连次数、错误、日志环形缓冲 `maxLogLines=200`）
- 平台相关代码用 `//go:build windows` 隔离：`win32.go`（user32/dwmapi syscall）、`autostart_windows.go`、`webview_ui.go`

### 前后端通信（`webview_ui.go`）

`w.Bind(...)` 暴露给 `window.*`，前端每秒轮询 `state`：

| 绑定 | 作用 |
|---|---|
| `state` | 返回 `uiState` JSON 快照（`state.go`，字段名须与 index.html 一致） |
| `save` | 接收 JSON 保存配置，成功后立刻 `Force()` 重新认证 |
| `reconnect` | `App.Force()` |
| `drag` / `minimize` / `hide` / `quit` | 无边框窗口控制 |
| `openURL` | 仅允许 `https://github.com/` 前缀 |
| `firstRun` | 标记使用须知已读 |

托盘与界面按钮共用同一个 `Force()` 通道（带缓冲，非阻塞）。

### 认证与探测（`auth.go` / `probe.go`）

- 认证走经典 Dr.COM GET `/drcom/login`，运营商以 `R3=<isp>` 提交（0 校园网 / 1 电信 / 2 联通 / 3 移动 / 4 广电）
- **参数顺序有坑**：`DDDDD`、`upass` 必须排在 `0MKKey` 之前，否则服务端返回 203
- 响应用正则从 JSONP 取 `result`/`msg`：`result=1`、msg 含「成功」或「已在线」都算成功
- 探测**不用** `generate_204`：连手机热点也会返回 204，会被误判成已联网。改为请求网关首页，用页面里的 `uid`（或注销页标题）判断会话，得到三态：`campusUnreachable` / `campusLoggedOut` / `campusLoggedIn`
- 门户对短时间重复登录会强制注销并冷却，因此：
  - `minLoginGap=45s` 内不重复提交认证
  - 认证成功后要等门户生效（`verifyTries × verifyGap`）再下结论，期间探测失败不代表认证失败
  - 失败重试带指数退避（5s 起，封顶 `maxBackoff=30s`，`maxLoginTry=6` 次）
- 明文密码只允许出现在 `buildURL()` 发出的请求里；写日志一律用 `maskedURL()`

### 配置与存储（`config.go`）

- 存放位置二选一：
  - **便携模式**：exe 同目录已存在 `config.json` → 配置和日志都放 exe 目录（绿色版/调试）
  - **安装模式**：否则写 `%APPDATA%\guatnet\`（`Program Files` 只读）
- 保存用 `.tmp` + rename 原子写入；`normalize()` 负责补默认值
- `jsVersion` / `probeURL` / `failThreshold` / `portalPort` 已废弃，仅为兼容旧配置保留，不要再扩展或依赖；认证固定走 80 端口 `/drcom/login`
- 开机自启写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`；`autoStartEnabled()` 会把注册表值和当前 exe 路径比对，换了安装位置就视为未启用

### Windows 细节（`win32.go`）

- `enableDPIAwareness()` 必须在**任何窗口创建之前**调用，否则高分屏上界面被系统拉伸、字发虚
- 窗口去掉系统标题栏/边框，拖动靠 `dragWindow()` 发 `WM_NCLBUTTONDOWN` 伪装成按标题栏
- 尺寸换算：配置里是逻辑像素，`CreateWindowEx` 要物理像素，按 `systemDPI()/96` 乘缩放比

## 注意事项

- 账号密码只存在于 `%APPDATA%\guatnet\`，绝不写进代码、日志或提交
- 文档和截图里的账号一律用假号（如 `202400000000`）
- 仓库根目录的 `guatnet.exe`、`dist/`、`config.json`、`*.log` 都是构建产物或本地数据，已被 gitignore，不要提交
- 已知限制：Alt+F4 关窗会销毁 WebView；只适配 Dr.COM，换认证系统需改 `auth.go` 的接口与参数
