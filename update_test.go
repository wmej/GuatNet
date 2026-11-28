package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"1.0.9", "v1.0.10", true},
		{"1.0.10", "v1.0.9", false},
		{"1.0.5", "v1.0.5", false},
		{"1.0", "v1.0.0", false},
		{"1.0.6", "v1.0.6.1", true},
		{"1.0.6", "1.0.6-beta", false},
		{"", "v1.0.6", false},
		{"1.0.6", "", false},
		{"1.0.6", "abc", false},
	}

	for _, c := range cases {
		if got := isNewerVersion(c.cur, c.latest); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestFetchLatestReleaseMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("请求缺少 User-Agent，GitHub 会拒绝")
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"https://github.com/wmej/GuatNet/releases/tag/v9.9.9"}`))
	}))
	defer srv.Close()

	old := latestReleaseAPI
	latestReleaseAPI = srv.URL
	defer func() { latestReleaseAPI = old }()

	rel, err := fetchLatestRelease()
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v9.9.9" || !strings.HasPrefix(rel.HTMLURL, "https://github.com/") {
		t.Fatalf("解析结果异常：%+v", rel)
	}
	if !isNewerVersion("1.0.6", rel.TagName) {
		t.Fatal("v9.9.9 应判定为新版本")
	}
}

func TestFetchLatestRelease403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	old := latestReleaseAPI
	latestReleaseAPI = srv.URL
	defer func() { latestReleaseAPI = old }()

	if _, err := fetchLatestRelease(); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("应返回 403 错误，实际：%v", err)
	}
}

func TestMaskSecrets(t *testing.T) {
	in := "http://user:secret@proxy:8080/path?DDDDD=202400000000&upass=abc123&0MKKey=123456"
	out := maskSecrets(in)

	for _, bad := range []string{"secret", "202400000000", "abc123"} {
		if strings.Contains(out, bad) {
			t.Fatalf("maskSecrets 泄漏了 %q：%s", bad, out)
		}
	}
}

func TestRedact(t *testing.T) {
	c := &DrcomClient{cfg: Config{Account: "202400000000", Password: "p@ss&word"}}
	// 模拟 *url.Error：完整请求 URL 里带明文和 URL 编码后的密码
	in := `Get "http://10.1.2.3/drcom/login?DDDDD=202400000000&upass=p%40ss%26word&0MKKey=123456": dial tcp 10.1.2.3:80: connection refused`
	out := c.redact(in)

	for _, bad := range []string{"p@ss&word", "p%40ss%26word", "202400000000"} {
		if strings.Contains(out, bad) {
			t.Fatalf("redact 泄漏了 %q：%s", bad, out)
		}
	}
}
