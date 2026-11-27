package main

import (
	"fmt"
	"io"
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

// buildURL 拼登录地址（经典 Dr.COM 接口，和学校登录页用的完全一致）。
//
// 运营商走 R3：0 校园网 / 1 中国电信 / 2 中国联通 / 3 中国移动 / 4 中国广电。
// 参数顺序有讲究：DDDDD、upass 必须在 0MKKey 之前，否则服务端返回 203。
//
// 返回值含明文密码，只能用于发请求，绝不能进日志。
func (c *DrcomClient) buildURL() string {
	return fmt.Sprintf(
		"http://%s/drcom/login?callback=dr1003&DDDDD=%s&upass=%s"+
			"&0MKKey=123456&R1=0&R2=&R3=%d&R6=0&para=00&v6ip="+
			"&terminal_type=1&lang=zh-cn&jsVersion=4.2.1&v=%d&lang=zh",
		c.cfg.Gateway,
		urlQueryEscape(c.cfg.Account),
		urlQueryEscape(c.cfg.Password),
		c.cfg.ISP,
		time.Now().Unix()%100000,
	)
}

// maskedURL 只用于日志：把密码换成 ***。
func (c *DrcomClient) maskedURL() string {
	u := c.buildURL()
	if i := strings.Index(u, "upass="); i >= 0 {
		j := strings.Index(u[i:], "&")
		if j < 0 {
			return u[:i] + "upass=***"
		}
		return u[:i] + "upass=***" + u[i+j:]
	}
	return u
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		" ", "%20", "+", "%2B", "&", "%26", "=", "%3D", "#", "%23", "?", "%3F",
		"%", "%25", "/", "%2F", ":", "%3A", "@", "%40", "\"", "%22", "'", "%27",
	)
	return r.Replace(s)
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

// Login 发一次认证请求。
func (c *DrcomClient) Login() (loginResult, error) {
	resp, err := c.http.Get(c.buildURL())
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
