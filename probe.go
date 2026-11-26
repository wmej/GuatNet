package main

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// campusState 是"和校园网的关系"，比"能不能上网"更准确。
//
// 之前用 generate_204 判断，连手机热点也会返回 204 → 误判成"已连接"。
// 现在直接问认证门户：它认识你（有会话）才算已认证。
type campusState int

const (
	campusUnreachable campusState = iota // 门户都连不上 → 不在校园网
	campusLoggedOut                      // 门户在，但没登录
	campusLoggedIn                       // 门户在，且已有会话
)

type probeResult struct {
	state  campusState
	ip     string // 门户看到的本机 IP
	detail string // 给人看的原因
}

type prober struct {
	gw   string
	http *http.Client
}

func newProber(cfg Config) *prober {
	return &prober{
		gw: cfg.Gateway,
		http: &http.Client{
			Timeout: time.Duration(cfg.ProbeTimeoutSec) * time.Second,
		},
	}
}

var (
	reUID   = regexp.MustCompile(`uid\s*=\s*'([^']*)'`)
	reV4    = regexp.MustCompile(`v4ip\s*=\s*'([0-9]{1,3}(?:\.[0-9]{1,3}){3})'`)
	reTitle = regexp.MustCompile(`<title>([^<]*)</title>`)
)

// check 问一次门户：我现在是什么状态。
func (p *prober) check() probeResult {
	// 加个时间戳，避免被缓存成旧页面
	url := fmt.Sprintf("http://%s/?_=%d", p.gw, time.Now().UnixNano())
	resp, err := p.http.Get(url)
	if err != nil {
		return probeResult{
			state:  campusUnreachable,
			detail: "连不上认证门户 " + p.gw + "，可能不在校园网",
		}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	page := string(body) // 页面是 GBK，但下面要匹配的都是 ASCII 片段

	r := probeResult{state: campusLoggedOut}

	if m := reV4.FindStringSubmatch(page); len(m) == 2 {
		r.ip = m[1]
	}

	// 门户用 uid 表示当前会话：有值说明已登录
	uid := ""
	if m := reUID.FindStringSubmatch(page); len(m) == 2 {
		uid = m[1]
	}

	switch {
	case uid != "":
		r.state = campusLoggedIn
		r.detail = "已认证（账号 " + uid + "）"
	case isLogoutPage(page):
		// 有些版本不返回 uid，但页面是注销页，说明也是在线
		r.state = campusLoggedIn
		r.detail = "已认证"
	default:
		r.state = campusLoggedOut
		r.detail = "在校园网，但还没有认证"
	}
	return r
}

func isLogoutPage(page string) bool {
	if m := reTitle.FindStringSubmatch(page); len(m) == 2 {
		return m[1] == "注销页" || m[1] == "在线状态页"
	}
	return false
}

// online 保留给旧调用方：只有"已认证"才算在线。
func (p *prober) online() bool {
	return p.check().state == campusLoggedIn
}
