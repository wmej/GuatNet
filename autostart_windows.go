//go:build windows

package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "guatnet"
)

func currentExe() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// autoStartEnabled 判断注册表里的启动项是不是指向当前这个 exe。
// 换了安装位置（比如从绿色版换成安装版）就算没启用。
func autoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	v, _, err := k.GetStringValue(runValue)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.Trim(v, `"`), currentExe())
}

// setAutoStart 写入或删除 HKCU 的 Run 项（只影响当前用户，不需要管理员）。
func setAutoStart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if on {
		return k.SetStringValue(runValue, `"`+currentExe()+`"`)
	}

	if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}
