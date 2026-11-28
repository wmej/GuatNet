package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config 是全部可调参数，来自程序同目录的 config.json。
// 账号密码放配置文件而不是命令行参数：命令行会出现在任务管理器的进程列表里。
type Config struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	ISP      int    `json:"isp"`     // 0 校园网 1 电信 2 联通 3 移动 4 广电
	Gateway  string `json:"gateway"` // 认证网关，如 10.1.2.3

	JSVersion string `json:"jsVersion"` // 部分校区会校验这个版本号

	// 已废弃：登录固定走 80 端口的经典 Dr.COM 接口，保留字段只为兼容旧配置。
	PortalPort int `json:"portalPort,omitempty"`

	ProbeURL        string `json:"probeURL"`
	ProbeEverySec   int    `json:"probeIntervalSec"`
	ProbeTimeoutSec int    `json:"probeTimeoutSec"`
	LoginTimeoutSec int    `json:"loginTimeoutSec"`
	FailThreshold   int    `json:"failThreshold"`

	LogFile string `json:"logFile"`

	// 窗口大小（逻辑像素）
	WindowW int `json:"windowWidth"`
	WindowH int `json:"windowHeight"`

	// 首次启动的开机自启询问是否已完成，避免每次都问
	FirstRunDone bool `json:"firstRunDone"`
}

func defaultConfig() Config {
	return Config{
		ISP:             0,
		Gateway:         "10.1.2.3",
		JSVersion:       "4.2.1",
		ProbeURL:        "http://connect.rom.miui.com/generate_204",
		ProbeEverySec:   20,
		ProbeTimeoutSec: 5,
		LoginTimeoutSec: 5,
		FailThreshold:   2,
		LogFile:         "guatnet.log",
		WindowW:         400,
		WindowH:         610,
	}
}

// exeDir 返回 exe 所在目录，保证双击/计划任务启动时都能找到配置文件。
func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(p)
}

// dataDir 决定配置和日志放哪：
//   - 便携模式：exe 同目录已经有 config.json，就继续用它（绿色版/开发调试）
//   - 安装模式：写到 %APPDATA%\guatnet，Program Files 是只读的
func dataDir() string {
	exeCfg := filepath.Join(exeDir(), "config.json")
	if _, err := os.Stat(exeCfg); err == nil {
		return exeDir()
	}
	base, err := os.UserConfigDir() // Windows 下是 %APPDATA%
	if err != nil {
		return exeDir()
	}
	dir := filepath.Join(base, "guatnet")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

func configFilePath() string { return filepath.Join(dataDir(), "config.json") }

// logFilePath 把相对路径的日志名落到 dataDir 下。
func logFilePath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dataDir(), name)
}

// loadConfig 读配置；文件不存在时返回默认值（界面里可以再去填）。
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil // 第一次运行，交给界面引导填写
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("配置文件格式错误: %w", err)
	}
	return normalize(cfg), nil
}

func normalize(c Config) Config {
	d := defaultConfig()
	if c.Gateway == "" {
		c.Gateway = d.Gateway
	}
	if c.JSVersion == "" {
		c.JSVersion = d.JSVersion
	}
	if c.ProbeURL == "" {
		c.ProbeURL = d.ProbeURL
	}
	if c.ProbeEverySec <= 0 {
		c.ProbeEverySec = d.ProbeEverySec
	}
	if c.ProbeTimeoutSec <= 0 {
		c.ProbeTimeoutSec = d.ProbeTimeoutSec
	}
	if c.LoginTimeoutSec <= 0 {
		c.LoginTimeoutSec = d.LoginTimeoutSec
	}
	if c.FailThreshold <= 0 {
		c.FailThreshold = d.FailThreshold
	}
	if c.LogFile == "" {
		c.LogFile = d.LogFile
	}
	if c.WindowW < 400 {
		c.WindowW = d.WindowW
	}
	if c.WindowH < 520 {
		c.WindowH = d.WindowH
	}
	return c
}

// backupConfigFile 把损坏的配置复制一份。
// 不清空原文件：原文件还在，便携模式判定不会被破坏；
// 而下次保存会覆盖它，所以必须先留一份底。
func backupConfigFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dst := path + ".broken-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// saveConfig 原子写入，避免写到一半断电把配置写坏。
func saveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
