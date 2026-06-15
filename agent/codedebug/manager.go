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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
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
		runtimes:             map[string]*runtimeRecord{},
		sessions:             map[string]*sessionRecord{},
		closed:               map[string]Session{},
	}
}

func signalProcessOS(pid int, signal string) error {
	return syscall.Kill(pid, signalToSyscall(signal))
}

func signalToSyscall(signal string) syscall.Signal {
	switch strings.TrimSpace(strings.ToUpper(signal)) {
	case "SIGUSR1", "USR1":
		return syscall.SIGUSR1
	case "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	default:
		return syscall.SIGUSR1
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
	if runtime, ok := m.RuntimeStatus(deploymentID); ok && runtime.Alive {
		return m.openLeaseOnRuntime(ctx, project, service, dep, approvalToken)
	}
	if attached, err := m.tryAttachRunning(ctx, project, service, dep); err != nil {
		return Session{}, false, err
	} else if attached {
		return m.openLeaseOnRuntime(ctx, project, service, dep, approvalToken)
	}
	return Session{}, false, ErrRuntimeNotRunning
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
	store := &debuggerSnapshotStore{}
	pump := newEventPump(dap, store)
	pump.start(context.Background())
	if cfg.StopOnEntry {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := dap.WaitForStopped(waitCtx)
		cancel()
		if err != nil {
			pump.stop()
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
			Origin:       "launched",
			Alive:        true,
			CreatedAt:    now,
			LastUsedAt:   now,
		},
		rootPath:   cfg.Target.RootPath,
		sourceRoot: resolveSourceRoot(cfg.WorkingDir, cfg.Target.RootPath),
		dap:        dap,
		debugStore: store,
		pump:       pump,
		close: func() error {
			pump.stop()
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
		return m.attachByListenPort(ctx, req.cfg, req.provider, req.port, req.pid)
	default:
		return m.attachByPID(ctx, req.cfg, req.provider, req.pid)
	}
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

// waitInspectorPort 在 SIGUSR1 后轮询 deployment stderr，解析 Node inspector 端口。
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

func (m *Manager) attachRuntimeWithConfig(ctx context.Context, cfg LaunchConfig, provider Provider, processID int, attachArgs func(LaunchConfig) map[string]any) (Runtime, error) {
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
	requestSub, cancelRequestSub := subscribeJSDebugRequests(cfg.Provider, dap)
	if cancelRequestSub != nil {
		defer cancelRequestSub()
	}
	if err := attachAndConfigure(ctx, dap, attachArgs(cfg)); err != nil {
		_ = dap.Close()
		_ = closeProcessIfPresent(process.Close)
		return Runtime{}, err
	}
	runtimeDAP := dap
	rootDAP := dap
	if cfg.Provider == model.CodeDebugProviderNode {
		childDAP, err := m.attachJSDebugChildSession(ctx, rootDAP, requestSub, port)
		if err != nil {
			_ = rootDAP.Close()
			_ = closeProcessIfPresent(process.Close)
			return Runtime{}, err
		}
		if childDAP != nil {
			runtimeDAP = childDAP
		}
	}
	now := m.now().UTC()
	store := &debuggerSnapshotStore{}
	pump := newEventPump(runtimeDAP, store)
	pump.start(context.Background())
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
	m.mu.Lock()
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
		req.pid = pid
		defaultInspectorAlreadyOpen := tcpPortOpen(defaultNodeInspectorPort)
		if err := m.signalProcess(pid, "SIGUSR1"); err != nil {
			return false, fmt.Errorf("signal node debuggee: %w", err)
		}
		inspectorPort, perr := m.waitInspectorPort(dep.ID, nodeInspectorFallback{
			port:    defaultNodeInspectorPort,
			enabled: !defaultInspectorAlreadyOpen,
		})
		if perr != nil {
			return false, perr
		}
		req.readiness = langruntime.ReadinessPrearmListen
		req.port = inspectorPort
	case model.LanguagePython:
		// prearm-listen：进程以 `python -m debugpy --listen host:port` 常驻，端口写在 argv 里。
		// 从 argv 反解端口直连，不发信号、不需独立端口存储。start_normal（无 --listen）的
		// Python 进程拿不到端口，按不可 attach 处理，不静默降级 launch。
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
func (m *Manager) openLeaseOnRuntime(ctx context.Context, project model.Project, service model.Service, dep model.Deployment, approvalToken string) (Session, bool, error) {
	if runtime, ok := m.RuntimeStatus(dep.ID); ok && runtime.Alive {
		return m.createSessionForRuntime(runtime), true, nil
	}
	session, err := m.Open(ctx, project, service, dep, OpenRequest{
		DeploymentID:  strings.TrimSpace(dep.ID),
		ApprovalToken: approvalToken,
	})
	if err != nil {
		return Session{}, false, err
	}
	return session, true, nil
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
	sourcePath, err := ResolveInsideRoot(runtime.sourceRoot, source)
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
	configurationDone := false
	sendConfigurationDone := func() error {
		if configurationDone {
			return nil
		}
		// js-debug 在 attach 后先发 initialized，并等 configurationDone 后才回 attach response。
		// 旧 adapter 通常先回 attach；两种时序都在这里归一。
		if err := dap.ConfigurationDone(ctx); err != nil {
			cancelAttach()
			return err
		}
		configurationDone = true
		return nil
	}
	for {
		select {
		case err := <-attachErr:
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
			cancelAttach()
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

func subscribeJSDebugRequests(provider model.CodeDebugProvider, dap DAP) (<-chan map[string]any, func()) {
	if provider != model.CodeDebugProviderNode {
		return nil, nil
	}
	reverse, ok := dap.(reverseRequestDAP)
	if !ok {
		return nil, nil
	}
	return reverse.SubscribeRequests()
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
			attachArgs, ok := jsDebugChildAttachArguments(request)
			if !ok {
				return nil, fmt.Errorf("js-debug startDebugging request missing child attach configuration")
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
			if err := attachAndConfigure(ctx, child, attachArgs); err != nil {
				_ = child.Close()
				return nil, err
			}
			return child, nil
		}
	}
}

func jsDebugChildAttachArguments(request map[string]any) (map[string]any, bool) {
	args, _ := request["arguments"].(map[string]any)
	if len(args) == 0 {
		return nil, false
	}
	configuration, _ := args["configuration"].(map[string]any)
	if len(configuration) == 0 {
		return nil, false
	}
	out := make(map[string]any, len(configuration))
	for k, v := range configuration {
		out[k] = v
	}
	return out, true
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
	nodeCapture := runtime.Provider == model.CodeDebugProviderNode
	nodeWasPaused := nodeCapture && runtime.debugStore != nil && runtime.debugStore.get().State == "paused"
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
	if !nodeCapture {
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
	if err := ensureCaptureBreakpointVerified(breakpoints, req.Line); err != nil {
		m.resumeCapturePause(req.SessionID, req.ThreadID, pausedByCapture)
		return nil, err
	}
	if !nodeCapture {
		if err := m.threadContinue(ctx, req.SessionID, req.ThreadID); err != nil && !isAlreadyRunningDAPError(err) {
			return nil, err
		}
	} else if nodeWasPaused {
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
	var code model.CodeDebugConfig
	if dep.CodeDebug != nil {
		code = *dep.CodeDebug
	}
	if code.Mode != "" && code.Mode != model.CodeDebugModeLaunch {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	providerName := ProviderForLanguage(service.Language)
	command := debugDeploymentCommand(dep)
	if providerName == "" {
		return LaunchConfig{}, nil, ErrTargetUnsupported
	}
	provider, err := m.providerFor(providerName)
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
		Language:     service.Language,
		Provider:     providerName,
		Experimental: providerName == model.CodeDebugProviderNode,
		Command:      command,
		WorkDir:      debugDeploymentWorkDir(dep),
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
			Experimental: providerName == model.CodeDebugProviderNode,
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
			Experimental: providerName == model.CodeDebugProviderNode,
			WorkDir:      normalized.CWD,
		},
		Provider:       providerName,
		WorkingDir:     normalized.CWD,
		Env:            normalized.Env,
		AdapterCommand: strings.TrimSpace(code.AdapterCommand),
		AdapterArgs:    append([]string{}, code.AdapterArgs...),
		StopOnEntry:    stopOnEntry,
	}, debugProvider, nil
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
