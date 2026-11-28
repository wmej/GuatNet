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

	// 两次认证之间的最小间隔。校园门户对短时间内的重复登录会"强制注销"并冷却，
	// 所以宁可慢一点，也不要连着重登。
	minLoginGap = 45 * time.Second

	// 认证成功后等门户生效：实测需要十几秒，期间探测不到不代表失败
	verifyTries = 4
	verifyGap   = 4 * time.Second
)

// App 把配置、状态、守护循环串在一起。
type App struct {
	cfgMu sync.RWMutex
	cfg   Config

	st    *AppState
	force chan struct{}
	log   *logger

	lastLogin time.Time // 上次提交认证的时间，用于冷却判断
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

	// 双开会让两个守护线程互相顶掉会话，先挡掉
	if !ensureSingleInstance() {
		return
	}

	cfgPath := configFilePath()
	cfg, err := loadConfig(cfgPath)
	brokenBackup := ""
	if err != nil {
		// 解析失败时先留一份底，否则下次保存会把用户的账号彻底覆盖
		if p, berr := backupConfigFile(cfgPath); berr == nil {
			brokenBackup = p
		}
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

	if brokenBackup != "" {
		a.log.Warn("配置文件无法解析，已备份为 %s，本次使用默认配置", brokenBackup)
	}
	a.log.Info("GuatNet 启动｜网关 %s｜ISP %d", cfg.Gateway, cfg.ISP)
	if cfg.Account == "" {
		a.log.Warn("还没有配置账号，请在「设置」里填写")
	}

	// 守护循环和界面各占一个线程，托盘跑在主线程
	go a.daemon()
	go runUI(a)
	go a.scheduleStartupUpdateCheck()

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
			a.st.setError("未设置账号或密码")
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
			a.tryLogin(client, probe, cfg)
			wasOnline = probe.online()
			if wasOnline {
				a.st.setStatus(StatusOnline)
				a.st.setError("")
			} else {
				a.st.setStatus(StatusOffline)
			}

		default: // campusUnreachable
			fails++
			// 只在刚掉线时记一条：一直连不上就不再每 20 秒刷屏
			if wasOnline || fails == 1 {
				a.log.Info("%s", pr.detail)
			}
			wasOnline = false
			a.st.setStatus(StatusOffline)
			a.st.setError(pr.detail)
		}

		select {
		case <-a.force:
			// 配置可能刚在设置里保存过，重建客户端，
			// 否则会拿旧账号/旧网关去认证
			cfg = a.Config()
			client = newDrcomClient(cfg)
			probe = newProber(cfg)

			cur := probe.check()
			wait := a.cooldownRemaining()
			switch {
			case cur.state == campusUnreachable:
				a.log.Warn("%s，跳过认证", cur.detail)
				a.st.setError(cur.detail)

			case wait > 0:
				// 用户点了重连，但冷却中：如实说明还要等多久，别假装认证过
				a.log.Warn("刚提交过认证，%d 秒内不重复提交（门户会强制注销并冷却）",
					int(wait/time.Second)+1)
				a.st.setError("认证冷却中，请稍候")

			default:
				if cur.state == campusLoggedIn {
					// 用户主动点了重连，就真的重来一次（会替换掉现有会话）
					a.log.Info("按要求重新认证（当前已在线，旧会话会被替换）")
				}
				a.tryLogin(client, probe, cfg)
				wasOnline = probe.online()
				if wasOnline {
					a.st.setStatus(StatusOnline)
					a.st.setError("")
				} else {
					a.st.setStatus(StatusOffline)
				}
			}
		case <-time.After(time.Duration(cfg.ProbeEverySec) * time.Second):
		}
	}
}

// operatorName 把 R3 编号翻成给人看的名字。
func operatorName(isp int) string {
	names := []string{"校园网", "中国电信", "中国联通", "中国移动", "中国广电"}
	if isp >= 0 && isp < len(names) {
		return names[isp]
	}
	return "校园网"
}

// cooldownRemaining 返回距离允许下次提交还要等多久，0 表示可以提交。
func (a *App) cooldownRemaining() time.Duration {
	if wait := minLoginGap - time.Since(a.lastLogin); wait > 0 {
		return wait
	}
	return 0
}

// tryLogin 在冷却期外才真正提交认证，避免把门户惹毛。
func (a *App) tryLogin(client *DrcomClient, probe *prober, cfg Config) {
	if wait := a.cooldownRemaining(); wait > 0 {
		// 只在冷却刚开始时记一条，避免每 20 秒刷一遍
		if wait > minLoginGap-5*time.Second {
			a.log.Warn("刚提交过认证，%s 内不重复提交（门户会强制注销并冷却）", minLoginGap)
		}
		a.st.setError("认证冷却中，请稍候")
		return
	}
	a.lastLogin = time.Now()
	a.login(client, probe, cfg)
}

// login 带退避地认证，成功后复探确认。
func (a *App) login(client *DrcomClient, probe *prober, cfg Config) {
	backoff := 5 * time.Second

	a.log.Info("开始认证（%s）：%s", operatorName(cfg.ISP), client.maskedURL())

	for i := 1; i <= maxLoginTry; i++ {
		a.st.setStatus(StatusAuthing)

		res, err := client.Login()
		switch {
		case err != nil:
			// err 是 *url.Error，文本里含完整请求 URL，必须先脱敏
			a.log.Error("第 %d 次认证请求失败：%s", i, client.redact(err.Error()))
			a.st.setError("认证请求失败")

		case res.OK:
			a.log.Info("第 %d 次认证成功（result=%s）", i, res.Code)
			a.st.addReconnect()

			// 门户返回成功后，会话生效需要一段时间，多探几轮再下结论
			for k := 1; k <= verifyTries; k++ {
				time.Sleep(verifyGap)
				if probe.online() {
					a.log.Info("复探通过，已在线（等待 %ds）", k*int(verifyGap/time.Second))
					a.st.setError("")
					return
				}
			}
			a.log.Warn("门户已接受，但 %ds 内仍未生效，稍后再探", verifyTries*int(verifyGap/time.Second))
			a.st.setError("门户已接受，等待生效")
			return

		default:
			a.log.Warn("第 %d 次认证被拒：result=%s msg=%s", i, res.Code, client.redact(res.Msg))
			a.st.setError("认证被拒：" + client.redact(res.Msg))
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
		Account   string  `json:"account"`
		Password  *string `json:"password"`
		ISP       int     `json:"isp"`
		Gateway   string  `json:"gateway"`
		AutoStart *bool   `json:"autoStart"`
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
	// 界面会回填已保存密码：空串是用户主动清空，字段缺失才表示保持原值
	if in.Password != nil {
		cfg.Password = *in.Password
	}
	cfg = normalize(cfg)

	if err := saveConfig(configFilePath(), cfg); err != nil {
		return "写入配置失败：" + err.Error()
	}
	if in.AutoStart != nil {
		if err := setAutoStart(*in.AutoStart); err != nil {
			return "设置开机自启失败：" + err.Error()
		}
		a.st.setAutoStartCache(*in.AutoStart)
	}

	a.setConfig(cfg)
	a.log.Info("配置已更新｜网关 %s｜ISP %d", cfg.Gateway, cfg.ISP)

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

// quitApp 先拆掉 WebView2、落盘日志，再退出进程。
// os.Exit 不会执行 main 里的 defer，所以这里手动收尾。
func (a *App) quitApp() {
	shutdownUI(2 * time.Second)
	if a.log != nil {
		_ = a.log.Close()
	}
	os.Exit(0)
}

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
		a.quitApp()
	})
}
