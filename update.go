package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// latestReleaseAPI 用 var 是为了本地验证时可以临时指向 mock。
var latestReleaseAPI = "https://api.github.com/repos/wmej/GuatNet/releases/latest"

const repoReleasesURL = "https://github.com/wmej/GuatNet/releases/latest"

var updateHTTP = &http.Client{Timeout: 8 * time.Second}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// fetchLatestRelease 问 GitHub 要最新的正式版（不含草稿和预发布）。
func fetchLatestRelease() (releaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	// GitHub API 要求带 User-Agent，否则直接 403
	req.Header.Set("User-Agent", "GuatNet/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTP.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return releaseInfo{}, err
	}

	var rel releaseInfo
	if err := json.Unmarshal(body, &rel); err != nil {
		return releaseInfo{}, err
	}
	return rel, nil
}

// parseVersion 把 v1.2.3 / 1.2.3 拆成数字段；带 -beta 这类后缀的只看主段。
func parseVersion(s string) ([]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return nil, false
	}

	parts := strings.Split(s, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}

// isNewerVersion 逐段按数字比较，缺位按 0 处理：1.0.10 > 1.0.9。
// 两边有任何一边解析不了就当作没有新版本，避免异常 tag 误弹提示。
func isNewerVersion(current, latest string) bool {
	cur, ok1 := parseVersion(current)
	lat, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}

	n := len(cur)
	if len(lat) > n {
		n = len(lat)
	}
	for i := 0; i < n; i++ {
		var a, b int
		if i < len(cur) {
			a = cur[i]
		}
		if i < len(lat) {
			b = lat[i]
		}
		if b != a {
			return b > a
		}
	}
	return false
}

// describeUpdateError 给界面一句能看懂的原因。
func describeUpdateError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "403") || strings.Contains(msg, "429"):
		return "GitHub 接口限流或拒绝访问，请稍后再试"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "连接 GitHub 超时（校园网可能无法访问）"
	default:
		return "无法连接 GitHub（校园网可能无法访问）"
	}
}

// CheckForUpdate 检查最新 Release。
// manual 为 true 时，失败原因会显示在界面上；启动检查失败则保持静默。
func (a *App) CheckForUpdate(manual bool) {
	if !a.st.beginUpdateCheck() {
		return
	}

	go func() {
		rel, err := fetchLatestRelease()
		if err != nil {
			a.st.finishUpdateCheck("", "", false, describeUpdateError(err), manual)
			a.log.Info("检查更新失败：%s", maskSecrets(err.Error()))
			return
		}
		if rel.TagName == "" {
			a.st.finishUpdateCheck("", "", false, "GitHub 返回内容异常", manual)
			a.log.Warn("检查更新失败：返回内容缺少 tag_name")
			return
		}

		latest := strings.TrimPrefix(strings.TrimPrefix(rel.TagName, "v"), "V")
		url := repoReleasesURL
		if strings.HasPrefix(rel.HTMLURL, "https://github.com/") {
			url = rel.HTMLURL
		}

		newer := isNewerVersion(appVersion, rel.TagName)
		a.st.finishUpdateCheck(latest, url, newer, "", manual)
		if newer {
			a.log.Info("发现新版本 %s（当前 %s）", latest, appVersion)
		} else {
			a.log.Info("已是最新版本 %s", appVersion)
		}
	}()
}

// scheduleStartupUpdateCheck 启动后查一次，不轮询、不重试。
func (a *App) scheduleStartupUpdateCheck() {
	time.Sleep(30 * time.Second)
	a.CheckForUpdate(false)
}
