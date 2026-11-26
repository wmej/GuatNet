package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/energye/systray"
)

const (
	maxLoginTry = 6
	maxBackoff  = 30 * time.Second
)

// App 把配置、状态、守护循环串在一起。
type App struct {
	cfgMu sync.RWMutex
	cfg   Config

	st    *AppState
	force chan struct{}
	log   *logger
}

func (a *App) Config() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

func (a *App) setConfig(c Config) {
	a.cfgMu.Lock()
	a.cfg = c
	a.cfgMu.Unlock()
}

// Force 请求立刻做一次认证（界面按钮、托盘菜单都会调）。
func (a *App) Force() {
	select {
	case a.force <- struct{}{}:
	default:
	}
}

func main() {
	// 必须最先调用：否则高分屏上整个界面会被系统拉伸，字发虚
	enableDPIAwareness()

	cfgPath := configFilePath()
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		cfg = defaultConfig()
	}

	st := newAppState()
	a := &App{cfg: cfg, st: st, force: make(chan struct{}, 1)}

	logPath := logFilePath(cfg.LogFile)
	log, err := newLogger(logPath, st)
	if err != nil {
		messageBox("GuatNet", "无法写入日志文件：\n"+logPath)
		return
	}
	a.log = log
	defer log.Close()

	a.log.Info("GuatNet 启动｜网关 %s｜ISP %d", cfg.Gateway, cfg.ISP)
	if cfg.Account == "" {
		a.log.Warn("还没有配置账号，请在「设置」里填写")
	}

	// 守护循环和界面各占一个线程，托盘跑在主线程
	go a.daemon()
	go runUI(a)

	systray.Run(a.onTrayReady, func() {})
}

// ---------------- 守护循环 ----------------

func (a *App) daemon() {
	fails := 0
	wasOnline := false

	for {
		cfg := a.Config()

		if cfg.Account == "" || cfg.Password == "" {
			a.st.setStatus(StatusOffline)
			a.st.setError("未设置账号")
			select {
			case <-a.force:
			case <-time.After(time.Duration(cfg.ProbeEverySec) * time.Second):
			}
			continue
		}

		client := newDrcomClient(cfg)
		probe := newProber(cfg)

		pr := probe.check()
		switch pr.state {
		case campusLoggedIn:
			if !wasOnline {
				a.log.Info("已恢复联网")
			}
			wasOnline = true
			fails = 0
			a.st.setStatus(StatusOnline)
			a.st.setError("")

		case campusLoggedOut:
			// 明确知道"在校园网但没认证"，直接认证，不用等阈值
			if !wasOnline {
				a.log.Warn("检测到校园网未认证")
			}
			wasOnline = false
			fails = 0
			a.st.setStatus(StatusOffline)
			a.st.setError(pr.detail)
			a.login(client, probe, cfg, pr.ip)
			wasOnline = probe.online()
			if wasOnline {
				a.st.setStatus(StatusOnline)
				a.st.setError("")
			} else {
				a.st.setStatus(StatusOffline)
			}

		default: // campusUnreachable
			if wasOnline || fails == 0 {
				a.log.Info("%s", pr.detail)
			}
			wasOnline = false
			fails = 0
			a.st.setStatus(StatusOffline)
			a.st.setError(pr.detail)
		}

		select {
		case <-a.force:
			cur := probe.check()
			switch cur.state {
			case campusLoggedIn:
				a.log.Info("当前已认证，无需重复登录")
				a.st.setStatus(StatusOnline)
				a.st.setError("")
			case campusLoggedOut:
				a.login(client, probe, cfg, cur.ip)
				wasOnline = probe.online()
				if wasOnline {
					a.st.setStatus(StatusOnline)
					a.st.setError("")
				} else {
					a.st.setStatus(StatusOffline)
				}
			default:
				a.log.Warn("%s，跳过认证", cur.detail)
				a.st.setError(cur.detail)
			}
		case <-time.After(time.Duration(cfg.ProbeEverySec) * time.Second):
		}
	}
}

// login 带退避地认证，成功后复探确认。
func (a *App) login(client *DrcomClient, probe *prober, cfg Config, ip string) {
	backoff := 5 * time.Second

	if ip == "" {
		ip = client.clientIP()
	}
	if ip == "" {
		a.log.Warn("没取到本机在门户里的 IP，认证可能失败")
	}
	a.log.Info("开始认证（%s）：%s", ip, client.maskedURL(ip))

	for i := 1; i <= maxLoginTry; i++ {
		a.st.setStatus(StatusAuthing)

		res, err := client.Login(ip)
		switch {
		case err != nil:
			a.log.Error("第 %d 次认证请求失败：%v", i, err)
			a.st.setError("认证请求失败")

		case res.OK:
			a.log.Info("第 %d 次认证成功（result=%s）", i, res.Code)
			a.st.addReconnect()
			time.Sleep(2 * time.Second)
			if probe.online() {
				a.log.Info("复探通过，已在线")
				return
			}
			a.log.Warn("网关返回成功，但仍无法上网")
			a.st.setError("网关已接受，但仍无法上网")
			return

		default:
			a.log.Warn("第 %d 次认证被拒：result=%s msg=%s", i, res.Code, res.Msg)
			a.st.setError("认证被拒：" + res.Msg)
		}

		if i == maxLoginTry {
			break
		}
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	a.log.Error("连续 %d 次认证失败，等待下一轮探测", maxLoginTry)
}

// ---------------- 界面回调 ----------------

// saveFromUI 接收界面传来的 JSON，校验后写盘并热更新。
func (a *App) saveFromUI(payload string) string {
	var in struct {
		Account   string `json:"account"`
		Password  string `json:"password"`
		ISP       int    `json:"isp"`
		Gateway   string `json:"gateway"`
		AutoStart *bool  `json:"autoStart"`
	}
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		return "参数格式错误"
	}
	// 账号允许为空：先配好开机自启、网关这些也算有效保存，
	// 不然没填账号时连开关都拨不动。
	cfg := a.Config()
	cfg.Account = in.Account
	cfg.ISP = in.ISP
	if in.Gateway != "" {
		cfg.Gateway = in.Gateway
	}
	if in.Password != "" {
		cfg.Password = in.Password
	}
	cfg = normalize(cfg)

	if err := saveConfig(configFilePath(), cfg); err != nil {
		return "写入配置失败：" + err.Error()
	}
	if in.AutoStart != nil {
		if err := setAutoStart(*in.AutoStart); err != nil {
			return "设置开机自启失败：" + err.Error()
		}
	}

	a.setConfig(cfg)
	a.log.Info("配置已更新｜账号 %s｜网关 %s｜ISP %d", cfg.Account, cfg.Gateway, cfg.ISP)

	// 配置变了，立刻重新认证一次
	a.Force()
	return ""
}

// applyFirstRun 标记"使用须知已读"，下次启动不再弹。
// 开机自启改由安装包勾选，或用户在通用设置里手动开。
func (a *App) applyFirstRun() string {
	cfg := a.Config()
	cfg.FirstRunDone = true

	if err := saveConfig(configFilePath(), cfg); err != nil {
		return err.Error()
	}
	a.setConfig(cfg)
	a.log.Info("已确认使用须知")
	return ""
}

// ---------------- 托盘 ----------------

func (a *App) onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("GuatNet")
	systray.SetTooltip("GuatNet · 校园网自动认证")

	mOpen := systray.AddMenuItem("打开面板", "显示主窗口")
	mReconnect := systray.AddMenuItem("立即重连", "马上重新认证一次")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 guatnet")

	systray.SetOnClick(func(systray.IMenu) { showMainWindow() })
	systray.SetOnDClick(func(systray.IMenu) { showMainWindow() })

	mOpen.Click(func() { showMainWindow() })
	mReconnect.Click(func() { a.Force() })
	mQuit.Click(func() {
		systray.Quit()
		os.Exit(0)
	})
}
