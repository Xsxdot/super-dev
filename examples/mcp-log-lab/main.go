// Package main 提供 MCP 运行态和日志诊断实验项目。
//
// 职责：
//   - 按 role 启动 api、worker、noisy、crasher 四类测试服务
//   - 产生包含稳定 trace/request 标记的单行日志
//   - 暴露一个轻量 HTTP API 供手工验证 api 服务
//
// 边界：
//   - 不依赖 SuperDev agent 或 MCP server
//   - 不访问外部网络、数据库或容器
//   - 不持久化任何运行态
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

const (
	targetTraceID   = "mcp-lab-target"
	targetRequestID = "req-mcp-lab-001"
	defaultAPIPort  = 18190
)

func main() {
	role, port := roleFromArgs(os.Args[1:])
	var err error
	switch role {
	case "api":
		err = runAPI(port)
	case "worker":
		err = runWorker()
	case "noisy":
		err = runNoisy()
	case "crasher":
		err = runCrasher()
	default:
		err = fmt.Errorf("unknown role %q", role)
	}
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if role == "crasher" {
		os.Exit(2)
	}
	os.Exit(1)
}

func roleFromArgs(args []string) (string, int) {
	fs := flag.NewFlagSet("mcp-log-lab", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	role := fs.String("role", "api", "service role")
	port := fs.Int("port", defaultAPIPort, "api port")
	_ = fs.Parse(args)
	return strings.TrimSpace(*role), *port
}

func formatLogLine(service, level, event string, fields map[string]string) string {
	parts := []string{
		"service=" + service,
		"level=" + strings.ToUpper(level),
		"event=" + event,
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := formatFieldValue(fields[key])
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, " ")
}

func formatFieldValue(value string) string {
	if strings.ContainsAny(value, " \t") {
		return `"` + strings.ReplaceAll(value, `"`, `'`) + `"`
	}
	return value
}

func crasherEvents() []string {
	return []string{
		formatLogLine("crasher", "INFO", "startup", map[string]string{
			"trace_id":   targetTraceID,
			"request_id": targetRequestID,
			"component":  "bootstrap",
		}),
		formatLogLine("crasher", "WARN", "dependency_unhealthy", map[string]string{
			"trace_id": targetTraceID,
			"target":   "database",
			"message":  "database connection refused",
		}),
		formatLogLine("crasher", "ERROR", "fatal_exit", map[string]string{
			"trace_id": targetTraceID,
			"message":  "retry exhausted",
			"exit":     "2",
		}),
	}
}

func runAPI(port int) error {
	writeLog(os.Stdout, "api", "INFO", "startup", map[string]string{
		"trace_id":   targetTraceID,
		"request_id": targetRequestID,
		"port":       fmt.Sprintf("%d", port),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeLog(os.Stdout, "api", "INFO", "health_check", map[string]string{
			"trace_id": targetTraceID,
			"status":   "ok",
		})
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		writeLog(os.Stdout, "api", "INFO", "request_start", map[string]string{
			"trace_id":   targetTraceID,
			"request_id": targetRequestID,
			"path":       "/work",
		})
		writeLog(os.Stderr, "api", "WARN", "slow_dependency", map[string]string{
			"trace_id":   targetTraceID,
			"request_id": targetRequestID,
			"latency_ms": "240",
		})
		_, _ = w.Write([]byte("done\n"))
	})

	go func() {
		ticker := time.NewTicker(700 * time.Millisecond)
		defer ticker.Stop()
		seq := 0
		for range ticker.C {
			seq++
			writeLog(os.Stdout, "api", "INFO", "tick", map[string]string{
				"trace_id":   targetTraceID,
				"request_id": targetRequestID,
				"seq":        fmt.Sprintf("%d", seq),
			})
			if seq%5 == 0 {
				writeLog(os.Stderr, "api", "WARN", "cache_refresh_slow", map[string]string{
					"trace_id":   targetTraceID,
					"latency_ms": "310",
				})
			}
		}
	}()

	return http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)
}

func runWorker() error {
	writeLog(os.Stdout, "worker", "INFO", "startup", map[string]string{
		"trace_id": targetTraceID,
		"queue":    "default",
	})
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()
	job := 0
	for range ticker.C {
		job++
		fields := map[string]string{
			"trace_id": targetTraceID,
			"job_id":   fmt.Sprintf("job-%03d", job),
		}
		if job%4 == 0 {
			fields["reason"] = "retry scheduled"
			writeLog(os.Stderr, "worker", "WARN", "job_retry", fields)
			continue
		}
		writeLog(os.Stdout, "worker", "INFO", "job_done", fields)
	}
	return nil
}

func runNoisy() error {
	writeLog(os.Stdout, "noisy", "INFO", "startup", map[string]string{
		"trace_id": targetTraceID,
	})
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	seq := 0
	for range ticker.C {
		seq++
		writeLog(os.Stdout, "noisy", "DEBUG", "HEARTBEAT", map[string]string{
			"seq": fmt.Sprintf("%d", seq),
		})
	}
	return nil
}

func runCrasher() error {
	for _, line := range crasherEvents() {
		if strings.Contains(line, "level=INFO") {
			fmt.Fprintln(os.Stdout, line)
			continue
		}
		fmt.Fprintln(os.Stderr, line)
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("crasher exited after deterministic failure")
}

func writeLog(out *os.File, service, level, event string, fields map[string]string) {
	fmt.Fprintln(out, formatLogLine(service, level, event, fields))
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
