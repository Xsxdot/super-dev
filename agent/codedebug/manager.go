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
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/execenv"
	"github.com/xsxdot/super-dev/agent/langruntime"
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

// RunningProcessProbe 返回某 deployment 普通运行态的进程信息。
//
// running=false 表示未以普通 runtime 运行（不能 attach，应提示 launch）。
type RunningProcessProbe func(deploymentID string) (mainPID int, pgid int, running bool)

// RunningProcessArgvProbe 返回某 deployment 普通运行态主进程的启动 argv。
//
// 供 prearm-listen 语言从 argv 的 `--listen host:port` 反解调试端口——argv 即真相源，
// 避免单独持久化端口。返回 nil/空表示拿不到 argv。
type RunningProcessArgvProbe func(deploymentID string) []string

// RunningProcessStderrProbe 返回某 deployment 普通运行态进程最近的 stderr 行。
//
// 供 Node 从 SIGUSR1 唤醒的 `Debugger listening on ws://...:port` 解析 inspector 端口。
type RunningProcessStderrProbe func(deploymentID string) []string

// processGroupLister 枚举指定进程组内的进程。
type processGroupLister func(pgid int) []procInfo

// signalProcessFunc 给运行中进程发送信号；测试可替换以避免触碰真实进程。
type signalProcessFunc func(pid int, signal string) error

// attachTarget 描述一次 attach 的目标进程。
type attachTarget struct {
	processID int
}

// ManagerOptions 描述代码调试 session 管理器依赖。
type ManagerOptions struct {
	AdapterLaunch AdapterLauncher
	Dial          DAPDialer
	ReservePort   PortReservoir
	SessionTTL    time.Duration
	Now           func() time.Time
	// RunningProcess 探测某 deployment 的普通运行态进程信息（用于 attach）。nil 表示不支持 attach。
	RunningProcess RunningProcessProbe
	// RunningProcessArgv 返回某 deployment 普通运行态主进程的启动 argv（用于 prearm-listen
	// 语言从 `--listen host:port` 反解调试端口）。nil 或返回空表示无法获取，prearm attach 不可用。
	RunningProcessArgv RunningProcessArgvProbe
	// RunningProcessStderr 返回某 deployment 普通运行态进程最近的 stderr 行（用于 Node inspector 端口解析）。
	RunningProcessStderr RunningProcessStderrProbe
	// JSDebugServerPath 指向打包的 @vscode/js-debug standalone DAP server 入口。
	JSDebugServerPath string
	// listProcessGroup 是包内测试 hook，用于替换 OS 进程组枚举；nil 时使用 OS ps 实现。
	listProcessGroup processGroupLister
	// SignalProcess 是包内测试 hook，用于替换真实 syscall.Kill。
	SignalProcess func(pid int, signal string) error
	// ProcessAlive 探测某 pid 的进程是否存活（复用 runtime 前的 liveness 兜底）。
	// nil 时使用 OS 默认实现；测试可替换。
	ProcessAlive func(pid int) bool
}

type runtimeRecord struct {
	Runtime
	rootPath string
	// sourceRoot 是断点源码路径的解析基准（已解析的工作目录，经 symlink 规范化）。
	// language runtime 的 cwd 是子目录，源码相对 cwd 而非项目根；用 rootPath 会少算 cwd 层。
	sourceRoot string
	dap        DAP
	close      func() error
	debugStore *debuggerSnapshotStore
	pump       *eventPump
}

type sessionRecord struct {
	Session
	runtimeDeploymentID string
}

// Manager 管理由 SuperDev 创建的本机代码调试会话。
type Manager struct {
	mu                   sync.Mutex
	launch               AdapterLauncher
	dial                 DAPDialer
	reserve              PortReservoir
	ttl                  time.Duration
	now                  func() time.Time
	runningProcess       RunningProcessProbe
	runningProcessArgv   RunningProcessArgvProbe
	runningProcessStderr RunningProcessStderrProbe
	jsDebugServerPath    string
	listProcessGroup     processGroupLister
	signalProcess        signalProcessFunc
	processAlive         func(pid int) bool
	runtimes             map[string]*runtimeRecord
	sessions             map[string]*sessionRecord
	closed               map[string]Session
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
	listProcessGroup := opts.listProcessGroup
	if listProcessGroup == nil {
		listProcessGroup = listProcessGroupOS
	}
	signalProcess := opts.SignalProcess
	if signalProcess == nil {
		signalProcess = signalProcessOS
	}
	processAlive := opts.ProcessAlive
	if processAlive == nil {
		processAlive = processAliveOS
	}
	return &Manager{
		launch:               launch,
		dial:                 dial,
		reserve:              reserve,
		ttl:                  ttl,
		now:                  now,
		runningProcess:       opts.RunningProcess,
		runningProcessArgv:   opts.RunningProcessArgv,
		runningProcessStderr: opts.RunningProcessStderr,
		jsDebugServerPath:    opts.JSDebugServerPath,
		listProcessGroup:     listProcessGroup,
		signalProcess:        signalProcess,
		processAlive:         processAlive,
		runtimes:             map[string]*runtimeRecord{},
		sessions:             map[string]*sessionRecord{},
		closed:               map[string]Session{},
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
	return m.createSessionForRuntime(runtime), nil
}

func (m *Manager) createSessionForRuntime(runtime Runtime) Session {
	now := m.now().UTC()
	record := &sessionRecord{
		Session: Session{
			ID:           "cds_" + uuid.NewString(),
			ProjectID:    runtime.ProjectID,
			DeploymentID: runtime.DeploymentID,
			Provider:     runtime.Provider,
			AdapterPort:  runtime.AdapterPort,
			ProcessID:    runtime.ProcessID,
			RuntimeState: runtime.State,
			CreatedAt:    now,
			LastUsedAt:   now,
			Alive:        true,
		},
		runtimeDeploymentID: runtime.DeploymentID,
	}

	m.mu.Lock()
	closeFns := m.cleanupLocked(now)
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	m.sessions[record.ID] = record
	return record.Session
}

// StartRuntime 启动或复用 deployment 级 Debug Runtime。
func (m *Manager) StartRuntime(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (Runtime, error) {
	cfg, provider, err := m.launchConfig(project, service, dep, req)
	if err != nil {
		return Runtime{}, err
	}
	return m.startRuntimeFromConfig(ctx, cfg, provider)
}

// ResolveLease 按 deployment 解析或创建 AI lease。
//
// 返回：
//   - session: 复用的或新建的 lease
//   - created: 是否本次新建
//   - err: runtime 和普通服务都不在时返回 ErrRuntimeNotRunning，不静默启动进程
//
// 规则：
//   - 已有活跃 lease，直接复用
//   - runtime 在跑且无 lease，通过 Open 创建新 lease
//   - runtime 不在但普通服务运行中，优先 attach
//   - attach 不可用时返回结构化错误，不静默 launch
func (m *Manager) ResolveLease(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, approvalToken string) (Session, bool, error) {
	deploymentID := strings.TrimSpace(dep.ID)
	if existing, ok := m.activeLeaseFor(deploymentID); ok {
		return existing, false, nil
	}
	if _, ok := m.reusableRuntime(deploymentID); ok {
		return m.openLeaseOnRuntime(ctx, project, service, dep, approvalToken)
	}
	if attached, err := m.tryAttachRunning(ctx, project, service, dep); err != nil {
		return Session{}, false, err
	} else if attached {
		// attach 刚完成，runtime 必然新鲜，直接建 lease；不再过 liveness 探测
		// （探测服务于"陈旧 runtime 复用"场景，这里多余且会误伤）。
		if runtime, ok := m.RuntimeStatus(deploymentID); ok && runtime.Alive {
			return m.createSessionForRuntime(runtime), true, nil
		}
		return Session{}, false, ErrRuntimeNotRunning
	}
	return Session{}, false, ErrRuntimeNotRunning
}

// reusableRuntime 返回可安全复用的 runtime 快照。
//
// 在 Alive 标记之外增加进程存活兜底：debuggee/adapter 死亡但 terminated 事件
// 丢失时（agent 断连、事件竞态），Alive 会滞留 true——2026-07-07 实测这种死
// runtime 会劫持后续所有调试请求。发现进程已死立即 retire，调用方回落到
// 重新 attach 路径。
func (m *Manager) reusableRuntime(deploymentID string) (Runtime, bool) {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	record, ok := m.runtimes[deploymentID]
	if !ok || !record.Alive {
		m.mu.Unlock()
		return Runtime{}, false
	}
	runtime := record.Runtime
	pid := record.ProcessID
	m.mu.Unlock()
	if pid > 0 && m.processAlive != nil && !m.processAlive(pid) {
		log.Printf("[codedebug] runtime process dead on reuse check deployment=%s pid=%d, retiring", deploymentID, pid)
		m.retireRuntime(deploymentID, record)
		return Runtime{}, false
	}
	return runtime, true
}

func (m *Manager) startRuntimeFromConfig(ctx context.Context, cfg LaunchConfig, provider Provider) (Runtime, error) {
	// 复用前做 liveness 兜底：进程已死的 runtime 会被 retire，走下方新建路径。
	if _, ok := m.reusableRuntime(cfg.Target.DeploymentID); ok {
		m.mu.Lock()
		if existing, stillThere := m.runtimes[cfg.Target.DeploymentID]; stillThere && existing.Alive {
			existing.LastUsedAt = m.now().UTC()
			runtime := existing.Runtime
			m.mu.Unlock()
			return runtime, nil
		}
		m.mu.Unlock()
	}

	port, err := m.reserve()
	if err != nil {
		return Runtime{}, err
	}
	cfg.AdapterPort = port
	cmd, process, err := m.resolveAndLaunchAdapter(ctx, cfg, provider)
	if err != nil {
		return Runtime{}, err
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
	requestSub, cancelRequestSub := subscribeJSDebugRequests(provider, dap)
	if cancelRequestSub != nil {
		defer cancelRequestSub()
	}
	runtimeDAP := dap
	rootDAP := dap
	// 仅 js-debug 拓扑需要 spawn child session；以能力查询代替语言硬编码，新语言无需改本文件。
	if provider.UsesReverseRequestChildSession() {
		runtimeDAP, err = m.runJSDebugRootAndChildHandshake(ctx, rootDAP, requestSub, port, "launch", func(handshakeCtx context.Context) error {
			return launchAndConfigure(handshakeCtx, rootDAP, provider.LaunchArguments(cfg))
		})
		if err != nil {
			_ = rootDAP.Close()
			_ = closeProcessIfPresent(closeProcess)
			return Runtime{}, err
		}
	} else {
		if err := launchAndConfigure(ctx, rootDAP, provider.LaunchArguments(cfg)); err != nil {
			_ = rootDAP.Close()
			_ = closeProcessIfPresent(closeProcess)
			return Runtime{}, err
		}
	}
	now := m.now().UTC()
	store := &debuggerSnapshotStore{}
	pump := newEventPump(runtimeDAP, store)
	record := &runtimeRecord{
		Runtime: Runtime{
			ProjectID:    cfg.Target.ProjectID,
			DeploymentID: cfg.Target.DeploymentID,
			Provider:     cfg.Provider,
			AdapterPort:  port,
			ProcessID:    process.PID,
			State:        RuntimeStateDebugRunning,
			Origin:       "launched",
			Alive:        true,
			CreatedAt:    now,
			LastUsedAt:   now,
		},
		rootPath:   cfg.Target.RootPath,
		sourceRoot: resolveSourceRoot(cfg.WorkingDir, cfg.Target.RootPath),
		dap:        runtimeDAP,
		debugStore: store,
		pump:       pump,
		close: func() error {
			pump.stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = runtimeDAP.Disconnect(ctx)
			_ = runtimeDAP.Close()
			if rootDAP != runtimeDAP {
				_ = rootDAP.Close()
			}
			return closeProcessIfPresent(closeProcess)
		},
	}
	// 回调必须在 pump.start 之前挂上，否则启动即死的 debuggee 的 terminated
	// 事件会漏过反向失效。retire 需等 pump loop 退出，必须异步。
	pump.onTerminated = func() {
		go m.retireRuntime(cfg.Target.DeploymentID, record)
	}
	pump.start(context.Background())
	if cfg.StopOnEntry {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := runtimeDAP.WaitForStopped(waitCtx)
		cancel()
		if err != nil {
			pump.stop()
			_ = runtimeDAP.Close()
			if rootDAP != runtimeDAP {
				_ = rootDAP.Close()
			}
			_ = closeProcessIfPresent(closeProcess)
			return Runtime{}, err
		}
	}

	m.mu.Lock()
	// debuggee 在启动窗口内已终止（retireRuntime 抢先翻转 Alive）：拒绝登记
	// 死 runtime，否则它会以 Alive=true 一直留在 map 里被后续调试复用。
	if !record.Alive {
		m.mu.Unlock()
		_ = closeProcessIfPresent(record.close)
		return Runtime{}, fmt.Errorf("debug runtime terminated during startup: %w", ErrRuntimeNotRunning)
	}
	m.runtimes[cfg.Target.DeploymentID] = record
	m.mu.Unlock()
	return record.Runtime, nil
}

// AttachRuntime 是「按已解析 PID 直接 pid-attach」的低层原语，固定走 attach-pid。
//
// 契约：调用方必须已自行解析出 debuggee PID 并确认目标支持 pid-attach（如 Go）。
// 它不按语言派生 readiness——Node 的 signal、Python 的 prearm 必须走 tryAttachRunning
// 的 readiness 派生路径，不要把它们路由到这里，否则会错误地按 PID attach。
//
// 参数：
//   - ctx: 请求上下文，用于控制 adapter 启动和 DAP 初始化
//   - project/service/dep: 目标 deployment 所属上下文
//   - target: 已解析出的真实 debuggee 进程
//
// 返回：
//   - 创建后的 Runtime，Origin 固定为 attached
//   - attach 不可用、PID 无效或 adapter/DAP 失败时返回错误
//
// 注意：
//   - close 时只断开 DAP 并关闭 adapter，不终止被调试的普通服务进程
func (m *Manager) AttachRuntime(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, target attachTarget) (Runtime, error) {
	cfg, provider, err := m.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})
	if err != nil {
		return Runtime{}, err
	}
	return m.attachWithReadiness(ctx, readinessRequest{
		cfg:       cfg,
		provider:  provider,
		readiness: langruntime.ReadinessAttachPID,
		pid:       target.processID,
	})
}

type readinessRequest struct {
	cfg       LaunchConfig
	provider  Provider
	readiness string
	signal    string
	pid       int
	port      int
}

const defaultNodeInspectorPort = 9229

type nodeInspectorFallback struct {
	port    int
	enabled bool
}

// attachWithReadiness 按 readiness 把进程带到「可建立 DAP 连接」状态：
//   - signal-then-attach：发信号唤醒 inspector，再走 PID attach
//   - prearm-listen：直连进程自带的 listen 端口
//   - attach-pid：直接 PID attach（Go 现状）
func (m *Manager) attachWithReadiness(ctx context.Context, req readinessRequest) (Runtime, error) {
	switch req.readiness {
	case langruntime.ReadinessSignalAttach:
		if req.pid <= 0 {
			return Runtime{}, ErrAttachTargetUnresolved
		}
		if err := m.signalProcess(req.pid, req.signal); err != nil {
			return Runtime{}, fmt.Errorf("signal debuggee: %w", err)
		}
		return m.attachByPID(ctx, req.cfg, req.provider, req.pid)
	case langruntime.ReadinessPrearmListen:
		// debuggee 预埋的端口若本身就是 DAP 服务（Python debugpy --listen），直连即可；
		// 若只是待 adapter 连接的目标端口（Node inspector），仍走 adapter-spawn。
		if req.provider.AttachCapability() == AttachModeDirectDAP {
			return m.attachByDirectDAP(ctx, req.cfg, req.provider, req.port, req.pid)
		}
		return m.attachByListenPort(ctx, req.cfg, req.provider, req.port, req.pid)
	default:
		return m.attachByPID(ctx, req.cfg, req.provider, req.pid)
	}
}

// attachByDirectDAP 直连被调试进程预埋的 DAP 服务端口（如 debugpy --listen），
// 不另起 adapter 进程：targetPort 即 DAP 端点，dial 后直接 Initialize+attach。
func (m *Manager) attachByDirectDAP(ctx context.Context, cfg LaunchConfig, provider Provider, targetPort int, processID int) (Runtime, error) {
	if provider.AttachCapability() != AttachModeDirectDAP {
		return Runtime{}, ErrAttachUnsupported
	}
	if targetPort <= 0 {
		return Runtime{}, ErrAttachTargetUnresolved
	}
	cfg.TargetPort = targetPort
	return m.attachRuntimeDirect(ctx, cfg, provider, targetPort, processID, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, processID)
	})
}

func (m *Manager) attachByPID(ctx context.Context, cfg LaunchConfig, provider Provider, processID int) (Runtime, error) {
	if provider.AttachCapability() != AttachModePID {
		return Runtime{}, ErrAttachUnsupported
	}
	if processID <= 0 {
		return Runtime{}, ErrAttachTargetUnresolved
	}
	return m.attachRuntimeWithConfig(ctx, cfg, provider, processID, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, processID)
	})
}

func (m *Manager) attachByListenPort(ctx context.Context, cfg LaunchConfig, provider Provider, targetPort int, processID int) (Runtime, error) {
	if provider.AttachCapability() != AttachModeListen {
		return Runtime{}, ErrAttachUnsupported
	}
	if targetPort <= 0 {
		return Runtime{}, ErrAttachTargetUnresolved
	}
	cfg.TargetPort = targetPort
	return m.attachRuntimeWithConfig(ctx, cfg, provider, processID, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, processID)
	})
}

// prearmListenPort 从 deployment 运行进程的 argv 反解 debugpy `--listen host:port` 端口。
// 拿不到 argv 或 argv 不含 --listen 时返回 0（表示该进程不可 prearm attach）。
func (m *Manager) prearmListenPort(deploymentID string) int {
	if m.runningProcessArgv == nil {
		return 0
	}
	return parseListenPort(m.runningProcessArgv(deploymentID))
}

// waitInspectorPort 轮询 deployment stderr，解析 Node inspector 端口。
// Unix 路径在 SIGUSR1 后调用；Windows prearm 路径在启动时已写 stderr。
// 端口异步出现，最多等约 3s；超时返回 ErrAttachTargetUnresolved。
func (m *Manager) waitInspectorPort(deploymentID string, fallback nodeInspectorFallback) (int, error) {
	if m.runningProcessStderr == nil {
		if fallback.enabled && tcpPortOpen(fallback.port) {
			return fallback.port, nil
		}
		return 0, ErrAttachTargetUnresolved
	}
	deadline := m.now().Add(3 * time.Second)
	for {
		if port := parseInspectorPort(m.runningProcessStderr(deploymentID)); port > 0 {
			return port, nil
		}
		if fallback.enabled && tcpPortOpen(fallback.port) {
			return fallback.port, nil
		}
		if m.now().After(deadline) {
			return 0, fmt.Errorf("%w: node inspector port not found in stderr", ErrAttachTargetUnresolved)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tcpPortOpen(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// parseListenPort 在 argv 中找 `--listen host:port`（或 `--listen=host:port`）并返回端口。
func parseListenPort(argv []string) int {
	for i, arg := range argv {
		value := ""
		switch {
		case arg == "--listen" && i+1 < len(argv):
			value = argv[i+1]
		case strings.HasPrefix(arg, "--listen="):
			value = strings.TrimPrefix(arg, "--listen=")
		case strings.HasPrefix(arg, "-agentlib:jdwp=") || strings.HasPrefix(arg, "-Xrunjdwp:"):
			value = jdwpAddressValue(arg)
		default:
			continue
		}
		// value 形如 host:port 或仅 port。
		if idx := strings.LastIndex(value, ":"); idx >= 0 {
			value = value[idx+1:]
		}
		if port, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && port > 0 {
			return port
		}
	}
	return 0
}

func jdwpAddressValue(arg string) string {
	const key = "address="
	idx := strings.Index(arg, key)
	if idx < 0 {
		return ""
	}
	value := arg[idx+len(key):]
	if comma := strings.Index(value, ","); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

func (m *Manager) attachRuntimeWithConfig(ctx context.Context, cfg LaunchConfig, provider Provider, processID int, attachArgs func(LaunchConfig) map[string]any) (Runtime, error) {
	port, err := m.reserve()
	if err != nil {
		return Runtime{}, err
	}
	cfg.AdapterPort = port
	cmd, process, err := m.resolveAndLaunchAdapter(ctx, cfg, provider)
	if err != nil {
		return Runtime{}, err
	}
	dap, err := m.dialAdapter(ctx, fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		_ = closeProcessIfPresent(process.Close)
		return Runtime{}, ensureAdapterError(CodeDAPConnectionFailed, cmd, err)
	}
	if _, err := dap.Initialize(ctx); err != nil {
		_ = dap.Close()
		_ = closeProcessIfPresent(process.Close)
		return Runtime{}, err
	}
	requestSub, cancelRequestSub := subscribeJSDebugRequests(provider, dap)
	if cancelRequestSub != nil {
		defer cancelRequestSub()
	}
	runtimeDAP := dap
	rootDAP := dap
	// 仅 js-debug 拓扑需要 spawn child session；以能力查询代替语言硬编码，新语言无需改本文件。
	if provider.UsesReverseRequestChildSession() {
		runtimeDAP, err = m.runJSDebugRootAndChildHandshake(ctx, rootDAP, requestSub, port, "attach", func(handshakeCtx context.Context) error {
			return attachAndConfigure(handshakeCtx, rootDAP, attachArgs(cfg))
		})
		if err != nil {
			_ = rootDAP.Close()
			_ = closeProcessIfPresent(process.Close)
			return Runtime{}, err
		}
	} else {
		if err := attachAndConfigure(ctx, rootDAP, attachArgs(cfg)); err != nil {
			_ = rootDAP.Close()
			_ = closeProcessIfPresent(process.Close)
			return Runtime{}, err
		}
	}
	now := m.now().UTC()
	store := &debuggerSnapshotStore{}
	pump := newEventPump(runtimeDAP, store)
	record := &runtimeRecord{
		Runtime: Runtime{
			ProjectID:    cfg.Target.ProjectID,
			DeploymentID: cfg.Target.DeploymentID,
			Provider:     cfg.Provider,
			AdapterPort:  port,
			ProcessID:    processID,
			State:        RuntimeStateDebugRunning,
			Origin:       "attached",
			Alive:        true,
			CreatedAt:    now,
			LastUsedAt:   now,
		},
		rootPath:   cfg.Target.RootPath,
		sourceRoot: resolveSourceRoot(cfg.WorkingDir, cfg.Target.RootPath),
		dap:        runtimeDAP,
		debugStore: store,
		pump:       pump,
	}
	record.close = func() error {
		if record.pump != nil {
			record.pump.stop()
		}
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = runtimeDAP.Detach(dctx)
		_ = runtimeDAP.Close()
		if rootDAP != runtimeDAP {
			_ = rootDAP.Close()
		}
		return closeProcessIfPresent(process.Close)
	}
	// 回调必须在 pump.start 之前挂上；retire 需等 pump loop 退出，必须异步。
	pump.onTerminated = func() {
		go m.retireRuntime(cfg.Target.DeploymentID, record)
	}
	pump.start(context.Background())
	m.mu.Lock()
	// debuggee 在 attach 窗口内已终止：拒绝登记死 runtime。
	if !record.Alive {
		m.mu.Unlock()
		_ = closeProcessIfPresent(record.close)
		return Runtime{}, fmt.Errorf("debug runtime terminated during attach: %w", ErrRuntimeNotRunning)
	}
	m.runtimes[cfg.Target.DeploymentID] = record
	m.mu.Unlock()
	return record.Runtime, nil
}

// attachRuntimeDirect 直连 debuggee 预埋的 DAP 端口（targetPort），不另起 adapter 进程。
//
// 与 attachRuntimeWithConfig 的区别：
//   - 不 reserve 端口、不 spawn adapter（debuggee 自身即 DAP 服务，如 debugpy --listen）
//   - dial 的是 targetPort 本身；close 时只断开 DAP，绝不杀 debuggee（普通 dev 进程）
func (m *Manager) attachRuntimeDirect(ctx context.Context, cfg LaunchConfig, provider Provider, targetPort int, processID int, attachArgs func(LaunchConfig) map[string]any) (Runtime, error) {
	// debuggee 已直接暴露 DAP 协议时没有 executable 可解析；记录 not_applicable，避免
	// 观察端把“没有 adapter start 日志”误判成漏执行或 silent fallback。
	logger.GetLogger().WithEntryName("CodeDebugAdapterLaunch").WithFields(map[string]any{
		"provider":      cfg.Provider,
		"deployment_id": cfg.Target.DeploymentID,
		"source":        AdapterCommandSourceNotApplicable,
		"target_port":   targetPort,
	}).Info("direct DAP attach does not require an external adapter")
	dap, err := m.dialAdapter(ctx, fmt.Sprintf("127.0.0.1:%d", targetPort), 5*time.Second)
	if err != nil {
		return Runtime{}, ensureAdapterError(CodeDAPConnectionFailed, AdapterCommand{Provider: cfg.Provider}, err)
	}
	if _, err := dap.Initialize(ctx); err != nil {
		_ = dap.Close()
		return Runtime{}, err
	}
	if err := attachAndConfigure(ctx, dap, attachArgs(cfg)); err != nil {
		_ = dap.Close()
		return Runtime{}, err
	}
	now := m.now().UTC()
	store := &debuggerSnapshotStore{}
	pump := newEventPump(dap, store)
	record := &runtimeRecord{
		Runtime: Runtime{
			ProjectID:    cfg.Target.ProjectID,
			DeploymentID: cfg.Target.DeploymentID,
			Provider:     cfg.Provider,
			AdapterPort:  targetPort,
			ProcessID:    processID,
			State:        RuntimeStateDebugRunning,
			Origin:       "attached",
			Alive:        true,
			CreatedAt:    now,
			LastUsedAt:   now,
		},
		rootPath:   cfg.Target.RootPath,
		sourceRoot: resolveSourceRoot(cfg.WorkingDir, cfg.Target.RootPath),
		dap:        dap,
		debugStore: store,
		pump:       pump,
	}
	record.close = func() error {
		if record.pump != nil {
			record.pump.stop()
		}
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = dap.Detach(dctx)
		_ = dap.Close()
		return nil
	}
	// 回调必须在 pump.start 之前挂上；retire 需等 pump loop 退出，必须异步。
	pump.onTerminated = func() {
		go m.retireRuntime(cfg.Target.DeploymentID, record)
	}
	pump.start(context.Background())
	m.mu.Lock()
	// debuggee 在 attach 窗口内已终止：拒绝登记死 runtime。
	if !record.Alive {
		m.mu.Unlock()
		_ = closeProcessIfPresent(record.close)
		return Runtime{}, fmt.Errorf("debug runtime terminated during attach: %w", ErrRuntimeNotRunning)
	}
	m.runtimes[cfg.Target.DeploymentID] = record
	m.mu.Unlock()
	return record.Runtime, nil
}

// tryAttachRunning 在服务普通运行中且 provider 支持 pid-attach 时执行 attach。
//
// 返回 (true,nil)=已 attach；(false,nil)=不满足 attach 条件；
// (false,err)=服务在运行但 attach 失败/不支持，不应静默降级 launch。
func (m *Manager) tryAttachRunning(ctx context.Context, project model.Project, service model.Service, dep model.Deployment) (bool, error) {
	if m.runningProcess == nil {
		return false, nil
	}
	mainPID, pgid, running := m.runningProcess(dep.ID)
	if !running {
		return false, nil
	}
	providerName := ProviderForLanguage(service.Language)
	provider, err := m.providerFor(providerName)
	if err != nil {
		return false, ErrAttachUnsupported
	}
	cfg, _, err := m.attachConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})
	if err != nil {
		return false, err
	}
	req := readinessRequest{
		cfg:       cfg,
		provider:  provider,
		readiness: langruntime.ReadinessAttachPID,
		pid:       mainPID,
	}
	switch service.Language {
	case model.LanguageGo:
		pid, err := resolveGoDebuggeePID(goDebuggeeHints{
			command:          attachCommandHint(dep),
			mainPID:          mainPID,
			pgid:             pgid,
			listProcessGroup: m.listProcessGroup,
		})
		if err != nil {
			return false, err
		}
		req.pid = pid
	case model.LanguageNode:
		pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
			mainPID:          mainPID,
			pgid:             pgid,
			mainIsNode:       nodeMainProcessIsNode(dep),
			listProcessGroup: m.listProcessGroup,
		})
		if err != nil {
			return false, err
		}
		if err := m.fillNodeAttach(&req, dep, pid); err != nil {
			return false, err
		}
	case model.LanguagePython, model.LanguageJava, model.LanguageKotlin:
		// prearm-listen：Python debugpy 与 JVM JDWP 都在 start_dev 时把监听端口写入 argv。
		// attach 时从 argv 反解端口直连/交给 adapter，不发信号、不按 PID 误附加。
		// start_normal（无 listen 参数）的进程拿不到端口，按不可 attach 处理，不静默降级 launch。
		port := m.prearmListenPort(dep.ID)
		if port <= 0 {
			return false, ErrAttachUnsupported
		}
		req.readiness = langruntime.ReadinessPrearmListen
		req.port = port
	default:
		if provider.AttachCapability() != AttachModePID {
			return false, ErrAttachUnsupported
		}
	}
	if _, err := m.attachWithReadiness(ctx, req); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) attachConfig(project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (LaunchConfig, Provider, error) {
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		return m.attachConfigFromLanguageRuntime(project, service, dep, req)
	}
	return m.launchConfig(project, service, dep, req)
}

func nodeMainProcessIsNode(dep model.Deployment) bool {
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		_, escapeHatch := langruntime.EscapeHatchCommand(dep.Runtime.Config)
		if escapeHatch {
			return false
		}
		// script 主路径实际启动的是 pnpm/npm/yarn wrapper；pnpm 可能自身也是
		// node 进程，不能因为 comm=node 就把 wrapper 当成业务 debuggee。
		return langruntime.StringValue(dep.Runtime.Config["script"]) == ""
	}
	fields := stripInlineEnvFields(strings.Fields(attachCommandHint(dep)))
	if len(fields) == 0 {
		return false
	}
	return isNodeProcess(fields[0])
}

// openLeaseOnRuntime 在已存在的 debug runtime 上建 lease（不重启 debuggee）。
//
// runtime 不可复用（刚被 retire/竞态消失）时返回 ErrRuntimeNotRunning，
// 不回落到 Open：Open 走 launch 语义，会启动一个重新编译的分身进程——
// ResolveLease 承诺"attach 不可用时返回结构化错误,不静默 launch"，
// 这里静默 launch 正是 2026-07-07 分身事故的成因之一。
func (m *Manager) openLeaseOnRuntime(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, approvalToken string) (Session, bool, error) {
	_ = ctx
	_ = project
	_ = service
	_ = approvalToken
	if runtime, ok := m.reusableRuntime(dep.ID); ok {
		return m.createSessionForRuntime(runtime), true, nil
	}
	return Session{}, false, ErrRuntimeNotRunning
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

// DebuggerSnapshot 返回指定 deployment 的 debugger 实时快照。
//
// 快照由 runtime 自己的事件泵维护；这里仅取出 store 指针后在 manager 锁外读取，
// 避免 manager 生命周期锁和快照锁互相嵌套。
func (m *Manager) DebuggerSnapshot(deploymentID string) (DebuggerSnapshot, bool) {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	record, ok := m.runtimes[deploymentID]
	alive := ok && record.Alive && record.debugStore != nil
	var store *debuggerSnapshotStore
	if alive {
		store = record.debugStore
	}
	m.mu.Unlock()
	if !alive {
		return DebuggerSnapshot{}, false
	}
	return store.get(), true
}

// LeaseActive 返回指定 deployment 是否存在活跃 AI lease。
func (m *Manager) LeaseActive(deploymentID string) bool {
	_, ok := m.activeLeaseFor(deploymentID)
	return ok
}

// LeaseFor 只返回已存在的活跃 lease，不创建。
func (m *Manager) LeaseFor(deploymentID string) (Session, bool) {
	return m.activeLeaseFor(deploymentID)
}

func (m *Manager) activeLeaseFor(deploymentID string) (Session, bool) {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	closeFns := m.cleanupLocked(m.now().UTC())
	defer closeSessionFns(closeFns)
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		if session.runtimeDeploymentID == deploymentID && session.Alive && !session.Closed {
			return sessionStatus(session), true
		}
	}
	return Session{}, false
}

// retireRuntime 在 debuggee 终止（terminated/exited 事件）后反向失效 runtime。
//
// 为什么必须存在：debuggee 死亡或被重启后 adapter 会话已不可用；若只等显式
// Stop/Close 翻转 Alive，ResolveLease 会永久复用这个 Alive=true 的死 runtime，
// 后续所有调试请求都打在死连接上（2026-07-07 实测：僵尸 runtime 劫持调试 24 小时）。
//
// 仅当 map 中仍是同一 record 时才摘除——deployment 重启后新建的 runtime
// 会顶替 map 槽位，旧 record 迟到的 terminated 事件不能误杀新 runtime。
// 由 pump 的 onTerminated 回调异步触发（回调内 record.close 会等 pump loop
// 退出，必须在 loop goroutine 之外执行）。
func (m *Manager) retireRuntime(deploymentID string, record *runtimeRecord) {
	m.mu.Lock()
	// 无条件先翻转 record 自身的 Alive：若 terminated 抢在 record 入 map 之前
	// 到达（debuggee 启动即死），入 map 处会检查 Alive 并拒绝登记死 runtime。
	record.Alive = false
	current, ok := m.runtimes[deploymentID]
	if !ok || current != record {
		m.mu.Unlock()
		return
	}
	closeFn := record.close
	delete(m.runtimes, deploymentID)
	for id, session := range m.sessions {
		if session.runtimeDeploymentID != deploymentID {
			continue
		}
		session.Alive = false
		session.Closed = true
		session.Error = "debug runtime terminated: debuggee exited"
		delete(m.sessions, id)
		m.closed[id] = session.Session
	}
	m.mu.Unlock()
	log.Printf("[codedebug] debug runtime retired on terminated event deployment=%s", deploymentID)
	_ = closeProcessIfPresent(closeFn)
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
//
// lines 为空是合法请求：DAP setBreakpoints 语义是按 source 全量替换，
// 空列表即清空该文件全部断点（capture 收尾、手工解除冻结都依赖它）。
func (m *Manager) SetBreakpoints(ctx context.Context, sessionID, source string, lines []int) (map[string]any, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return nil, err
	}
	sourcePath, err := ResolveInsideRoot(runtime.sourceRoot, source)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if err := validatePositiveLine(line); err != nil {
			return nil, err
		}
	}
	result, err := runtime.dap.SetBreakpoints(ctx, sourcePath, lines)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]any{}
	}
	// 暴露解析后的绝对路径，便于诊断 js-debug "Unbound breakpoint"：
	// 当 js-debug 加载的脚本真实路径与此不一致时即为路径映射问题。
	result["resolved_source"] = sourcePath
	log.Printf("[SuperDev][codedebug] setBreakpoints session=%s source=%q resolved=%q sourceRoot=%q lines=%v", sessionID, source, sourcePath, runtime.sourceRoot, lines)
	return result, nil
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

// ContinueRuntime 对指定 deployment 的 debug runtime 直接发送 continue。
//
// 参数：
//   - ctx: 请求上下文，用于取消 DAP 操作
//   - deploymentID: 目标 deployment ID
//   - threadID: 要继续的线程 ID
//
// 返回：
//   - runtime 缺失或已停止时返回 ErrSessionNotFound
//   - DAP continue 执行失败时返回底层错误
//
// 注意：
//   - 这是用户级操作，不创建或校验 AI lease
func (m *Manager) ContinueRuntime(ctx context.Context, deploymentID string, threadID int) error {
	deploymentID = strings.TrimSpace(deploymentID)
	m.mu.Lock()
	record, ok := m.runtimes[deploymentID]
	alive := ok && record.Alive
	var dap DAP
	if alive {
		dap = record.dap
	}
	m.mu.Unlock()
	if !alive {
		return ErrSessionNotFound
	}
	return dap.Continue(ctx, threadID)
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

func attachAndConfigure(ctx context.Context, dap DAP, args map[string]any) error {
	sub, cancelSub := dap.Subscribe()
	defer cancelSub()
	attachCtx, cancelAttach := context.WithCancel(ctx)
	defer cancelAttach()
	attachErr := make(chan error, 1)
	go func() {
		attachErr <- dap.Attach(attachCtx, args)
	}()
	configurationStarted := false
	configurationCompleted := false
	var configurationErr <-chan error
	var cancelConfiguration context.CancelFunc
	startConfigurationDone := func() {
		if configurationStarted {
			return
		}
		configurationStarted = true
		configurationCtx, cancel := context.WithCancel(ctx)
		cancelConfiguration = cancel
		result := make(chan error, 1)
		configurationErr = result
		go func() {
			result <- dap.ConfigurationDone(configurationCtx)
		}()
	}
	defer func() {
		if cancelConfiguration != nil {
			cancelConfiguration()
		}
	}()
	for {
		select {
		case err := <-attachErr:
			if err != nil {
				return err
			}
			if !configurationStarted {
				// 一些旧 adapter 先返回 attach 且不发 initialized；这种时序仍同步确认响应。
				return dap.ConfigurationDone(ctx)
			}
			if !configurationCompleted {
				select {
				case err := <-configurationErr:
					if err != nil {
						return err
					}
					configurationCompleted = true
				default:
					// fwcd Kotlin DAP 0.4.4 用 configurationDone 的 pending future 作为
					// attach 栅栏，attach 成功后也不会完成该响应；成功的 attach 已证明
					// 请求被消费，此处取消本地等待，不能让真实 adapter 永久卡住。
					logger.GetLogger().WithEntryName("CodeDebugDAPHandshake").WithField("phase", "attach").Info("attach 已完成，configurationDone 响应保持 pending")
				}
			}
			return nil
		case err := <-configurationErr:
			if err != nil {
				cancelAttach()
				return err
			}
			configurationCompleted = true
			configurationErr = nil
		case event, ok := <-sub:
			if !ok {
				sub = nil
				continue
			}
			if isDAPInitializedEvent(event) {
				startConfigurationDone()
			}
		case <-ctx.Done():
			cancelAttach()
			return ctx.Err()
		}
	}
}

func awaitAttachResultBeforeConfiguration(ctx context.Context, attachErr <-chan error) (error, bool) {
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-attachErr:
		return err, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return ctx.Err(), true
	}
}

func launchAndConfigure(ctx context.Context, dap DAP, args map[string]any) error {
	sub, cancelSub := dap.Subscribe()
	defer cancelSub()
	launchCtx, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()
	launchErr := make(chan error, 1)
	go func() {
		launchErr <- dap.Launch(launchCtx, args)
	}()
	configurationDone := false
	sendConfigurationDone := func() error {
		if configurationDone {
			return nil
		}
		// js-debug 的 launch 与 attach 一样，会先发 initialized，并等 configurationDone 后才回 launch response。
		// Go/debugpy 通常先回 launch；两种时序都在这里归一。
		if err := dap.ConfigurationDone(ctx); err != nil {
			cancelLaunch()
			return err
		}
		configurationDone = true
		return nil
	}
	for {
		select {
		case err := <-launchErr:
			if err != nil {
				return err
			}
			return sendConfigurationDone()
		case event, ok := <-sub:
			if !ok {
				sub = nil
				continue
			}
			if isDAPInitializedEvent(event) {
				if err := sendConfigurationDone(); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			cancelLaunch()
			return ctx.Err()
		}
	}
}

func isDAPInitializedEvent(event map[string]any) bool {
	name, _ := event["event"].(string)
	return name == "initialized"
}

type reverseRequestDAP interface {
	SubscribeRequests() (<-chan map[string]any, func())
	RespondToRequest(context.Context, map[string]any, bool, map[string]any) error
}

func subscribeJSDebugRequests(provider Provider, dap DAP) (<-chan map[string]any, func()) {
	if provider == nil || !provider.UsesReverseRequestChildSession() {
		return nil, nil
	}
	reverse, ok := dap.(reverseRequestDAP)
	if !ok {
		return nil, nil
	}
	return reverse.SubscribeRequests()
}

type jsDebugChildHandshakeResult struct {
	dap DAP
	err error
}

func (m *Manager) runJSDebugRootAndChildHandshake(
	ctx context.Context,
	root DAP,
	requests <-chan map[string]any,
	adapterPort int,
	phase string,
	rootHandshake func(context.Context) error,
) (DAP, error) {
	// js-debug 可以在根 launch/attach 返回之前发 startDebugging，并等待客户端建立子会话。
	// 两段握手必须并发推进，否则根响应与反向请求会形成循环等待。
	handshakeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rootResult := make(chan error, 1)
	childResult := make(chan jsDebugChildHandshakeResult, 1)
	log := logger.GetLogger().WithEntryName("CodeDebugJSDebugHandshake").WithFields(map[string]any{
		"adapter_port": adapterPort,
		"phase":        phase,
	})
	log.Info("js-debug 根会话与子会话握手开始")
	go func() {
		rootResult <- rootHandshake(handshakeCtx)
	}()
	go func() {
		child, err := m.attachJSDebugChildSession(handshakeCtx, root, requests, adapterPort)
		childResult <- jsDebugChildHandshakeResult{dap: child, err: err}
	}()

	var rootDone bool
	var childDone bool
	var childSessionEstablished bool
	runtimeDAP := root
	for !rootDone || !childDone {
		select {
		case err := <-rootResult:
			rootDone = true
			if err != nil {
				cancel()
				log.WithErr(err).Error("js-debug 根会话握手失败")
				return nil, err
			}
		case result := <-childResult:
			childDone = true
			if result.err != nil {
				cancel()
				log.WithErr(result.err).Error("js-debug 子会话握手失败")
				return nil, result.err
			}
			if result.dap != nil {
				runtimeDAP = result.dap
				childSessionEstablished = true
			}
		case <-ctx.Done():
			cancel()
			log.WithErr(ctx.Err()).Error("js-debug 根会话与子会话握手超时")
			return nil, ctx.Err()
		}
	}
	log.WithField("child_session", childSessionEstablished).Info("js-debug 根会话与子会话握手完成")
	return runtimeDAP, nil
}

func (m *Manager) attachJSDebugChildSession(ctx context.Context, root DAP, requests <-chan map[string]any, adapterPort int) (DAP, error) {
	if requests == nil {
		return nil, nil
	}
	reverse, ok := root.(reverseRequestDAP)
	if !ok {
		return nil, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			return nil, nil
		case request, ok := <-requests:
			if !ok {
				return nil, nil
			}
			if command, _ := request["command"].(string); command != "startDebugging" {
				continue
			}
			childRequest, childArgs, ok := jsDebugChildRequest(request)
			if !ok {
				return nil, fmt.Errorf("js-debug startDebugging request missing child configuration")
			}
			if err := reverse.RespondToRequest(ctx, request, true, map[string]any{}); err != nil {
				return nil, err
			}
			child, err := m.dialAdapter(ctx, fmt.Sprintf("127.0.0.1:%d", adapterPort), 5*time.Second)
			if err != nil {
				return nil, ensureAdapterError(CodeDAPConnectionFailed, AdapterCommand{Provider: model.CodeDebugProviderNode}, err)
			}
			if _, err := child.Initialize(ctx); err != nil {
				_ = child.Close()
				return nil, err
			}
			if err := runJSDebugChildRequest(ctx, child, childRequest, childArgs); err != nil {
				_ = child.Close()
				return nil, err
			}
			return child, nil
		}
	}
}

func runJSDebugChildRequest(ctx context.Context, child DAP, request string, args map[string]any) error {
	switch strings.TrimSpace(request) {
	case "launch":
		return launchAndConfigure(ctx, child, args)
	case "", "attach":
		return attachAndConfigure(ctx, child, args)
	default:
		return fmt.Errorf("unsupported js-debug child request %q", request)
	}
}

func jsDebugChildRequest(request map[string]any) (string, map[string]any, bool) {
	args, _ := request["arguments"].(map[string]any)
	if len(args) == 0 {
		return "", nil, false
	}
	requestType, _ := args["request"].(string)
	configuration, _ := args["configuration"].(map[string]any)
	if len(configuration) == 0 {
		return "", nil, false
	}
	out := make(map[string]any, len(configuration))
	for k, v := range configuration {
		out[k] = v
	}
	return requestType, out, true
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
	if _, err := m.validateCaptureSource(req.SessionID, req.Source); err != nil {
		return nil, err
	}
	_, runtime, err := m.sessionRuntime(req.SessionID)
	if err != nil {
		return nil, err
	}
	runtimeProvider, err := m.providerFor(runtime.Provider)
	if err != nil {
		return nil, err
	}
	// fwcd Kotlin DAP 与 js-debug 都不接受没有真实线程 identity 的主动 pause；
	// 由 provider 能力决定是否先暂停，避免把 adapter 差异硬编码成语言分支。
	pauseBeforeCapture := shouldPauseBeforeCapture(runtimeProvider)
	wasPaused := runtime.debugStore != nil && runtime.debugStore.get().State == "paused"
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
	pausedByCapture := false
	if pauseBeforeCapture {
		pausedByCapture, err = m.pauseForCaptureIfRunning(waitCtx, req.SessionID, req.ThreadID)
		if err != nil {
			return nil, err
		}
	}
	stoppedSub, cancelStopped := runtime.dap.Subscribe()
	defer cancelStopped()
	breakpoints, err := m.SetBreakpoints(ctx, req.SessionID, req.Source, []int{req.Line})
	if err != nil {
		m.resumeCapturePause(req.SessionID, req.ThreadID, pausedByCapture)
		return nil, err
	}
	// 无论命中、超时还是采集失败，退出前都清掉本次断点：残留断点会让后续
	// 命中它的请求把 debuggee 永久挂起（服务对外表现为冻结），而 capture
	// 已经返回，没有任何调用方会来 continue。暂停现场不受影响，仍可 inspect。
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, clearErr := m.SetBreakpoints(cctx, req.SessionID, req.Source, nil); clearErr != nil {
			log.Printf("[codedebug] clear capture breakpoints failed session=%s source=%s: %v", req.SessionID, req.Source, clearErr)
		}
	}()
	if err := ensureCaptureBreakpointVerified(breakpoints, req.Line); err != nil {
		m.resumeCapturePause(req.SessionID, req.ThreadID, pausedByCapture)
		return nil, err
	}
	// 只有本次 capture 主动暂停，或进入 capture 前已经暂停，才需要 continue。
	// 对正在运行且不支持 threadless pause 的 adapter，断点设置后直接等待命中。
	if pausedByCapture || wasPaused {
		threadID := runtime.debugStore.get().ThreadID
		if threadID == 0 {
			threadID = req.ThreadID
		}
		if err := m.threadContinue(ctx, req.SessionID, threadID); err != nil && !isAlreadyRunningDAPError(err) {
			return nil, err
		}
	}
	stopped, err := waitForStoppedEvent(waitCtx, stoppedSub)
	if err != nil {
		return nil, fmt.Errorf("wait for capture breakpoint failed: %w", err)
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
	if !IsSupportedTarget(dep) {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		return m.launchConfigFromLanguageRuntime(project, service, dep, req)
	}
	return LaunchConfig{}, nil, ErrTargetUnsupported
}

// launchConfigFromLanguageRuntime 由语言 provider 的 debug_launch plan 构造调试启动配置。
//
// 同源原则：cwd/env/program 全部来自 runtime 配置，code_debug 仅保留
// policy/stop_on_entry/adapter override 等调试特有项，不再读 code_debug.program。
func (m *Manager) launchConfigFromLanguageRuntime(project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (LaunchConfig, Provider, error) {
	providerName := ProviderForLanguage(service.Language)
	debugProvider, err := m.providerFor(providerName)
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	runtimeProvider, ok := langruntime.Core().Provider(service.Language)
	if !ok {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	var code model.CodeDebugConfig
	if dep.CodeDebug != nil {
		code = *dep.CodeDebug
	}
	stopOnEntry := code.StopOnEntry
	if req.StopOnEntry != nil {
		stopOnEntry = *req.StopOnEntry
	}
	ctx := context.Background()
	normalized, diagnostics, err := runtimeProvider.Normalize(ctx, langruntime.RuntimeConfigInput{
		ProjectRoot: project.RootPath,
		CWD:         dep.Runtime.EffectiveCWD(),
		Env:         dep.Runtime.EffectiveEnv(),
		Config:      dep.Runtime.Config,
	})
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	if langruntime.HasErrorDiagnostic(diagnostics) {
		return LaunchConfig{}, nil, ErrConfigInvalid
	}
	plan, diagnostics, err := runtimeProvider.BuildPlan(ctx, langruntime.BuildPlanInput{
		Intent:      langruntime.IntentDebugLaunch,
		Config:      normalized,
		StopOnEntry: stopOnEntry,
	})
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	if langruntime.HasErrorDiagnostic(diagnostics) || plan.Debug == nil {
		return LaunchConfig{}, nil, ErrConfigInvalid
	}
	cfg := LaunchConfig{
		Target: Target{
			ProjectID:    project.ID,
			ProjectName:  project.Name,
			RootPath:     project.RootPath,
			ServiceID:    service.ID,
			ServiceName:  service.Name,
			DeploymentID: dep.ID,
			EnvName:      dep.EnvName,
			Language:     service.Language,
			Provider:     providerName,
			Experimental: providerIsExperimental(providerName),
			WorkDir:      plan.WorkingDir,
		},
		Provider:       providerName,
		Program:        plan.Debug.Program,
		Args:           append([]string{}, plan.Debug.Args...),
		WorkingDir:     plan.WorkingDir,
		Env:            plan.Env,
		AdapterCommand: strings.TrimSpace(code.AdapterCommand),
		AdapterArgs:    append([]string{}, code.AdapterArgs...),
		StopOnEntry:    plan.Debug.StopOnEntry,
	}
	return cfg, debugProvider, nil
}

// attachConfigFromLanguageRuntime 构造运行中 language runtime 的 attach 配置。
//
// script-based Node 只能 attach 到已经运行的子进程；它没有 debug_launch 所需的
// program 入口，但 listen attach 只依赖 cwd/env/provider 和 inspector 端口。
func (m *Manager) attachConfigFromLanguageRuntime(project model.Project, service model.Service, dep model.Deployment, req OpenRequest) (LaunchConfig, Provider, error) {
	providerName := ProviderForLanguage(service.Language)
	debugProvider, err := m.providerFor(providerName)
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	runtimeProvider, ok := langruntime.Core().Provider(service.Language)
	if !ok {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	var code model.CodeDebugConfig
	if dep.CodeDebug != nil {
		code = *dep.CodeDebug
	}
	stopOnEntry := code.StopOnEntry
	if req.StopOnEntry != nil {
		stopOnEntry = *req.StopOnEntry
	}
	ctx := context.Background()
	normalized, diagnostics, err := runtimeProvider.Normalize(ctx, langruntime.RuntimeConfigInput{
		ProjectRoot: project.RootPath,
		CWD:         dep.Runtime.EffectiveCWD(),
		Env:         dep.Runtime.EffectiveEnv(),
		Config:      dep.Runtime.Config,
	})
	if err != nil {
		return LaunchConfig{}, nil, err
	}
	if langruntime.HasErrorDiagnostic(diagnostics) {
		return LaunchConfig{}, nil, ErrConfigInvalid
	}
	program, args := attachDebugProgram(ctx, runtimeProvider, normalized, stopOnEntry, debugProvider.AttachCapability())
	return LaunchConfig{
		Target: Target{
			ProjectID:    project.ID,
			ProjectName:  project.Name,
			RootPath:     project.RootPath,
			ServiceID:    service.ID,
			ServiceName:  service.Name,
			DeploymentID: dep.ID,
			EnvName:      dep.EnvName,
			Language:     service.Language,
			Provider:     providerName,
			Experimental: providerIsExperimental(providerName),
			WorkDir:      normalized.CWD,
		},
		Provider:       providerName,
		Program:        program,
		Args:           args,
		WorkingDir:     normalized.CWD,
		Env:            normalized.Env,
		AdapterCommand: strings.TrimSpace(code.AdapterCommand),
		AdapterArgs:    append([]string{}, code.AdapterArgs...),
		StopOnEntry:    stopOnEntry,
	}, debugProvider, nil
}

func attachDebugProgram(ctx context.Context, provider langruntime.Provider, normalized langruntime.NormalizedRuntimeConfig, stopOnEntry bool, attachMode AttachMode) (string, []string) {
	if attachMode != AttachModePID {
		return "", nil
	}
	plan, diagnostics, err := provider.BuildPlan(ctx, langruntime.BuildPlanInput{
		Intent:      langruntime.IntentDebugLaunch,
		Config:      normalized,
		StopOnEntry: stopOnEntry,
	})
	if err != nil || langruntime.HasErrorDiagnostic(diagnostics) || plan.Debug == nil {
		return "", nil
	}
	return plan.Debug.Program, append([]string{}, plan.Debug.Args...)
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

func (m *Manager) validateCaptureSource(sessionID string, source string) (string, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return "", err
	}
	return ResolveInsideRoot(runtime.sourceRoot, source)
}

// resolveSourceRoot 计算断点源码的解析基准目录。
//
// 优先用已解析的工作目录（language runtime 的 cwd 是子目录，源码相对 cwd）；
// 为空时回退项目根。并对结果做 symlink 规范化，使其与编译产物 DWARF 里记录的
// 真实路径一致（macOS 下 /tmp 是 /private/tmp 的 symlink，go build 写真实路径，
// 而 dlv 按字符串匹配源码路径，不规范化会导致 "could not find file"）。
func resolveSourceRoot(workingDir, rootPath string) string {
	root := strings.TrimSpace(workingDir)
	if root == "" {
		root = strings.TrimSpace(rootPath)
	}
	if root == "" {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

func (m *Manager) pauseForCaptureIfRunning(ctx context.Context, sessionID string, threadID int) (bool, error) {
	_, runtime, err := m.sessionRuntime(sessionID)
	if err != nil {
		return false, err
	}
	if runtime.debugStore != nil && runtime.debugStore.get().State == "paused" {
		return false, nil
	}
	stoppedSub, cancelStopped := runtime.dap.Subscribe()
	defer cancelStopped()
	if err := runtime.dap.Pause(ctx, threadID); err != nil && !isAlreadyPausedDAPError(err) {
		return false, err
	}
	if _, err := waitForStoppedEvent(ctx, stoppedSub); err != nil && !isAlreadyPausedDAPError(err) {
		return false, fmt.Errorf("wait for capture pause failed: %w", err)
	}
	return true, nil
}

func (m *Manager) resumeCapturePause(sessionID string, threadID int, pausedByCapture bool) {
	if !pausedByCapture {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.threadContinue(ctx, sessionID, threadID)
}

func waitForStoppedEvent(ctx context.Context, sub <-chan map[string]any) (map[string]any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-sub:
			if !ok {
				return nil, fmt.Errorf("dap subscription closed")
			}
			if body, ok := stoppedBody(event); ok {
				return body, nil
			}
		}
	}
}

func ensureCaptureBreakpointVerified(result map[string]any, line int) error {
	breakpoints := asMapSlice(result["breakpoints"])
	if len(breakpoints) == 0 {
		return nil
	}
	for _, breakpoint := range breakpoints {
		verified, hasVerified := breakpoint["verified"].(bool)
		if !hasVerified || verified {
			return nil
		}
		message, _ := breakpoint["message"].(string)
		if message != "" {
			return fmt.Errorf("breakpoint line %d unverified: %s", line, message)
		}
		return fmt.Errorf("breakpoint line %d unverified", line)
	}
	return nil
}

func isAlreadyRunningDAPError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "debuggee is running") || strings.Contains(message, "already running")
}

func isAlreadyPausedDAPError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already paused") || strings.Contains(message, "already stopped")
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

func (m *Manager) providerFor(provider model.CodeDebugProvider) (Provider, error) {
	switch provider {
	case model.CodeDebugProviderGo:
		return NewGoProvider(), nil
	case model.CodeDebugProviderPython:
		return NewPythonProvider("python3"), nil
	case model.CodeDebugProviderNode:
		return NewNodeProvider(m.jsDebugServerPath), nil
	case model.CodeDebugProviderNative:
		return NewNativeDebugProvider(""), nil
	case model.CodeDebugProviderJVM:
		return NewJVMDebugProvider(""), nil
	default:
		return nil, ErrTargetUnsupported
	}
}

// resolveAndLaunchAdapter 解析并启动一次外部 DAP adapter。
//
// 参数：
//   - ctx: 控制启动阶段取消
//   - cfg: deployment 的 adapter 配置、环境和工作目录
//   - provider: 生成语言固定参数并调用统一 executable resolver 的 provider
//
// 返回：
//   - 已解析的命令与进程句柄
//   - 解析或启动失败时返回保留 provider、source 和 executable identity 的稳定错误
//
// 注意：
//   - 一旦 resolver 选中候选，启动失败必须原样返回；这里绝不尝试低优先级候选。
func (m *Manager) resolveAndLaunchAdapter(ctx context.Context, cfg LaunchConfig, provider Provider) (AdapterCommand, AdapterProcess, error) {
	cmd, err := provider.AdapterCommand(cfg)
	if err != nil {
		fields := map[string]any{
			"provider":      cfg.Provider,
			"deployment_id": cfg.Target.DeploymentID,
		}
		if info, ok := AdapterErrorDetails(err); ok {
			fields["cause_code"] = info.CauseCode
		}
		logger.GetLogger().WithEntryName("CodeDebugAdapterLaunch").WithErr(err).WithFields(fields).Error("debug adapter command resolution failed")
		return AdapterCommand{}, AdapterProcess{}, err
	}
	log := logger.GetLogger().WithEntryName("CodeDebugAdapterLaunch").WithFields(map[string]any{
		"provider":            cmd.Provider,
		"deployment_id":       cfg.Target.DeploymentID,
		"source":              cmd.Source,
		"executable_identity": adapterExecutableIdentity(cmd.Name),
	})
	log.Info("debug adapter process starting")
	process, err := m.launch(ctx, cmd)
	if err != nil {
		stableErr := ensureAdapterError(CodeAdapterStartFailed, cmd, err)
		causeCode := AdapterCauseStartFailed
		if info, ok := AdapterErrorDetails(stableErr); ok {
			causeCode = info.CauseCode
		}
		// Error() 继续只暴露稳定 code/source/executable；底层 cause 仅以不可逆分类进入日志，
		// 既能区分安装缺失与权限问题，也不会把绝对路径、参数或环境变量写入日志。
		log.WithErr(stableErr).WithField("cause_code", causeCode).Error("debug adapter process start failed")
		return cmd, AdapterProcess{}, stableErr
	}
	log.WithField("pid", process.PID).Info("debug adapter process started")
	return cmd, process, nil
}

// providerIsExperimental 报告该调试 provider 是否为实验性档位。
//
// 实验性 = 不开箱即用、需用户自备 / 配置 adapter 才能调试：
//   - Node：依赖打包的 js-debug，且 script 模式 attach 行为仍在收敛
//   - JVM：官方 java-debug 是 JDT LS plugin，没有 standalone adapter，
//     必须由用户显式配置或在 PATH 提供符合端口合同的 jvm-dap-wrapper
//
// 以能力归类代替语言硬编码：新增「需自备 adapter」的语言只需在此登记，
// list_code_debug_targets 不必再叠 == ProviderX 判断。
func providerIsExperimental(provider model.CodeDebugProvider) bool {
	switch provider {
	case model.CodeDebugProviderNode, model.CodeDebugProviderJVM:
		return true
	default:
		return false
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
	//
	// adapter 命令（如 python3 -m debugpy.adapter / dlv dap）必须用补齐并叠加
	// deployment runtime.env 后的 PATH 解析可执行：Go exec.Command 在构造时即用
	// agent 自身 PATH 做 LookPath，之后设 c.Env 不会改变已选中的二进制，会导致
	// 解析到缺少 debugpy 的系统 python3，adapter 起不来、DAP 连接被拒。
	adapterEnv := execenv.Build(execenv.Options{WorkDir: cmd.WorkDir, Overrides: cmd.Env})
	exe, lookErr := execenv.LookPath(cmd.Name, adapterEnv)
	if lookErr != nil {
		return AdapterProcess{}, NewAdapterError(CodeAdapterUnavailable, cmd, lookErr)
	}
	c := exec.Command(exe, cmd.Args...)
	if strings.TrimSpace(cmd.WorkDir) != "" {
		c.Dir = strings.TrimSpace(cmd.WorkDir)
	}
	c.Env = adapterEnv
	// adapter 自成进程组：dlv dap 会派生 debugserver/debuggee，Close 必须能
	// 整组回收，否则残留孤儿（2026-07-07 实测三件套残留 24 小时）。
	setAdapterProcessGroup(c)
	if err := c.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
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
				return killAdapterProcessTree(c.Process.Pid)
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

// attachCommandHint 返回用于 debuggee PID 解析的命令 hint。
func attachCommandHint(dep model.Deployment) string {
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		// build+exec：主进程直接是编译产物二进制，不再有 go 驱动子进程；
		// 返回空让 resolveGoDebuggeePID 走"直接可执行"分支（mainPID 即 debuggee）。
		return ""
	}
	return debugDeploymentCommand(dep)
}

func debugDeploymentWorkDir(dep model.Deployment) string {
	if dep.Runtime != nil {
		if dep.Runtime.Type == model.RuntimeTypeLanguage && strings.TrimSpace(dep.Runtime.EffectiveCWD()) != "" {
			return strings.TrimSpace(dep.Runtime.EffectiveCWD())
		}
		if strings.TrimSpace(dep.Runtime.WorkingDir) != "" {
			return strings.TrimSpace(dep.Runtime.WorkingDir)
		}
	}
	return strings.TrimSpace(dep.WorkDir)
}

func reserveLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
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
		if id, ok := dapNumericIdentity(frame["id"]); ok {
			return id
		}
	}
	return 0
}

func dapNumericIdentity(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		converted := int(typed)
		return converted, typed == float64(converted)
	default:
		return 0, false
	}
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
