// manager.go 管理由 SuperDev 创建的本机浏览器调试 session。
//
// 职责：
//   - 调用浏览器 launcher 创建隔离调试浏览器
//   - 保存 session 与关闭函数的运行态映射
//   - 提供 list/get/close 生命周期操作
//
// 边界：
//   - 不解析项目配置或选择浏览器
//   - 不直接实现 Chromium 启动细节
package browserdebug

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// OpenResolvedRequest 是 API 层完成 deployment/browser 解析后的打开请求。
type OpenResolvedRequest struct {
	Browser      BrowserRecord
	Target       Target
	TargetURL    string
	OpenDevtools bool
}

// LaunchRequest 描述底层浏览器进程启动参数。
type LaunchRequest struct {
	Browser      BrowserRecord
	TargetURL    string
	OpenDevtools bool
}

// LaunchResult 描述底层浏览器启动后的 CDP 发现结果。
type LaunchResult struct {
	ProcessID   int
	DebugPort   int
	BrowserWS   string
	PageWS      string
	DevtoolsURL string
	Close       func() error
}

// Launcher 启动浏览器并返回 CDP 发现结果。
type Launcher func(context.Context, LaunchRequest) (LaunchResult, error)

// ManagerOptions 描述浏览器 session 管理器依赖。
type ManagerOptions struct {
	Launch Launcher
}

// SessionRecord 保存 runtime 需要的 session 进程关闭能力。
type SessionRecord struct {
	Session
	ProcessID int
	ClosedAt  time.Time `json:"closed_at,omitempty"`
	close     func() error
}

// Manager 管理由 SuperDev 创建的本机浏览器调试会话。
type Manager struct {
	mu       sync.Mutex
	launch   Launcher
	sessions map[string]*SessionRecord
}

// NewManager 创建浏览器调试 session 管理器。
func NewManager(opts ManagerOptions) *Manager {
	return &Manager{
		launch:   opts.Launch,
		sessions: map[string]*SessionRecord{},
	}
}

// Open 创建一个新的浏览器调试会话。
func (m *Manager) Open(ctx context.Context, req OpenResolvedRequest) (Session, error) {
	if req.Browser.ID == "" || req.Browser.ExecutablePath == "" {
		return Session{}, fmt.Errorf("browser is not configured")
	}
	if !req.Browser.Available {
		return Session{}, fmt.Errorf("browser executable is unavailable")
	}
	if m.launch == nil {
		return Session{}, fmt.Errorf("browser launcher is not configured")
	}
	result, err := m.launch(ctx, LaunchRequest{Browser: req.Browser, TargetURL: req.TargetURL, OpenDevtools: req.OpenDevtools})
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	record := &SessionRecord{
		Session: Session{
			ID:           "brs_" + uuid.NewString(),
			DeploymentID: req.Target.DeploymentID,
			TargetURL:    req.TargetURL,
			BrowserID:    req.Browser.ID,
			DebugPort:    result.DebugPort,
			BrowserWS:    result.BrowserWS,
			PageWS:       result.PageWS,
			DevtoolsURL:  result.DevtoolsURL,
			CreatedAt:    now,
		},
		ProcessID: result.ProcessID,
		close:     result.Close,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[record.ID] = record
	return record.Session, nil
}

// List 返回当前已知 session 快照。
func (m *Manager) List() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, record := range m.sessions {
		out = append(out, record.Session)
	}
	return out
}

// Get 返回指定 session 快照。
func (m *Manager) Get(id string) (SessionRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok {
		return SessionRecord{}, false
	}
	return *record, true
}

// Close 关闭指定浏览器调试会话。
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	record, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("browser session not found")
	}
	if record.Closed {
		m.mu.Unlock()
		return nil
	}
	record.Closed = true
	record.ClosedAt = time.Now().UTC()
	closeFn := record.close
	m.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}
