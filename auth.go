package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DrcomClient 对接 Dr.COM eportal（v3 接口）。
//
// 桂林航天工业学院的门户在 http://10.1.2.3:801，
// 登录走 GET /eportal/portal/login，返回 JSONP：dr1003({"result":..,"msg":".."})
type DrcomClient struct {
	cfg  Config
	http *http.Client
}

func newDrcomClient(cfg Config) *DrcomClient {
	return &DrcomClient{
		cfg: cfg,
		http: &http.Client{
			Timeout: time.Duration(cfg.LoginTimeoutSec) * time.Second,
		},
	}
}

// ispSuffix 对应门户里运营商下拉的账号后缀（页面内 carrier 配置）。
func ispSuffix(isp int) string {
	switch isp {
	case 1:
		return "@dx" // 校园电信
	case 2:
		return "@lt" // 校园联通
	default:
		return "" // 校园用户 / 其他
	}
}

// clientIP 取门户眼中的本机 IP。
//
// 门户首页会内联 v4ip='x.x.x.x'，这是最准的来源；
// 拿不到就退回到出口网卡的地址。
func (c *DrcomClient) clientIP() string {
	if resp, err := c.http.Get(fmt.Sprintf("http://%s/", c.cfg.Gateway)); err == nil {
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)); err == nil {
			resp.Body.Close()
			if m := reV4IP.FindSubmatch(body); len(m) == 2 {
				ip := strings.TrimSpace(string(m[1]))
				if net.ParseIP(ip) != nil {
					return ip
				}
			}
		} else {
			resp.Body.Close()
		}
	}

	// 退路：本地发起一次连接，看出口地址
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:80", c.cfg.Gateway), 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

var reV4IP = regexp.MustCompile(`v4ip\s*=\s*'([0-9]{1,3}(?:\.[0-9]{1,3}){3})'`)

// buildURL 拼登录地址。返回值含明文密码，只能用于发请求，不能进日志。
func (c *DrcomClient) buildURL(ip string) string {
	return fmt.Sprintf(
		"http://%s:%d/eportal/portal/login?callback=dr1003&login_method=0"+
			"&user_account=%s&user_password=%s&wlan_user_ip=%s&wlan_user_ipv6="+
			"&wlan_user_mac=000000000000&wlan_ac_ip=&wlan_ac_name="+
			"&jsVersion=4.X&terminal_type=1&lang=zh-cn",
		c.cfg.Gateway, c.cfg.PortalPort,
		urlQueryEscape(c.cfg.Account+ispSuffix(c.cfg.ISP)),
		urlQueryEscape(c.cfg.Password),
		urlQueryEscape(ip),
	)
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		" ", "%20", "+", "%2B", "&", "%26", "=", "%3D", "#", "%23", "?", "%3F",
		"%", "%25", "/", "%2F", ":", "%3A", "@", "%40", "\"", "%22", "'", "%27",
	)
	return r.Replace(s)
}

// maskedURL 只用于日志：把密码换成 ***。
func (c *DrcomClient) maskedURL(ip string) string {
	u := c.buildURL(ip)
	if i := strings.Index(u, "user_password="); i >= 0 {
		j := strings.Index(u[i:], "&")
		if j < 0 {
			return u[:i] + "user_password=***"
		}
		return u[:i] + "user_password=***" + u[i+j:]
	}
	return u
}

type loginResult struct {
	OK   bool
	Code string
	Msg  string
}

var (
	reResult = regexp.MustCompile(`"result"\s*:\s*"?(-?\d+)"?`)
	reMSG    = regexp.MustCompile(`"msg"\s*:\s*"([^"]*)"`)
)

// Login 用给定的客户端 IP 发一次认证请求。
func (c *DrcomClient) Login(ip string) (loginResult, error) {
	resp, err := c.http.Get(c.buildURL(ip))
	if err != nil {
		return loginResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return loginResult{}, err
	}
	raw := strings.TrimSpace(string(body))

	r := loginResult{}
	if m := reResult.FindStringSubmatch(raw); len(m) == 2 {
		r.Code = m[1]
	}
	if m := reMSG.FindStringSubmatch(raw); len(m) == 2 {
		r.Msg = m[1]
	}

	// 这套接口 result=1 才是成功；另外兼容"已在线"这类结果
	switch {
	case r.Code == "1":
		r.OK = true
	case strings.Contains(r.Msg, "成功") && !strings.Contains(r.Msg, "失败"):
		r.OK = true
	case strings.Contains(r.Msg, "已在线") || strings.Contains(r.Msg, "已经在线"):
		r.OK = true
	}

	if r.Msg == "" {
		r.Msg = summarize(raw)
	}
	return r, nil
}

// summarize 截断响应，避免把整页 HTML 灌进日志。
func summarize(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	if s == "" {
		return "(空响应)"
	}
	return s
}
