package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// 单个日志文件上限：超过就轮转成 .1，只保留一份旧文件。
const maxLogBytes = 2 << 20

// logger 把日志同时写进文件和界面的环形缓冲。
// 约定：调用方传进来的字符串必须已经脱敏，logger 不再做二次清洗。
type logger struct {
	mu   sync.Mutex
	file *os.File
	path string
	size int64
	st   *AppState
}

func newLogger(path string, st *AppState) (*logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	var size int64
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	return &logger{file: f, path: path, size: size, st: st}, nil
}

func (l *logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	return f.Close()
}

func (l *logger) logf(tag, format string, args ...any) {
	now := time.Now()
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s [%s] %s\n", now.Format("2006-01-02 15:04:05"), tag, msg)

	l.mu.Lock()
	if l.file != nil {
		if n, err := l.file.WriteString(line); err == nil {
			l.size += int64(n)
			if l.size > maxLogBytes {
				l.rotateLocked()
			}
		}
	}
	l.mu.Unlock()

	if l.st != nil {
		l.st.addLog(fmt.Sprintf("%s [%s] %s", now.Format("15:04:05"), tag, msg))
	}
}

// rotateLocked 关闭当前文件、留作 .1，再开一个新的空文件。调用方需持有 l.mu。
func (l *logger) rotateLocked() {
	_ = l.file.Close()
	l.file = nil
	l.size = 0
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
	if f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		l.file = f
	}
}

func (l *logger) Info(format string, args ...any)  { l.logf("INFO", format, args...) }
func (l *logger) Warn(format string, args ...any)  { l.logf("WARN", format, args...) }
func (l *logger) Error(format string, args ...any) { l.logf("ERR", format, args...) }
