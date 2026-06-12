// manager.go 管理由 SuperDev 创建的本机代码调试 session。
//
// 职责：
//   - 启动 DAP adapter 并建立 DAP 连接
//   - 保存 session 与关闭函数的运行态映射
//   - 提供低层 DAP 操作和高层 capture/inspect 组合操作
//
// 边界：
//   - 不修改普通服务启停链路
//   - 不持久化 session
//   - 不支持远端、attach、systemd、Docker 等非 v1 场景
package codedebug

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/model"
)

// AdapterProcess 描述已启动的 DAP adapter 进程。
type AdapterProcess struct {
	PID   int
	Close func() error
}

// AdapterLauncher 启动 DAP adapter 进程。
type AdapterLauncher func(context.Context, AdapterCommand) (AdapterProcess, error)

// DAPDialer 连接 DAP adapter。
type DAPDialer func(context.Context, string, time.Duration) (DAP, error)

// PortReservoir 预留一个本机端口给 adapter 监听。
type PortReservoir func() (int, error)

// ManagerOptions 描述代码调试 session 管理器依赖。
type ManagerOptions struct {
	AdapterLaunch AdapterLauncher
	Dial          DAPDialer
	ReservePort   PortReservoir
	SessionTTL    time.Duration
	Now           func() time.Time
}

type runtimeRecord struct {
	Runtime
	rootPath string
	dap      DAP
	close    func() error
}

type sessionRecord struct {
	Session
	runtimeDeploymentID string
}

// Manager 管理由 SuperDev 创建的本机代码调试会话。
type Manager struct {
	mu       sync.Mutex
	launch   AdapterLauncher
	dial     DAPDialer
	reserve  PortReservoir
	ttl      time.Duration
	now      func() time.Time
	runtimes map[string]*runtimeRecord
	sessions map[string]*sessionRecord
	closed   map[string]Session
}

// NewManager 创建代码调试 session 管理器。
func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	ttl := opts.SessionTTL
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	launch := opts.AdapterLaunch
	if launch == nil {
		launch = defaultAdapterLaunch
	}
	dial := opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, addr string, timeout time.Duration) (DAP, error) {
			return DialDAP(ctx, addr, timeout)
		}
	}
	reserve := opts.ReservePort
	if reserve == nil {
		reserve = reserveLocalPort
	}
	return &Manager{
		launch:   launch,
		dial:     dial,
		reserve:  reserve,
		ttl:      ttl,
		now:      now,
		runtimes: map[string]*runtimeRecord{},
		sessions: map[string]*sessionRecord{},
		closed:   map[string]Session{},
	}
}

// Open 创建一个新的代码调试会话。
func (m *Manager) Open(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (Session, error) {
	cfg, provider, err := m.launchConfig(project, service, dep, req)
	if err != nil {
		return Session{}, err
	}
	runtime, err := m.startRuntimeFromConfig(ctx, cfg, provider)
	if err != nil {
		return Session{}, err
	}
	now := m.now().UTC()
	record := &sessionRecord{
		Session: Session{
			ID:           "cds_" + uuid.NewString(),
			ProjectID:    project.ID,
			DeploymentID: dep.ID,
			Provider:     runtime.Provider,
			AdapterPort:  runtime.AdapterPort,
			ProcessID:    runtime.ProcessID,
			RuntimeState: runtime.State,
			CreatedAt:    now,
			LastUsedAt:   now,
			Alive:        true,
		},
		runtimeDeploymentID: dep.ID,
	}

	m.mu.Lock()
	closeFns := m.cleanupLocked(now)
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	m.sessions[record.ID] = record
	return record.Session, nil
}

// StartRuntime 启动或复用 deployment 级 Debug Runtime。
func (m *Manager) StartRuntime(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (Runtime, error) {
	cfg, provider, err := m.launchConfig(project, service, dep, req)
	if err != nil {
		return Runtime{}, err
	}
	return m.startRuntimeFromConfig(ctx, cfg, provider)
}

func (m *Manager) startRuntimeFromConfig(ctx context.Context, cfg LaunchConfig, provider Provider) (Runtime, error) {
	m.mu.Lock()
	if existing, ok := m.runtimes[cfg.Target.DeploymentID]; ok && existing.Alive {
		existing.LastUsedAt = m.now().UTC()
		runtime := existing.Runtime
		m.mu.Unlock()
		return runtime, nil
	}
	m.mu.Unlock()

	port, err := m.reserve()
	if err != nil {
		return Runtime{}, err
	}
	cfg.AdapterPort = port
	cmd, err := provider.AdapterCommand(cfg)
	if err != nil {
		return Runtime{}, err
	}
	process, err := m.launch(ctx, cmd)
	if err != nil {
		return Runtime{}, ensureAdapterError(CodeAdapterStartFailed, cmd, err)
	}
	closeProcess := process.Close
	dap, err := m.dialAdapter(ctx, fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		_ = closeProcessIfPresent(closeProcess)
		return Runtime{}, ensureAdapterError(CodeDAPConnectionFailed, cmd, err)
	}
	if _, err := dap.Initialize(ctx); err != nil {
		_ = dap.Close()
		_ = closeProcessIfPresent(closeProcess)
		return Runtime{}, err
	}
	if err := dap.Launch(ctx, provider.LaunchArguments(cfg)); err != nil {
		_ = dap.Close()
		_ = closeProcessIfPresent(closeProcess)
		return Runtime{}, err
	}
	if err := dap.ConfigurationDone(ctx); err != nil {
		_ = dap.Close()
		_ = closeProcessIfPresent(closeProcess)
		return Runtime{}, err
	}
	if cfg.StopOnEntry {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := dap.WaitForStopped(waitCtx)
		cancel()
		if err != nil {
			_ = dap.Close()
			_ = closeProcessIfPresent(closeProcess)
			return Runtime{}, err
		}
	}

	now := m.now().UTC()
	record := &runtimeRecord{
		Runtime: Runtime{
			ProjectID:    cfg.Target.ProjectID,
			DeploymentID: cfg.Target.DeploymentID,
			Provider:     cfg.Provider,
			AdapterPort:  port,
			ProcessID:    process.PID,
			State:        RuntimeStateDebugRunning,
			Alive:        true,
			CreatedAt:    now,
			LastUsedAt:   now,
		},
		rootPath: cfg.Target.RootPath,
		dap:      dap,
		close: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = dap.Disconnect(ctx)
			_ = dap.Close()
			return closeProcessIfPresent(closeProcess)
		},
	}

	m.mu.Lock()
	m.runtimes[cfg.Target.DeploymentID] = record
	m.mu.Unlock()
	return record.Runtime, nil
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

// Status 返回 session 状态快照。
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

// RuntimeStatus 返回 deployment 级 Debug Runtime 状态快照。
func (m *Manager) RuntimeStatus(deploymentID string) (Runtime, bool) {
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[strings.TrimSpace(deploymentID)]
	if !ok {
		return Runtime{}, false
	}
	return runtime.Runtime, true
}

// LeaseActive 返回指定 deployment 是否存在活跃 AI lease。
func (m *Manager) LeaseActive(deploymentID string) bool {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		if session.runtimeDeploymentID == deploymentID && session.Alive && !session.Closed {
			return true
		}
	}
	return false
}

// StopRuntime 停止 deployment 级 Debug Runtime，并关闭关联 AI lease。
func (m *Manager) StopRuntime(deploymentID string) error {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	runtime, ok := m.runtimes[deploymentID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	runtime.Alive = false
	closeFn := runtime.close
	delete(m.runtimes, deploymentID)
	for id, session := range m.sessions {
		if session.runtimeDeploymentID != deploymentID {
			continue
		}
		session.Alive = false
		session.Closed = true
		delete(m.sessions, id)
		m.closed[id] = session.Session
	}
	m.mu.Unlock()
	return closeProcessIfPresent(closeFn)
}

// Close 关闭指定代码调试会话，可选择同时停止 Debug Runtime。
func (m *Manager) Close(id string, req CloseRequest) error {
	m.mu.Lock()
	record, ok := m.sessions[id]
	if !ok {
		if _, closed := m.closed[id]; closed {
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	record.Closed = true
	record.Alive = false
	delete(m.sessions, id)
	m.closed[id] = record.Session
	runtimeID := record.runtimeDeploymentID
	stopRuntime := req.StopRuntime != nil && *req.StopRuntime
	var closeFn func() error
	if stopRuntime {
		if runtime, ok := m.runtimes[runtimeID]; ok {
			runtime.Alive = false
			closeFn = runtime.close
			delete(m.runtimes, runtimeID)
		}
		for sessionID, session := range m.sessions {
			if session.runtimeDeploymentID != runtimeID {
				continue
			}
			session.Closed = true
			session.Alive = false
			delete(m.sessions, sessionID)
			m.closed[sessionID] = session.Session
		}
	}
	m.mu.Unlock()
	return closeProcessIfPresent(closeFn)
}

// SetBreakpoints 设置源码断点。
func (m *Manager) SetBreakpoints(ctx context.Context, sessionID, source string, lines []int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	sourcePath, err := ResolveInsideRoot(runtime.rootPath, source)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, ErrConfigInvalid
	}
	for _, line := range lines {
		if err := validatePositiveLine(line); err != nil {
			return nil, err
		}
	}
	return runtime.dap.SetBreakpoints(ctx, sourcePath, lines)
}

// ThreadAction 执行 continue/pause/step 动作。
func (m *Manager) ThreadAction(ctx context.Context, sessionID, action string, threadID int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	switch action {
	case "continue":
		err = runtime.dap.Continue(ctx, threadID)
	case "pause":
		err = runtime.dap.Pause(ctx, threadID)
	case "step_over":
		err = runtime.dap.Next(ctx, threadID)
	case "step_in":
		err = runtime.dap.StepIn(ctx, threadID)
	case "step_out":
		err = runtime.dap.StepOut(ctx, threadID)
	default:
		return nil, ErrConfigInvalid
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": sessionID, "action": action, "thread_id": threadID}, nil
}

// StackTrace 读取指定线程的调用栈。
func (m *Manager) StackTrace(ctx context.Context, sessionID string, threadID int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	return runtime.dap.StackTrace(ctx, threadID)
}

// Scopes 读取指定 frame 的 scopes。
func (m *Manager) Scopes(ctx context.Context, sessionID string, frameID int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	return runtime.dap.Scopes(ctx, frameID)
}

// Variables 读取指定 variablesReference 下的变量。
func (m *Manager) Variables(ctx context.Context, sessionID string, variablesReference int) (map[string]any, error) {
	result, err := m.variablesRaw(ctx, sessionID, variablesReference)
	if err != nil {
		return nil, err
	}
	return sanitizeDAPMap(result), nil
}

// Evaluate 在指定 frame 中求值。
func (m *Manager) Evaluate(ctx context.Context, sessionID, expression string, frameID int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	result, err := runtime.dap.Evaluate(ctx, expression, frameID)
	if err != nil {
		return nil, err
	}
	return sanitizeDAPMap(result), nil
}

// CaptureAtRequest 描述 stop-at-line 并采集现场的复合请求。
type CaptureAtRequest struct {
	SessionID     string        `json:"session_id"`
	Source        string        `json:"source"`
	Line          int           `json:"line"`
	ThreadID      int           `json:"thread_id"`
	Timeout       time.Duration `json:"-"`
	TimeoutMS     int           `json:"timeout_ms,omitempty"`
	MaxVariables  int           `json:"max_variables,omitempty"`
	VariableNames []string      `json:"variable_names,omitempty"`
}

// InspectRequest 描述读取暂停现场的复合请求。
type InspectRequest struct {
	SessionID     string   `json:"session_id"`
	ThreadID      int      `json:"thread_id"`
	FrameID       int      `json:"frame_id,omitempty"`
	MaxVariables  int      `json:"max_variables,omitempty"`
	VariableNames []string `json:"variable_names,omitempty"`
}

// CaptureAt 设置断点、继续运行、等待 stopped，然后采集当前栈和变量。
func (m *Manager) CaptureAt(ctx context.Context, req CaptureAtRequest) (map[string]any, error) {
	if err := validatePositiveLine(req.Line); err != nil {
		return nil, err
	}
	if _, err := m.SetBreakpoints(ctx, req.SessionID, req.Source, []int{req.Line}); err != nil {
		return nil, err
	}
	if err := m.threadContinue(ctx, req.SessionID, req.ThreadID); err != nil {
		return nil, err
	}
	waitCtx := ctx
	timeout := req.Timeout
	if timeout == 0 && req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_, runtime, err := m.sessionRuntime(req.SessionID)
	if err != nil {
		return nil, err
	}
	stopped, err := runtime.dap.WaitForStopped(waitCtx)
	if err != nil {
		return nil, err
	}
	threadID := intFromAny(stopped["threadId"])
	if threadID == 0 {
		threadID = req.ThreadID
	}
	inspect, err := m.Inspect(ctx, InspectRequest{
		SessionID:     req.SessionID,
		ThreadID:      threadID,
		MaxVariables:  req.MaxVariables,
		VariableNames: req.VariableNames,
	})
	if err != nil {
		return nil, err
	}
	inspect["stopped"] = stopped
	return inspect, nil
}

// Inspect 读取当前调用栈、选定 frame 的 scopes 和变量。
func (m *Manager) Inspect(ctx context.Context, req InspectRequest) (map[string]any, error) {
	stack, err := m.StackTrace(ctx, req.SessionID, req.ThreadID)
	if err != nil {
		return nil, err
	}
	frameID := req.FrameID
	if frameID == 0 {
		frameID = firstFrameID(stack)
	}
	scopes, err := m.Scopes(ctx, req.SessionID, frameID)
	if err != nil {
		return nil, err
	}
	variables := []map[string]any{}
	for _, scope := range asMapSlice(scopes["scopes"]) {
		ref := intFromAny(scope["variablesReference"])
		if ref == 0 {
			continue
		}
		got, err := m.variablesRaw(ctx, req.SessionID, ref)
		if err != nil {
			return nil, err
		}
		for _, variable := range filterVariables(asMapSlice(got["variables"]), req.MaxVariables, req.VariableNames) {
			if sanitized, ok := sanitizeDAPValue(variable).(map[string]any); ok {
				variables = append(variables, sanitized)
			}
		}
	}
	return map[string]any{
		"session_id": req.SessionID,
		"thread_id":  req.ThreadID,
		"frame_id":   frameID,
		"stack":      stack,
		"scopes":     scopes,
		"variables":  variables,
	}, nil
}

func (m *Manager) launchConfig(project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (LaunchConfig, Provider, error) {
	if !isSupportedTarget(dep) {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	code := dep.CodeDebug
	if code.Mode != "" && code.Mode != model.CodeDebugModeLaunch {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	providerName := code.Provider
	command := debugDeploymentCommand(dep)
	if req.Provider != "" {
		providerName = model.CodeDebugProvider(strings.TrimSpace(req.Provider))
	}
	if providerName == "" {
		providerName = inferProvider(command)
	}
	provider, err := providerFor(providerName)
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	workingDir := strings.TrimSpace(code.WorkingDir)
	if workingDir == "" {
		workingDir = debugDeploymentWorkDir(dep)
	}
	if workingDir == "" {
		workingDir = project.RootPath
	}
	workingDir, err = ResolveInsideRoot(project.RootPath, workingDir)
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	program := strings.TrimSpace(code.Program)
	programExplicit := program != ""
	if !programExplicit {
		program, err = DefaultProgramForProvider(providerName, command)
		if err != nil {
			return LaunchConfig{}, nil, err
		}
	}
	programPath, err := resolveLaunchProgramPath(project.RootPath, workingDir, program, programExplicit)
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	if providerName == model.CodeDebugProviderGo {
		programPath = goDAPProgramPath(workingDir, programPath)
	}
	env := mergeEnv(dep.Env, code.EnvVars)
	stopOnEntry := code.StopOnEntry
	if req.StopOnEntry != nil {
		stopOnEntry = *req.StopOnEntry
	}
	target := Target{
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		RootPath:     project.RootPath,
		ServiceID:    service.ID,
		ServiceName:  service.Name,
		DeploymentID: dep.ID,
		EnvName:      dep.EnvName,
		Provider:     providerName,
		Experimental: providerName == model.CodeDebugProviderNode,
		Command:      command,
		WorkDir:      debugDeploymentWorkDir(dep),
		Enabled:      true,
	}
	return LaunchConfig{
		Target:         target,
		Provider:       providerName,
		Program:        programPath,
		Args:           append([]string{}, code.Args...),
		WorkingDir:     workingDir,
		Env:            env,
		AdapterCommand: strings.TrimSpace(code.AdapterCommand),
		AdapterArgs:    append([]string{}, code.AdapterArgs...),
		StopOnEntry:    stopOnEntry,
	}, provider, nil
}

// DefaultProgramForProvider 返回 provider 在未显式配置 program 时的默认 launch 入口。
func DefaultProgramForProvider(provider model.CodeDebugProvider, command string) (string, error) {
	switch provider {
	case model.CodeDebugProviderGo:
		return ".", nil
	case model.CodeDebugProviderPython, model.CodeDebugProviderNode:
		if program, ok := inferProgramFromSimpleCommand(provider, command); ok {
			return program, nil
		}
		return "", ErrConfigInvalid
	default:
		return "", ErrTargetUnsupported
	}
}

func resolveLaunchProgramPath(projectRoot, workingDir, program string, explicit bool) (string, error) {
	if explicit || filepath.IsAbs(program) {
		return ResolveInsideRoot(projectRoot, program)
	}
	base := strings.TrimSpace(workingDir)
	if base == "" {
		base = projectRoot
	}
	return ResolveInsideRoot(projectRoot, filepath.Join(base, program))
}

func goDAPProgramPath(workingDir, resolvedProgram string) string {
	workingDir = strings.TrimSpace(workingDir)
	resolvedProgram = strings.TrimSpace(resolvedProgram)
	if workingDir == "" || resolvedProgram == "" {
		return resolvedProgram
	}
	// Delve treats absolute symlinked paths as outside the Go module in some
	// workspace layouts, so keep SuperDev's root validation above and pass DAP
	// a path relative to the configured cwd.
	rel, err := filepath.Rel(workingDir, resolvedProgram)
	if err != nil || rel == "" || filepath.IsAbs(rel) {
		return resolvedProgram
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return "." + string(filepath.Separator) + rel
}

func (m *Manager) sessionRuntime(sessionID string) (*sessionRecord, *runtimeRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil, ErrSessionNotFound
	}
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	record, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil, ErrSessionNotFound
	}
	if record.Closed {
		return nil, nil, ErrSessionClosed
	}
	runtime, err := m.runtimeForSessionLocked(record)
	if err != nil {
		return nil, nil, err
	}
	now := m.now().UTC()
	record.LastUsedAt = now
	runtime.LastUsedAt = now
	return record, runtime, nil
}

func (m *Manager) runtimeForSessionLocked(record *sessionRecord) (*runtimeRecord, error) {
	runtime, ok := m.runtimes[record.runtimeDeploymentID]
	if !ok || !runtime.Alive {
		return nil, ErrSessionClosed
	}
	return runtime, nil
}

func (m *Manager) variablesRaw(ctx context.Context, sessionID string, variablesReference int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	return runtime.dap.Variables(ctx, variablesReference)
}

func (m *Manager) dialAdapter(ctx context.Context, addr string, timeout time.Duration) (DAP, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		dap, err := m.dial(ctx, addr, 250*time.Millisecond)
		if err == nil {
			return dap, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (m *Manager) threadContinue(ctx context.Context, sessionID string, threadID int) error {
	_, err := m.ThreadAction(ctx, sessionID, "continue", threadID)
	return err
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
			record.Alive = false
			if expired {
				record.Error = "code debug session idle timeout"
			}
		}
		delete(m.sessions, id)
		m.closed[id] = record.Session
	}
	return closeFns
}

func providerFor(provider model.CodeDebugProvider) (Provider, error) {
	switch provider {
	case model.CodeDebugProviderGo:
		return NewGoProvider(), nil
	case model.CodeDebugProviderPython:
		return NewPythonProvider("python3"), nil
	case model.CodeDebugProviderNode:
		return NewNodeProvider(), nil
	default:
		return nil, ErrTargetUnsupported
	}
}

func defaultAdapterLaunch(ctx context.Context, cmd AdapterCommand) (AdapterProcess, error) {
	if strings.TrimSpace(cmd.Name) == "" {
		return AdapterProcess{}, NewAdapterError(CodeAdapterUnavailable, cmd, ErrAdapterUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return AdapterProcess{}, NewAdapterError(CodeAdapterStartFailed, cmd, err)
	}
	// Adapter lifetime is owned by Manager.Close/StopRuntime. Binding the OS
	// process to an HTTP request context would kill Delve as soon as the open
	// request returns, leaving the debuggee orphaned and future DAP calls broken.
	c := exec.Command(cmd.Name, cmd.Args...)
	if strings.TrimSpace(cmd.WorkDir) != "" {
		c.Dir = strings.TrimSpace(cmd.WorkDir)
	}
	if len(cmd.Env) > 0 {
		c.Env = append(c.Environ(), envPairs(cmd.Env)...)
	}
	if err := c.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return AdapterProcess{}, NewAdapterError(CodeAdapterUnavailable, cmd, err)
		}
		return AdapterProcess{}, NewAdapterError(CodeAdapterStartFailed, cmd, err)
	}
	go func() {
		_ = c.Wait()
	}()
	return AdapterProcess{
		PID: c.Process.Pid,
		Close: func() error {
			if c.Process != nil {
				_ = c.Process.Kill()
			}
			return nil
		},
	}, nil
}

func ensureAdapterError(code string, cmd AdapterCommand, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := AdapterErrorDetails(err); ok {
		return err
	}
	return NewAdapterError(code, cmd, err)
}

func debugDeploymentCommand(dep model.Deployment) string {
	if dep.Runtime != nil && strings.TrimSpace(dep.Runtime.Command) != "" {
		return strings.TrimSpace(dep.Runtime.Command)
	}
	return strings.TrimSpace(dep.Command)
}

func debugDeploymentWorkDir(dep model.Deployment) string {
	if dep.Runtime != nil && strings.TrimSpace(dep.Runtime.WorkingDir) != "" {
		return strings.TrimSpace(dep.Runtime.WorkingDir)
	}
	return strings.TrimSpace(dep.WorkDir)
}

func inferProgramFromSimpleCommand(provider model.CodeDebugProvider, command string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 {
		return "", false
	}
	if !commandExecutableMatchesProvider(provider, fields[0]) {
		return "", false
	}
	for _, field := range fields {
		if strings.ContainsAny(field, "|&;<>") {
			return "", false
		}
	}
	program := strings.TrimSpace(fields[1])
	if program == "" || strings.HasPrefix(program, "-") {
		return "", false
	}
	return program, true
}

func commandExecutableMatchesProvider(provider model.CodeDebugProvider, executable string) bool {
	executable = strings.TrimSpace(executable)
	if idx := strings.LastIndex(executable, "/"); idx >= 0 {
		executable = executable[idx+1:]
	}
	switch provider {
	case model.CodeDebugProviderPython:
		return executable == "python" || executable == "python3"
	case model.CodeDebugProviderNode:
		return executable == "node"
	default:
		return false
	}
}

func reserveLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func envPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func mergeEnv(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func sessionStatus(record *sessionRecord) Session {
	session := record.Session
	if record.Closed {
		session.Alive = false
	}
	return session
}

func closeSessionFns(closeFns []func() error) {
	for _, closeFn := range closeFns {
		_ = closeProcessIfPresent(closeFn)
	}
}

func closeProcessIfPresent(closeFn func() error) error {
	if closeFn == nil {
		return nil
	}
	return closeFn()
}

func validatePositiveLine(line int) error {
	if line <= 0 {
		return ErrConfigInvalid
	}
	return nil
}

func firstFrameID(stack map[string]any) int {
	for _, frame := range asMapSlice(stack["stackFrames"]) {
		if id := intFromAny(frame["id"]); id != 0 {
			return id
		}
	}
	return 0
}

func asMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func filterVariables(vars []map[string]any, max int, names []string) []map[string]any {
	nameSet := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			nameSet[name] = true
		}
	}
	out := make([]map[string]any, 0, len(vars))
	for _, variable := range vars {
		if len(nameSet) > 0 {
			name, _ := variable["name"].(string)
			if !nameSet[name] {
				continue
			}
		}
		out = append(out, variable)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}
