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
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// OpenResolvedRequest 是 API 层完成 deployment/browser 解析后的打开请求。
type OpenResolvedRequest struct {
	Browser        BrowserRecord
	Target         Target
	TargetURL      string
	OpenDevtools   bool
	ProfileMode    string
	ViewportWidth  int
	ViewportHeight int
}

// LaunchRequest 描述底层浏览器进程启动参数。
type LaunchRequest struct {
	Browser        BrowserRecord
	TargetURL      string
	OpenDevtools   bool
	ProfileMode    string
	ProfileScope   string
	ViewportWidth  int
	ViewportHeight int
}

// LaunchResult 描述底层浏览器启动后的 CDP 发现结果。
type LaunchResult struct {
	ProcessID   int
	DebugPort   int
	BrowserWS   string
	PageWS      string
	DevtoolsURL string
	ProfileDir  string
	Alive       func() bool
	Close       func() error
}

// Launcher 启动浏览器并返回 CDP 发现结果。
type Launcher func(context.Context, LaunchRequest) (LaunchResult, error)

// ManagerOptions 描述浏览器 session 管理器依赖。
type ManagerOptions struct {
	Launch     Launcher
	SessionTTL time.Duration
	Now        func() time.Time
}

// SessionRecord 保存 runtime 需要的 session 进程关闭能力。
type SessionRecord struct {
	Session
	ProcessID int
	ClosedAt  time.Time `json:"closed_at,omitempty"`
	alive     func() bool
	close     func() error
}

// Manager 管理由 SuperDev 创建的本机浏览器调试会话。
type Manager struct {
	mu       sync.Mutex
	launch   Launcher
	ttl      time.Duration
	now      func() time.Time
	sessions map[string]*SessionRecord
	closed   map[string]Session
}

// NewManager 创建浏览器调试 session 管理器。
func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		launch:   opts.Launch,
		ttl:      opts.SessionTTL,
		now:      now,
		sessions: map[string]*SessionRecord{},
		closed:   map[string]Session{},
	}
}

// Open 创建或复用一个浏览器调试会话。
func (m *Manager) Open(ctx context.Context, req OpenResolvedRequest) (Session, error) {
	if req.Browser.ID == "" || req.Browser.ExecutablePath == "" {
		return Session{}, fmt.Errorf("browser is not configured")
	}
	if !req.Browser.Available {
		return Session{}, fmt.Errorf("browser executable is unavailable")
	}
	now := m.now().UTC()
	m.mu.Lock()
	closeFns := m.cleanupLocked(now)
	if record, ok := m.findReusableLocked(req, true); ok {
		session := m.touchRecordLocked(record, now)
		m.mu.Unlock()
		closeSessionFns(closeFns)
		log.Printf("[SuperDev] reusing debug browser session session=%s deployment=%s browser=%s target=%s", session.ID, session.DeploymentID, session.BrowserID, session.TargetURL)
		return session, nil
	}
	m.mu.Unlock()
	closeSessionFns(closeFns)

	if m.launch == nil {
		return Session{}, fmt.Errorf("browser launcher is not configured")
	}
	result, err := m.launch(ctx, LaunchRequest{
		Browser:        req.Browser,
		TargetURL:      req.TargetURL,
		OpenDevtools:   req.OpenDevtools,
		ProfileMode:    req.ProfileMode,
		ViewportWidth:  req.ViewportWidth,
		ViewportHeight: req.ViewportHeight,
		// deployment 级隔离可避免两个本机服务复用同一端口时共享登录态。
		ProfileScope: req.Target.DeploymentID,
	})
	if err != nil {
		return Session{}, err
	}
	now = m.now().UTC()
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
			LastUsedAt:   now,
			Alive:        true,
			ProfileDir:   result.ProfileDir,
		},
		ProcessID: result.ProcessID,
		alive:     result.Alive,
		close:     result.Close,
	}
	m.mu.Lock()
	closeFns = m.cleanupLocked(now)
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	m.sessions[record.ID] = record
	return record.Session, nil
}

// FindReusable 返回同一 deployment 和 browser 的存活调试会话。
//
// 参数：
//   - req: 已解析的打开请求，用于匹配 deployment 与 browser
//
// 返回：
//   - 可复用 session 快照，并刷新 LastUsedAt
//   - 是否存在可复用 session
//
// 注意：
//   - 不要求 target URL 相同；调用方可在复用后导航到目标 path
//   - 不创建浏览器进程
func (m *Manager) FindReusable(req OpenResolvedRequest) (Session, bool) {
	now := m.now().UTC()
	m.mu.Lock()
	closeFns := m.cleanupLocked(now)
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	record, ok := m.findReusableLocked(req, false)
	if !ok {
		return Session{}, false
	}
	return m.touchRecordLocked(record, now), true
}

// UpdateTargetURL 更新 session 当前目标 URL，供复用会话导航后同步状态。
func (m *Manager) UpdateTargetURL(id string, targetURL string) (Session, bool) {
	now := m.now().UTC()
	m.mu.Lock()
	closeFns := m.cleanupLocked(now)
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok || record.Closed {
		return Session{}, false
	}
	record.TargetURL = targetURL
	return m.touchRecordLocked(record, now), true
}

// List 返回当前已知 session 快照。
func (m *Manager) List() []Session {
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, record := range m.sessions {
		out = append(out, sessionStatus(record))
	}
	return out
}

// Get 返回指定 session 快照。
func (m *Manager) Get(id string) (SessionRecord, bool) {
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok {
		return SessionRecord{}, false
	}
	out := *record
	out.Session = sessionStatus(record)
	return out, true
}

// Touch 刷新 session 的最近使用时间，用于 idle TTL 续期。
func (m *Manager) Touch(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok || record.Closed {
		return false
	}
	record.LastUsedAt = m.now().UTC()
	return true
}

// Status 返回 session 状态快照，包含浏览器进程存活检查结果。
func (m *Manager) Status(id string) (Session, bool) {
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	if record, ok := m.sessions[id]; ok {
		return sessionStatus(record), true
	}
	session, ok := m.closed[id]
	return session, ok
}

// Close 关闭指定浏览器调试会话。
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	record, ok := m.sessions[id]
	if !ok {
		if _, closed := m.closed[id]; closed {
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		return fmt.Errorf("browser session not found")
	}
	if record.Closed {
		delete(m.sessions, id)
		m.mu.Unlock()
		return nil
	}
	record.Closed = true
	record.ClosedAt = m.now().UTC()
	record.Alive = false
	closeFn := record.close
	delete(m.sessions, id)
	m.closed[id] = record.Session
	m.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}

func (m *Manager) cleanupLocked(now time.Time) []func() error {
	closeFns := []func() error{}
	for id, record := range m.sessions {
		expired := m.ttl > 0 && now.Sub(record.LastUsedAt) > m.ttl
		if !record.Closed && !expired {
			continue
		}
		if !record.Closed {
			record.Closed = true
			record.ClosedAt = now
			record.Alive = false
			if expired {
				record.Error = "browser session idle timeout"
			}
			if record.close != nil {
				closeFns = append(closeFns, record.close)
			}
		}
		delete(m.sessions, id)
		m.closed[id] = record.Session
	}
	return closeFns
}

func (m *Manager) findReusableLocked(req OpenResolvedRequest, requireSameTarget bool) (*SessionRecord, bool) {
	for _, record := range m.sessions {
		if reusableSessionMatches(record, req, requireSameTarget) {
			return record, true
		}
	}
	return nil, false
}

func (m *Manager) touchRecordLocked(record *SessionRecord, now time.Time) Session {
	record.LastUsedAt = now
	return sessionStatus(record)
}

func reusableSessionMatches(record *SessionRecord, req OpenResolvedRequest, requireSameTarget bool) bool {
	if record == nil || record.Closed {
		return false
	}
	if record.DeploymentID != req.Target.DeploymentID || record.BrowserID != req.Browser.ID {
		return false
	}
	if record.alive != nil && !record.alive() {
		return false
	}
	if requireSameTarget && !targetURLMatches(record.TargetURL, req.TargetURL) {
		return false
	}
	return true
}

func sessionStatus(record *SessionRecord) Session {
	session := record.Session
	session.Alive = record.alive == nil || record.alive()
	if !session.Alive && session.Error == "" {
		session.Error = "browser process is not alive"
	}
	return session
}

func closeSessionFns(closeFns []func() error) {
	for _, closeFn := range closeFns {
		_ = closeFn()
	}
}
