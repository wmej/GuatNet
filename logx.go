package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// logger 把日志同时写进文件和界面的环形缓冲。
// 约定：调用方传进来的字符串必须已经脱敏，logger 不再做二次清洗。
type logger struct {
	mu   sync.Mutex
	file *os.File
	st   *AppState
}

func newLogger(path string, st *AppState) (*logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &logger{file: f, st: st}, nil
}

func (l *logger) Close() error {
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *logger) logf(tag, format string, args ...any) {
	now := time.Now()
	msg := fmt.Sprintf(format, args...)

	l.mu.Lock()
	if l.file != nil {
		fmt.Fprintf(l.file, "%s [%s] %s\n", now.Format("2006-01-02 15:04:05"), tag, msg)
	}
	l.mu.Unlock()

	if l.st != nil {
		l.st.addLog(fmt.Sprintf("%s [%s] %s", now.Format("15:04:05"), tag, msg))
	}
}

func (l *logger) Info(format string, args ...any)  { l.logf("INFO", format, args...) }
func (l *logger) Warn(format string, args ...any)  { l.logf("WARN", format, args...) }
func (l *logger) Error(format string, args ...any) { l.logf("ERR", format, args...) }
