// Package main 提供 SuperDev onboarding 示例服务。
//
// 职责：
//   - 启动一个极小 HTTP 服务，暴露 / 和 /health
//   - 持续输出结构化 stdout/stderr 日志，包含 INFO、WARN 和偶发 ERROR
//   - 作为桌面端随包分发的本地 managed deployment 目标
//
// 边界：
//   - 不依赖 SuperDev agent 或 MCP server
//   - 不访问外部网络、数据库或用户文件
//   - 不持久化状态
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func main() {
	port := flag.Int("port", 18191, "HTTP listen port")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeLog(os.Stdout, "INFO", "request", map[string]string{"path": "/", "status": "ok"})
		_, _ = w.Write([]byte("SuperDev sample is running\n"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeLog(os.Stdout, "INFO", "health", map[string]string{"status": "ok"})
		_, _ = w.Write([]byte("ok\n"))
	})

	go emitLogs()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	writeLog(os.Stdout, "INFO", "startup", map[string]string{"addr": addr})
	if err := http.ListenAndServe(addr, mux); err != nil {
		writeLog(os.Stderr, "ERROR", "server_exit", map[string]string{"error": err.Error()})
		os.Exit(1)
	}
}

func emitLogs() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	seq := 0
	for range ticker.C {
		seq++
		writeLog(os.Stdout, "INFO", "heartbeat", map[string]string{"seq": fmt.Sprintf("%d", seq)})
		if seq%5 == 0 {
			writeLog(os.Stderr, "WARN", "cache_slow", map[string]string{
				"latency_ms": "320",
				"seq":        fmt.Sprintf("%d", seq),
			})
		}
		if seq%9 == 0 {
			writeLog(os.Stderr, "ERROR", "demo_error", map[string]string{
				"message": "simulated downstream timeout",
				"seq":     fmt.Sprintf("%d", seq),
			})
		}
	}
}

func formatLogLine(level string, event string, fields map[string]string) string {
	parts := []string{
		"service=sample-api",
		"level=" + level,
		"event=" + event,
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprintf("%q", fields[key]))
	}
	return strings.Join(parts, " ")
}

func writeLog(out *os.File, level string, event string, fields map[string]string) {
	fmt.Fprintln(out, formatLogLine(level, event, fields))
}
