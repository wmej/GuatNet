package main

import (
	"sync"
	"time"
)

type Status string

const (
	StatusOffline Status = "offline"
	StatusAuthing Status = "authing"
	StatusOnline  Status = "online"
)

const (
	maxLogLines = 200

	// 开机自启状态要查注册表，界面每秒拉一次快照，缓存几秒足够。
	autoStartTTL = 5 * time.Second
)

// AppState 是守护线程和界面线程之间唯一共享的状态。
// 所有字段都在锁内读写；界面只拿快照。
type AppState struct {
	mu          sync.Mutex
	status      Status
	onlineSince time.Time
	reconnects  int
	lastError   string
	logs        []string

	autoStart   bool
	autoStartAt time.Time
}

func newAppState() *AppState {
	return &AppState{status: StatusOffline, logs: make([]string, 0, maxLogLines)}
}

func (s *AppState) setStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st == StatusOnline && s.status != StatusOnline {
		s.onlineSince = time.Now()
		s.lastError = ""
	}
	if st != StatusOnline {
		s.onlineSince = time.Time{}
	}
	s.status = st
}

func (s *AppState) setError(msg string) {
	s.mu.Lock()
	s.lastError = msg
	s.mu.Unlock()
}

func (s *AppState) addReconnect() {
	s.mu.Lock()
	s.reconnects++
	s.mu.Unlock()
}

// autoStartCached 最多每 autoStartTTL 查一次注册表，避免界面轮询把它变成 1 Hz 查询。
func (s *AppState) autoStartCached() bool {
	s.mu.Lock()
	if !s.autoStartAt.IsZero() && time.Since(s.autoStartAt) < autoStartTTL {
		v := s.autoStart
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	v := autoStartEnabled() // 注册表调用放在锁外

	s.mu.Lock()
	s.autoStart = v
	s.autoStartAt = time.Now()
	s.mu.Unlock()
	return v
}

// setAutoStartCache 在界面刚改过开机自启后立刻同步，别让界面看到旧值。
func (s *AppState) setAutoStartCache(on bool) {
	s.mu.Lock()
	s.autoStart = on
	s.autoStartAt = time.Now()
	s.mu.Unlock()
}

func (s *AppState) addLog(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logs) >= maxLogLines {
		copy(s.logs, s.logs[len(s.logs)-maxLogLines+1:])
		s.logs = s.logs[:maxLogLines-1]
	}
	s.logs = append(s.logs, line)
}

// uiState 是发给前端的结构，字段名和 index.html 里的保持一致。
type uiState struct {
	Status      string   `json:"status"`
	Account     string   `json:"account"`
	HasPassword bool     `json:"hasPassword"`
	Gateway     string   `json:"gateway"`
	ISP         int      `json:"isp"`
	UptimeSec   int64    `json:"uptimeSec"`
	Reconnects  int      `json:"reconnects"`
	LastError   string   `json:"lastError"`
	Logs        []string `json:"logs"`
	AutoStart   bool     `json:"autoStart"`
	FirstRun    bool     `json:"firstRun"`
}

func (s *AppState) snapshot(cfg Config) uiState {
	auto := s.autoStartCached()

	s.mu.Lock()
	defer s.mu.Unlock()

	var up int64
	if s.status == StatusOnline && !s.onlineSince.IsZero() {
		up = int64(time.Since(s.onlineSince).Seconds())
	}

	logs := make([]string, len(s.logs))
	copy(logs, s.logs)

	return uiState{
		Status:      string(s.status),
		Account:     cfg.Account,
		HasPassword: cfg.Password != "",
		Gateway:     cfg.Gateway,
		ISP:         cfg.ISP,
		UptimeSec:   up,
		Reconnects:  s.reconnects,
		LastError:   s.lastError,
		Logs:        logs,
		AutoStart:   auto,
		FirstRun:    !cfg.FirstRunDone,
	}
}
