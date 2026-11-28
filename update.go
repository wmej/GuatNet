package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// latestReleaseAPI 用 var 是为了本地验证时可以临时指向 mock。
var latestReleaseAPI = "https://api.github.com/repos/wmej/GuatNet/releases/latest"

var (
	updateHTTP   = &http.Client{Timeout: 8 * time.Second}
	downloadHTTP = &http.Client{Timeout: 5 * time.Minute}
)

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
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
		newer := isNewerVersion(appVersion, rel.TagName)
		_, downloadURL := pickSetupAsset(rel)
		if newer && downloadURL == "" {
			a.log.Warn("新版本 %s 没有找到安装包资产，无法应用内更新", latest)
		}
		a.st.finishUpdateCheck(latest, downloadURL, newer, "", manual)
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

// pickSetupAsset 从 Release 资产里挑出安装包。
func pickSetupAsset(rel releaseInfo) (name, downloadURL string) {
	for _, a := range rel.Assets {
		if !strings.HasSuffix(strings.ToLower(a.Name), "setup.exe") {
			continue
		}
		if !isAllowedDownloadURL(a.BrowserDownloadURL) {
			continue
		}
		return a.Name, a.BrowserDownloadURL
	}
	return "", ""
}

func isAllowedDownloadURL(u string) bool {
	return strings.HasPrefix(u, "https://github.com/") ||
		strings.HasPrefix(u, "https://objects.githubusercontent.com/") ||
		strings.HasPrefix(u, "https://release-assets.githubusercontent.com/")
}

type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	last  int
	st    *AppState
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.read += int64(n)
		if p.total > 0 {
			pct := int(p.read * 100 / p.total)
			if pct >= p.last+2 || pct == 100 {
				p.last = pct
				p.st.setUpdateProgress(pct)
			}
		}
	}
	return n, err
}

// downloadToTemp 把安装包下到临时目录，返回本地路径。
func downloadToTemp(rawURL string, st *AppState) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GuatNet/"+appVersion)

	resp, err := downloadHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}

	name := "GuatNet-update-setup.exe"
	if u, err := url.Parse(rawURL); err == nil {
		if base := filepath.Base(u.Path); strings.HasSuffix(strings.ToLower(base), ".exe") {
			name = base
		}
	}
	path := filepath.Join(os.TempDir(), name)

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, last: -2, st: st}
	written, copyErr := io.Copy(f, pr)
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(path)
		return "", copyErr
	}
	if written < 1<<20 {
		_ = os.Remove(path)
		return "", fmt.Errorf("下载文件过小（%d 字节）", written)
	}
	return path, nil
}

func describeDownloadError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "403") || strings.Contains(msg, "404"):
		return "下载安装包失败：GitHub 拒绝访问"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "下载安装包超时（校园网可能无法访问 GitHub）"
	default:
		return "下载安装包失败（校园网可能无法访问 GitHub）"
	}
}

// StartUpdateDownload 下载安装包；成功后启动安装程序并退出本程序。
func (a *App) StartUpdateDownload() {
	if !a.st.beginUpdateDownload() {
		return
	}

	go func() {
		rawURL := a.st.updateDownloadURL()
		if rawURL == "" {
			a.st.finishUpdateDownload("没有可用的安装包，请前往 Releases 手动下载")
			return
		}
		if !isAllowedDownloadURL(rawURL) {
			a.st.finishUpdateDownload("安装包地址异常")
			return
		}

		path, err := downloadToTemp(rawURL, a.st)
		if err != nil {
			a.st.finishUpdateDownload(describeDownloadError(err))
			a.log.Warn("下载更新失败：%s", maskSecrets(err.Error()))
			return
		}

		if err := exec.Command(path).Start(); err != nil {
			_ = os.Remove(path)
			a.st.finishUpdateDownload("启动安装程序失败")
			a.log.Warn("启动安装程序失败：%s", maskSecrets(err.Error()))
			return
		}

		a.st.finishUpdateDownload("")
		a.log.Info("更新已下载，正在启动安装程序")
		// 先退出，安装包才能覆盖正在运行的 exe
		a.quitApp()
	}()
}
