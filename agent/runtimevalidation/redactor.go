// redactor.go 在 Agent/MCP/fixture 输出进入任何持久 sink 前执行流式 secret 替换。
//
// 职责：
//   - 支持 secret 跨 Write chunk 仍被完整识别
//   - 为 stderr、HTTP evidence 和 fixture logs 提供统一 ingestion-time writer
//   - 记录脱敏次数但不记录 secret、原文或匹配位置
//
// 边界：
//   - 不发现 secret；调用方必须在首个 Write 前显式注册
//   - 不处理 MCP stdout，协议 stdout 必须只在内存 framing
package runtimevalidation

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/xsxdot/gokit/logger"
)

const redactionMarker = "[REDACTED]"

// RedactingWriter 在写入底层 sink 前跨 chunk 替换已注册 secret。
type RedactingWriter struct {
	mu          sync.Mutex
	destination io.Writer
	secrets     []string
	maxLength   int
	pending     []byte
	started     bool
	closed      bool
	redactions  int64
}

// NewRedactingWriter 创建一个尚未注册 secret 的 ingestion-time writer。
//
// 参数：
//   - destination: 接收脱敏后字节的持久或内存 sink
//
// 返回：
//   - 可在首个 Write 前注册多个 secret 的 writer
//
// 注意：nil destination 会被视为 io.Discard；协议 stdout 不应传入此处。
func NewRedactingWriter(destination io.Writer) *RedactingWriter {
	if destination == nil {
		destination = io.Discard
	}
	return &RedactingWriter{destination: destination}
}

// RegisterSecret 在首个字节写入前登记一个必须替换的非空 secret。
//
// 参数：
//   - secret: 内存中已获得的 credential、token 或 cookie 明文
//
// 返回：
//   - 空值、重复生命周期或写入开始后注册时的错误
//
// 注意：writer 不记录 secret；按长度降序匹配，避免短 secret 截断长 secret。
func (w *RedactingWriter) RegisterSecret(secret string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if secret == "" {
		return fmt.Errorf("redaction secret cannot be empty")
	}
	for _, existing := range w.secrets {
		if existing == secret {
			return nil
		}
	}
	if w.closed || w.started {
		return fmt.Errorf("secrets must be registered before the first write")
	}
	w.secrets = append(w.secrets, secret)
	sort.Slice(w.secrets, func(i, j int) bool { return len(w.secrets[i]) > len(w.secrets[j]) })
	if len(secret) > w.maxLength {
		w.maxLength = len(secret)
	}
	return nil
}

// Write 接收原始输出，并只把确定不可能组成跨 chunk secret 的前缀写入底层 sink。
func (w *RedactingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, fmt.Errorf("redacting writer is closed")
	}
	w.started = true
	w.pending = append(w.pending, data...)
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Close 刷新最后的受保护后缀并关闭 writer 生命周期。
//
// 返回：
//   - 底层 sink 最后一次写入失败时的错误
//
// 注意：不会关闭调用方提供的 destination；其生命周期仍由调用方拥有。
func (w *RedactingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	err := w.flush(true)
	fields := map[string]any{"redaction_count": w.redactions, "registered_secret_count": len(w.secrets)}
	if err != nil {
		logger.GetLogger().WithEntryName("RuntimeValidationRedactor").WithErr(err).WithFields(fields).Error("runtime validation 脱敏 sink 刷新失败")
		return err
	}
	logger.GetLogger().WithEntryName("RuntimeValidationRedactor").WithFields(fields).Info("runtime validation 脱敏 sink 已关闭")
	return nil
}

// RedactionCount 返回目前已完成的 secret 替换次数。
func (w *RedactingWriter) RedactionCount() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.redactions
}

func (w *RedactingWriter) flush(final bool) error {
	if len(w.secrets) == 0 {
		return w.writePrefix(len(w.pending))
	}
	for len(w.pending) > 0 {
		if !final && len(w.pending) < w.maxLength {
			return nil
		}
		matched := ""
		for _, secret := range w.secrets {
			if len(w.pending) >= len(secret) && strings.HasPrefix(string(w.pending), secret) {
				matched = secret
				break
			}
		}
		if matched != "" {
			if _, err := io.WriteString(w.destination, redactionMarker); err != nil {
				return err
			}
			w.pending = w.pending[len(matched):]
			w.redactions++
			continue
		}
		if err := w.writePrefix(1); err != nil {
			return err
		}
	}
	return nil
}

func (w *RedactingWriter) writePrefix(length int) error {
	if length <= 0 {
		return nil
	}
	if _, err := w.destination.Write(w.pending[:length]); err != nil {
		return err
	}
	w.pending = w.pending[length:]
	return nil
}
