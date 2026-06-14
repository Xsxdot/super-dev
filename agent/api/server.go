// Package api 提供 SuperDev agent 的 HTTP REST API 和 WebSocket 日志流服务。
//
// 职责：
//   - 暴露项目管理接口（列表、添加、删除、规则读写）
//   - 暴露服务列表与 deployment 级启停接口
//   - 暴露日志查询接口（REST 分页 + WebSocket 实时推送）
//   - 暴露本机排障会话接口，供 MCP 与用户共享诊断上下文
//   - 生命周期管理：启动时从注册表加载已注册项目
//
// 边界：
//   - 不直接持久化日志，日志由 logbuf.Buffer → store.Store 异步写入
//   - 不持有进程的直接引用，通过 process.Manager 间接管理
//   - ID 分配（UUID）在此层完成，config.Loader 不负责 ID
package api

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/browserdebug"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/debugsession"
	"github.com/xsxdot/super-dev/agent/identity"
	"github.com/xsxdot/super-dev/agent/ingress"
	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/onboarding"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/process"
	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/remoteexec"
	"github.com/xsxdot/super-dev/agent/security"
	"github.com/xsxdot/super-dev/agent/store"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// AppConfig 包含创建 App 所需的配置参数。
type AppConfig struct {
	// DataDir 是存放数据库文件和注册表文件的根目录。
	DataDir string
	// ProbeOverride 仅用于测试,生产环境为 nil 时使用 SystemProbe。
	ProbeOverride collector.Probe
	// NodeTransportOverride 注入自定义节点传输,仅用于测试。
	NodeTransportOverride nodetransport.NodeTransport
	// NodeRegistryOverride 注入自定义节点状态中心，仅用于测试。
	NodeRegistryOverride *noderegistry.Registry
	// InstallBinaryDir 是远端 agent 安装二进制目录；为空时安装接口返回明确错误。
	InstallBinaryDir string
	// SampleBinaryPath 是随桌面端打包的 onboarding 示例服务二进制；为空时跳过示例落地。
	SampleBinaryPath string
	// InstallerOverride 注入自定义远端 agent 安装器，仅用于测试。
	InstallerOverride HostAgentInstaller
	// RuntimeMetricsSampler 注入进程级指标采样器；nil 时使用 metrics.NewSampler。
	RuntimeMetricsSampler metrics.MetricsSampler
	// RuntimeStatusRequestTimeout 是一次聚合的整体超时；0 时使用 3 秒。
	RuntimeStatusRequestTimeout time.Duration
	// ExecutionAuthorizer 注入 /ws/exec 命令授权器；nil 时使用 remoteexec.AllowAll。
	ExecutionAuthorizer remoteexec.Authorizer
	// ManagedDeploymentReconcileInterval 控制桌面端推送 remote deployment 期望状态的周期；0 时使用 30 秒。
	ManagedDeploymentReconcileInterval time.Duration
	// DebugBrowserCandidates 注入本机浏览器探测候选，仅用于测试或嵌入式发行定制。
	DebugBrowserCandidates []browserdebug.BrowserCandidate
	// CodeDebugManagerOverride 注入代码调试 manager，仅用于测试。
	CodeDebugManagerOverride *codedebug.Manager
	// BootstrapToken 是远端 agent 首次安全自举的一次性 token。
	BootstrapToken string
	// RequireAuth 控制 agent API 是否必须完成安全自举后才允许访问。
	RequireAuth bool
	// TLSCertFile 是用户手动配置的 HTTPS 服务端证书路径；必须与 TLSKeyFile 同时提供。
	TLSCertFile string
	// TLSKeyFile 是用户手动配置的 HTTPS 服务端私钥路径；必须与 TLSCertFile 同时提供。
	TLSKeyFile string
}

// HostAgentInstaller 安装、重启或原地更新远端 SuperDev agent。
type HostAgentInstaller interface {
	// Install 使用 Host SSH 凭据和 Agent 服务参数执行安装。
	Install(ctx context.Context, host model.Host, opts installer.ServiceOptions) (installer.Result, error)
	// Restart 使用 Host SSH 凭据重启远端 Agent 服务。
	Restart(ctx context.Context, host model.Host) (installer.RestartResult, error)
	// UpdateBinary 使用 Host SSH 凭据原地替换远端 Agent 二进制并重启服务。
	UpdateBinary(ctx context.Context, host model.Host) (installer.UpdateResult, error)
}

type productionHostAgentInstaller struct {
	installer *installer.Installer
}

// storeWriter 把 store.Store 适配成 logbuf 的 ctx-aware 写入接口。
type storeWriter struct {
	s *store.Store
}

func (w storeWriter) AppendBatch(_ context.Context, entries []model.LogEntry) error {
	return w.s.AppendBatch(entries)
}

func (p productionHostAgentInstaller) Install(ctx context.Context, host model.Host, opts installer.ServiceOptions) (installer.Result, error) {
	return p.installer.InstallWithOptions(ctx, host, opts)
}

func (p productionHostAgentInstaller) Restart(ctx context.Context, host model.Host) (installer.RestartResult, error) {
	return p.installer.Restart(ctx, host)
}

func (p productionHostAgentInstaller) UpdateBinary(ctx context.Context, host model.Host) (installer.UpdateResult, error) {
	return p.installer.UpdateBinary(ctx, host)
}

// App 是 HTTP API 服务的核心结构，持有所有运行时状态。
type App struct {
	cfg                    AppConfig
	mu                     sync.RWMutex
	projects               []model.Project
	managers               map[string]*process.Manager // projectID → manager
	buf                    *logbuf.Buffer
	store                  *store.Store
	registry               *config.Registry
	settings               *config.SettingsStore
	procMgr                *process.Manager // 远端 collector 复用的进程管理器
	collector              *collector.Manager
	managedStore           *ManagedStore
	managedProjectIDs      map[string]struct{}
	managedStatus          model.ManagedDeploymentStatus
	managedReconciler      *HostDeploymentReconciler
	managedReconcileCancel context.CancelFunc
	processReconcileCancel context.CancelFunc
	logCleanupCancel       context.CancelFunc
	logCleanupDone         <-chan struct{}
	// nodeStatusPublishers 保存当前 /ws/node-status 连接，供 managed 状态变化即时推送。
	nodeStatusPublisherMu sync.Mutex
	nodeStatusPublishers  map[*nodeStatusPublisher]struct{}
	remoteStore           *remote.Store
	agentStore            *remote.AgentStore
	tunnels               *tunnel.Manager
	// debugSessions 持久化本机排障记录，供 MCP 与用户共享诊断上下文。
	debugSessions debugsession.Store
	// operationApprovals 持久化 MCP 写操作审批请求和一次性 token 状态。
	operationApprovals operation.ApprovalStore
	// operationAudit 持久化 MCP 写操作安全链路审计事件。
	operationAudit operation.AuditStore
	// operationGrace 持久化项目级审批豁免窗口。
	operationGrace operation.GraceStore
	// browserDebug 管理由 SuperDev 创建的本机浏览器调试会话。
	browserDebug *browserdebug.Manager
	// codeDebug 管理由 SuperDev 创建的本机代码调试会话。
	codeDebug *codedebug.Manager
	// debugBrowserCandidates 保存本机浏览器自动探测候选。
	debugBrowserCandidates []browserdebug.BrowserCandidate
	// browserControl 通过 Playwright 控制已创建的浏览器调试会话。
	browserControl browsercontrol.Controller
	// securityStore 持久化 agent 安全自举与长期 token 状态。
	securityStore *security.Store
	// nodeTransport 统一承载按 hostID 访问远端 agent 的请求和流。
	nodeTransport nodetransport.NodeTransport
	// nodeTransportProviders 按 transport type 保存具体 provider，供 per-entry probe/provision 精确选路。
	nodeTransportProviders map[model.TransportType]nodetransport.NodeTransport
	// nodeRegistry 持有所有远端节点的最新状态快照。
	nodeRegistry *noderegistry.Registry
	// nodeRegistryCancel 停止节点状态流订阅。
	nodeRegistryCancel context.CancelFunc
	// backends 按 deployment ID 索引对应的 LogBackend。
	// 在 loadRegisteredProjects 时构造，供 deployment 日志 handler 使用。
	backends                    map[string]logbackend.LogBackend
	identity                    identity.Identity
	pidStore                    *process.PIDStore
	hostAgentInstaller          HostAgentInstaller
	runtimeMetricsSampler       metrics.MetricsSampler
	runtimeStatusRequestTimeout time.Duration
	// agentHealth 监控各 host 远端 agent 的健康状态，与隧道状态正交。
	agentHealth       *agenthealth.Monitor
	agentHealthCancel context.CancelFunc
	// agentInstallTokens 保存 generated-command 的短期安装 token hash。
	agentInstallTokenMu sync.Mutex
	agentInstallTokens  map[string]agentInstallTokenRecord
	// executionAuthorizer 在 /ws/exec 每次命令执行前进行授权。
	executionAuthorizer remoteexec.Authorizer
	// pipelineAgentRunner 仅供包内测试替换 pipeline agent 通道；nil 时使用真实 tunnel runner。
	pipelineAgentRunner pipelineRemoteTransport
	// runHub 广播 pipeline run 的实时日志与状态事件，供运行控制台 WebSocket 订阅。
	runHub *RunHub
	// ingressStore 持久化入口声明、落地状态和 DNS provider 配置。
	ingressStore ingress.Store
	// ingressRegistry 持有入口子系统的 proxy、DNS 和证书 provider。
	ingressRegistry *ingress.Registry
	// ingressService 编排入口声明的预览、落地和孤儿资源处理。
	ingressService *ingress.Service
	// ingressCertService 编排全局 SSL 证书申请、续期和部署。
	ingressCertService *ingress.CertService
	// ingressCertManager 定期续期已托管的入口证书。
	ingressCertManager *ingress.CertManager
}

// NewApp 创建并初始化 App 实例。
//
// 参数：
//   - cfg: 应用配置，DataDir 必须可写
//
// 返回：
//   - 初始化完成的 *App
//   - 打开数据库失败时返回错误
//
// 注意：
//   - 会在 DataDir 下创建 logs.db 和 projects.json
//   - logbuf.Buffer 使用 store 作为持久化后端，环形缓冲大小为 2000
func NewApp(cfg AppConfig) (*App, error) {
	dbPath := filepath.Join(cfg.DataDir, "logs.db")
	s, err := store.New(dbPath)
	if err != nil {
		return nil, err
	}

	settingsStore := config.NewSettingsStore(cfg.DataDir)
	settings, err := settingsStore.Load()
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.DeleteOlderThan(settings.LogRetentionDays); err != nil {
		_ = s.Close()
		return nil, err
	}

	id, err := identity.LoadOrCreate(cfg.DataDir)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	securityStore, err := security.NewStore(filepath.Join(cfg.DataDir, "security.json"), security.Options{
		BootstrapToken: cfg.BootstrapToken,
		RequireAuth:    cfg.RequireAuth,
	})
	if err != nil {
		_ = s.Close()
		return nil, err
	}

	buf := logbuf.New(storeWriter{s: s}, 2000, id.NodeID)
	registryPath := filepath.Join(cfg.DataDir, "projects.json")
	registry := config.NewRegistry(registryPath)
	if result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          cfg.DataDir,
		SampleBinaryPath: cfg.SampleBinaryPath,
		Registry:         registry,
		Settings:         settingsStore,
	}); err != nil {
		log.Printf("[SuperDev] sample seed skipped: %v", err)
	} else if result.Reason != "" {
		log.Printf("[SuperDev] sample seed skipped: %s", result.Reason)
	}
	procMgr := process.NewManager(buf.Append)
	probe := collector.Probe(collector.NewSystemProbe())
	if cfg.ProbeOverride != nil {
		probe = cfg.ProbeOverride
	}
	colMgr := collector.NewManager(procMgr, probe)
	remoteStore := remote.NewStore(
		filepath.Join(cfg.DataDir, "hosts.json"),
		filepath.Join(cfg.DataDir, "log_sources.json"),
	)
	agentStore := remote.NewAgentStore(filepath.Join(cfg.DataDir, "agents.json"), remoteStore)
	if err := agentStore.MigrateLegacyHostAgents(); err != nil {
		_ = s.Close()
		return nil, err
	}
	managedStore := NewManagedStore(cfg.DataDir)
	debugSessions := debugsession.NewFileStore(filepath.Join(cfg.DataDir, "debug-sessions.json"))
	operationApprovals := operation.NewApprovalFileStore(filepath.Join(cfg.DataDir, "operation-approvals.json"))
	operationAudit := operation.NewAuditFileStore(filepath.Join(cfg.DataDir, "operation-audit.json"), 5000)
	operationGrace := operation.NewGraceFileStore(filepath.Join(cfg.DataDir, "operation-grace.json"))
	browserProfileRoot := filepath.Join(cfg.DataDir, "browser-debug")
	browserTTL := time.Duration(settings.DebugBrowser.SessionTTLMinutes) * time.Minute
	_, _ = browserdebug.CleanupStaleProfiles(browserProfileRoot, browserTTL, time.Now())
	browserDebug := browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch:     browserdebug.NewChromiumLauncher(browserProfileRoot, http.DefaultClient),
		SessionTTL: browserTTL,
	})
	var app *App
	codeDebug := cfg.CodeDebugManagerOverride
	if codeDebug == nil {
		codeDebug = codedebug.NewManager(codedebug.ManagerOptions{
			SessionTTL: 30 * time.Minute,
			RunningProcess: func(deploymentID string) (int, int, bool) {
				if app == nil {
					return 0, 0, false
				}
				app.mu.RLock()
				defer app.mu.RUnlock()
				for _, mgr := range app.managers {
					if mgr != nil && mgr.IsDeploymentActive(deploymentID) {
						return mgr.DeploymentPID(deploymentID), mgr.DeploymentPGID(deploymentID), true
					}
				}
				return 0, 0, false
			},
		})
	}
	browserControl := browsercontrol.NewPlaywrightController(filepath.Join(cfg.DataDir, "playwright-driver"))
	tunnels := tunnel.NewManager(tunnel.NewSSHDialer())
	targetSource := agentTargetSource(remoteStore, agentStore)
	tunnelTransport := nodetransport.NewTunnelTransport(tunnels, targetSource)
	directTransport := nodetransport.NewDirectTransport(targetSource)
	nodeTransportProviders := map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeTunnel: tunnelTransport,
		model.TransportTypeDirect: directTransport,
	}
	appNodeTransportProviders := nodeTransportProviders
	nodeTransport := nodetransport.NodeTransport(nodetransport.NewDispatcher(targetSource, nodeTransportProviders))
	if cfg.NodeTransportOverride != nil {
		nodeTransport = cfg.NodeTransportOverride
		appNodeTransportProviders = map[model.TransportType]nodetransport.NodeTransport{}
	}
	nodeRegistry := cfg.NodeRegistryOverride
	if nodeRegistry == nil {
		nodeRegistry = noderegistry.New([]nodetransport.NodeTransport{nodeTransport}, noderegistry.Options{})
	}
	nodeRegistryCtx, nodeRegistryCancel := context.WithCancel(context.Background())
	nodeRegistry.Start(nodeRegistryCtx)
	agentHealthCtx, agentHealthCancel := context.WithCancel(context.Background())
	agentHealthMonitor := agenthealth.NewMonitor(newAgentHealthProber(nodeTransport))
	// 订阅隧道事件，桥接为 agenthealth 信号；桥接与 Run 都绑定 App 生命周期。
	tunnelEvents := tunnels.Subscribe("agent-health-monitor")
	signals := make(chan agenthealth.TunnelSignal, 64)
	go func() {
		defer close(signals)
		defer tunnels.Unsubscribe("agent-health-monitor")
		for {
			select {
			case <-agentHealthCtx.Done():
				return
			case ev, ok := <-tunnelEvents:
				if !ok {
					return
				}
				sig := agenthealth.TunnelSignal{
					HostID:    ev.HostID,
					Connected: ev.Status == tunnel.StatusConnected,
				}
				select {
				case signals <- sig:
				case <-agentHealthCtx.Done():
					return
				}
			}
		}
	}()
	go agentHealthMonitor.Run(agentHealthCtx, signals)
	runtimeSampler := cfg.RuntimeMetricsSampler
	if runtimeSampler == nil {
		runtimeSampler = metrics.NewSampler(metrics.ExecCommandExecutor{})
	}
	runtimeTimeout := cfg.RuntimeStatusRequestTimeout
	if runtimeTimeout == 0 {
		runtimeTimeout = 3 * time.Second
	}
	hostAgentInstaller := cfg.InstallerOverride
	if hostAgentInstaller == nil {
		hostAgentInstaller = productionHostAgentInstaller{installer: installer.New(installer.Options{BinaryDir: cfg.InstallBinaryDir})}
	}
	executionAuthorizer := cfg.ExecutionAuthorizer
	if executionAuthorizer == nil {
		executionAuthorizer = remoteexec.AllowAll{}
	}
	logCleanupCtx, logCleanupCancel := context.WithCancel(context.Background())
	logCleanupDone := make(chan struct{})

	app = &App{
		cfg:                         cfg,
		projects:                    []model.Project{},
		managers:                    map[string]*process.Manager{},
		buf:                         buf,
		store:                       s,
		registry:                    registry,
		settings:                    settingsStore,
		procMgr:                     procMgr,
		collector:                   colMgr,
		managedStore:                managedStore,
		managedProjectIDs:           map[string]struct{}{},
		logCleanupCancel:            logCleanupCancel,
		logCleanupDone:              logCleanupDone,
		nodeStatusPublishers:        map[*nodeStatusPublisher]struct{}{},
		remoteStore:                 remoteStore,
		agentStore:                  agentStore,
		tunnels:                     tunnels,
		debugSessions:               debugSessions,
		operationApprovals:          operationApprovals,
		operationAudit:              operationAudit,
		operationGrace:              operationGrace,
		browserDebug:                browserDebug,
		codeDebug:                   codeDebug,
		debugBrowserCandidates:      cfg.DebugBrowserCandidates,
		browserControl:              browserControl,
		securityStore:               securityStore,
		nodeTransport:               nodeTransport,
		nodeTransportProviders:      appNodeTransportProviders,
		nodeRegistry:                nodeRegistry,
		nodeRegistryCancel:          nodeRegistryCancel,
		backends:                    map[string]logbackend.LogBackend{},
		identity:                    id,
		pidStore:                    process.NewPIDStore(filepath.Join(cfg.DataDir, "pids.json")),
		hostAgentInstaller:          hostAgentInstaller,
		runtimeMetricsSampler:       runtimeSampler,
		runtimeStatusRequestTimeout: runtimeTimeout,
		agentHealth:                 agentHealthMonitor,
		agentHealthCancel:           agentHealthCancel,
		agentInstallTokens:          map[string]agentInstallTokenRecord{},
		executionAuthorizer:         executionAuthorizer,
		runHub:                      NewRunHub(),
		ingressStore:                ingress.NewFileStore(cfg.DataDir),
		ingressRegistry:             ingress.NewRegistry(),
	}
	cleaner := newLogCleaner(s, cleanupConfig{
		RetentionDays: settings.LogRetentionDays,
		MaxBytes:      settings.LogMaxBytes,
	})
	go func() {
		defer close(logCleanupDone)
		cleaner.Start(logCleanupCtx, time.Duration(settings.LogCleanupIntervalSeconds)*time.Second)
	}()
	if err := app.initIngress(agentHealthCtx); err != nil {
		app.Close()
		return nil, err
	}
	app.managedReconciler = NewHostDeploymentReconciler(app, nodeTransport, cfg.ManagedDeploymentReconcileInterval)
	return app, nil
}

func agentTargetSource(hostStore *remote.Store, agentStore *remote.AgentStore) nodetransport.TargetSource {
	return func() ([]nodetransport.NodeTarget, error) {
		hosts, err := hostStore.ListHosts()
		if err != nil {
			return nil, err
		}
		agents, err := agentStore.ListAgents()
		if err != nil {
			return nil, err
		}
		hostsByID := make(map[string]model.Host, len(hosts))
		for _, host := range hosts {
			model.ApplyHostDefaults(&host)
			hostsByID[host.ID] = host
		}
		targets := make([]nodetransport.NodeTarget, 0, len(agents))
		for _, agent := range agents {
			host, ok := hostsByID[agent.HostID]
			if !ok {
				continue
			}
			model.ApplyAgentDefaults(&agent)
			targets = append(targets, nodetransport.NodeTarget{Host: host, Agent: agent})
		}
		return targets, nil
	}
}

// Close 停止 Buffer 的 flush goroutine 并关闭 Store 数据库连接，释放所有资源。
//
// 应在 App 不再使用时调用，通常配合 defer 或测试 Cleanup 使用。
func (a *App) Close() {
	if a.nodeRegistryCancel != nil {
		a.nodeRegistryCancel()
	}
	if a.agentHealthCancel != nil {
		a.agentHealthCancel()
	}
	if a.managedReconcileCancel != nil {
		a.managedReconcileCancel()
	}
	if a.processReconcileCancel != nil {
		a.processReconcileCancel()
	}
	if a.logCleanupCancel != nil {
		a.logCleanupCancel()
	}
	if a.logCleanupDone != nil {
		<-a.logCleanupDone
	}
	if a.procMgr != nil {
		a.procMgr.StopAll()
	}
	if closer, ok := a.browserControl.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	a.buf.Close()
	if a.tunnels != nil {
		a.tunnels.Close()
	}
	if a.store != nil {
		a.store.Close()
	}
}

// Handler 构建并返回 HTTP 路由处理器。
//
// 使用 Go 1.22 的 "METHOD /path" 路由语法，支持路径参数 {id}。
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	// 项目管理
	mux.HandleFunc("GET /api/projects", a.listProjects)
	mux.HandleFunc("POST /api/projects", a.addProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.deleteProject)
	mux.HandleFunc("GET /api/projects/probe", a.probeProject)
	mux.HandleFunc("GET /api/projects/{id}/rules", a.getProjectRules)
	mux.HandleFunc("PUT /api/projects/{id}/rules", a.putProjectRules)
	mux.HandleFunc("GET /api/projects/{id}/config", a.getProjectConfig)
	mux.HandleFunc("POST /api/config-changes/preview", a.previewConfigChange)
	mux.HandleFunc("POST /api/config-changes/apply", a.applyConfigChange)
	mux.HandleFunc("GET /api/projects/{id}/vscode-launch", a.getVscodeLaunch)
	mux.HandleFunc("PUT /api/projects/{id}/setup", a.putProjectSetup)
	mux.HandleFunc("GET /api/projects/{id}/runtime-status", a.getProjectRuntimeStatus)
	mux.HandleFunc("GET /api/language-runtime/providers", a.listLanguageRuntimeProviders)
	mux.HandleFunc("GET /api/language-runtime/{language}/schema", a.describeLanguageRuntimeSchema)
	mux.HandleFunc("GET /api/nodes", a.listNodes)
	mux.HandleFunc("GET /ws/nodes", a.wsNodes)
	mux.HandleFunc("GET /ws/node-status", a.wsNodeStatus)
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)

	// Ingress 入口配置
	mux.HandleFunc("GET /api/projects/{id}/ingress", a.listProjectIngress)
	mux.HandleFunc("POST /api/projects/{id}/ingress", a.createProjectIngress)
	mux.HandleFunc("POST /api/projects/{id}/ingress/defaults", a.inferProjectIngressDefaults)
	mux.HandleFunc("GET /api/projects/{id}/ingress/{ingressID}", a.getProjectIngress)
	mux.HandleFunc("PUT /api/projects/{id}/ingress/{ingressID}", a.updateProjectIngress)
	mux.HandleFunc("DELETE /api/projects/{id}/ingress/{ingressID}", a.deleteProjectIngress)
	mux.HandleFunc("POST /api/projects/{id}/ingress/{ingressID}/preview", a.previewProjectIngress)
	mux.HandleFunc("POST /api/projects/{id}/ingress/{ingressID}/apply", a.applyProjectIngress)
	mux.HandleFunc("POST /api/projects/{id}/ingress/{ingressID}/detect-orphans", a.detectProjectIngressOrphans)
	mux.HandleFunc("POST /api/projects/{id}/ingress/{ingressID}/orphan-removals", a.removeProjectIngressOrphans)
	mux.HandleFunc("GET /api/ingress", a.listIngress)
	mux.HandleFunc("POST /api/ingress", a.upsertIngress)
	mux.HandleFunc("GET /api/ingress/certs", a.listIngressCertificates)
	mux.HandleFunc("POST /api/ingress/certs", a.createIngressCertificate)
	mux.HandleFunc("GET /api/ingress/certs/match", a.matchIngressCertificate)
	mux.HandleFunc("GET /api/ingress/certs/{id}", a.getIngressCertificate)
	mux.HandleFunc("DELETE /api/ingress/certs/{id}", a.deleteIngressCertificate)
	mux.HandleFunc("POST /api/ingress/certs/{id}/issue", a.issueIngressCertificate)
	mux.HandleFunc("POST /api/ingress/certs/{id}/renew", a.renewIngressCertificate)
	mux.HandleFunc("POST /api/ingress/certs/{id}/deploy", a.deployIngressCertificate)
	mux.HandleFunc("GET /api/ingress/acme-account", a.getIngressACMEAccount)
	mux.HandleFunc("POST /api/ingress/acme-account", a.saveIngressACMEAccount)
	mux.HandleFunc("GET /api/ingress/{id}", a.getIngress)
	mux.HandleFunc("PUT /api/ingress/{id}", a.updateIngress)
	mux.HandleFunc("DELETE /api/ingress/{id}", a.deleteIngress)
	mux.HandleFunc("POST /api/ingress/{id}/preview", a.previewIngress)
	mux.HandleFunc("POST /api/ingress/{id}/apply", a.applyIngress)
	mux.HandleFunc("POST /api/ingress/{id}/detect-orphans", a.detectIngressOrphans)
	mux.HandleFunc("POST /api/ingress/{id}/orphan-removals", a.removeIngressOrphans)
	mux.HandleFunc("GET /api/ingress/providers/dns", a.listIngressDNSProviders)
	mux.HandleFunc("POST /api/ingress/providers/dns", a.upsertIngressDNSProvider)
	mux.HandleFunc("DELETE /api/ingress/providers/dns/{id}", a.deleteIngressDNSProvider)

	// Debug sessions（本机排障记录，不修改运行态或配置）
	mux.HandleFunc("GET /api/debug-sessions", a.listDebugSessions)
	mux.HandleFunc("POST /api/debug-sessions", a.createDebugSession)
	mux.HandleFunc("GET /api/debug-sessions/{id}", a.getDebugSession)
	mux.HandleFunc("POST /api/debug-sessions/{id}/events", a.appendDebugSessionEvent)
	mux.HandleFunc("POST /api/debug-sessions/{id}/close", a.closeDebugSession)

	// Browser debug sessions（本机前端 Web entrypoint 调试）
	mux.HandleFunc("GET /api/debug-browsers", a.listDebugBrowsers)
	mux.HandleFunc("GET /api/debug-browsers/detected", a.detectDebugBrowsers)
	mux.HandleFunc("GET /api/browser-targets", a.listBrowserTargets)
	mux.HandleFunc("POST /api/browser-sessions", a.openBrowserSession)
	mux.HandleFunc("GET /api/browser-sessions", a.listBrowserSessions)
	mux.HandleFunc("GET /api/browser-sessions/{id}", a.getBrowserSession)
	mux.HandleFunc("DELETE /api/browser-sessions/{id}", a.closeBrowserSession)
	mux.HandleFunc("POST /api/browser-sessions/{id}/snapshot", a.browserSessionSnapshot)
	mux.HandleFunc("POST /api/browser-sessions/{id}/click", a.browserSessionClick)
	mux.HandleFunc("POST /api/browser-sessions/{id}/type", a.browserSessionType)
	mux.HandleFunc("POST /api/browser-sessions/{id}/screenshot", a.browserSessionScreenshot)
	mux.HandleFunc("POST /api/browser-sessions/{id}/navigate", a.browserSessionNavigate)
	mux.HandleFunc("POST /api/browser-sessions/{id}/reload", a.browserSessionReload)
	mux.HandleFunc("POST /api/browser-sessions/{id}/wait-for-selector", a.browserSessionWaitForSelector)
	mux.HandleFunc("POST /api/browser-sessions/{id}/press-key", a.browserSessionPressKey)
	mux.HandleFunc("POST /api/browser-sessions/{id}/select-option", a.browserSessionSelectOption)
	mux.HandleFunc("POST /api/browser-sessions/{id}/console-logs", a.browserSessionConsoleLogs)
	mux.HandleFunc("POST /api/browser-sessions/{id}/network-requests", a.browserSessionNetworkRequests)
	mux.HandleFunc("POST /api/browser-sessions/{id}/evaluate", a.browserSessionEvaluate)

	// Code debug sessions（本机后端代码调试）
	mux.HandleFunc("GET /api/code-debug-targets", a.listCodeDebugTargets)
	mux.HandleFunc("GET /api/code-debug-sessions", a.listCodeDebugSessions)
	mux.HandleFunc("POST /api/code-debug-sessions", a.openCodeDebugSession)
	mux.HandleFunc("GET /api/code-debug-sessions/{id}", a.getCodeDebugSession)
	mux.HandleFunc("DELETE /api/code-debug-sessions/{id}", a.closeCodeDebugSession)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/close", a.closeCodeDebugSession)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/breakpoints", a.setCodeDebugBreakpoints)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/continue", a.codeDebugContinue)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/pause", a.codeDebugPause)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/step-over", a.codeDebugStepOver)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/step-in", a.codeDebugStepIn)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/step-out", a.codeDebugStepOut)
	mux.HandleFunc("GET /api/code-debug-sessions/{id}/stack", a.codeDebugStackTrace)
	mux.HandleFunc("GET /api/code-debug-sessions/{id}/scopes", a.codeDebugScopes)
	mux.HandleFunc("GET /api/code-debug-sessions/{id}/variables", a.codeDebugVariables)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/evaluate", a.codeDebugEvaluate)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/capture-at", a.codeDebugCaptureAt)
	mux.HandleFunc("POST /api/code-debug-sessions/{id}/inspect", a.codeDebugInspect)

	// Operation safety（本机写操作预检、审批与审计）
	mux.HandleFunc("POST /api/operations/preflight", a.preflightOperation)
	mux.HandleFunc("GET /api/operation-approvals", a.listOperationApprovals)
	mux.HandleFunc("GET /api/operation-approvals/{id}", a.getOperationApproval)
	mux.HandleFunc("POST /api/operation-approvals/{id}/approve", a.approveOperationApproval)
	mux.HandleFunc("POST /api/operation-approvals/{id}/reject", a.rejectOperationApproval)
	mux.HandleFunc("GET /api/operation-audit", a.listOperationAudit)

	// 服务管理（service 级启停/选择已下线，统一走 deployment 级接口）
	mux.HandleFunc("GET /api/services", a.listServices)
	mux.HandleFunc("PUT /api/projects/{id}/env-selected", a.putEnvSelected)
	mux.HandleFunc("POST /api/projects/{id}/envs/{envName}/start-selected", a.startEnvSelected)

	// 日志
	mux.HandleFunc("GET /api/logs", a.fetchLogs)
	mux.HandleFunc("GET /api/log-search", a.searchLogs)
	mux.HandleFunc("GET /api/logs/context", a.fetchLogContext)
	mux.HandleFunc("GET /api/logs/context/page", a.fetchLogContextPage)
	mux.HandleFunc("GET /ws/logs", a.wsLogs)
	mux.HandleFunc("GET /ws/exec", a.wsExec)
	mux.HandleFunc("GET /api/exec/health", a.execHealth)
	mux.HandleFunc("GET /api/security/health", a.securityHealth)
	mux.HandleFunc("POST /api/security/provision", a.provisionSecurity)
	mux.HandleFunc("POST /api/transfer", a.transferFile)

	// Collector 控制(远端 agent 接收本机隧道请求)
	mux.HandleFunc("POST /api/collectors", a.startCollector)
	mux.HandleFunc("DELETE /api/collectors/{id}", a.stopCollector)
	mux.HandleFunc("GET /api/collectors", a.listCollectors)
	mux.HandleFunc("PUT /api/managed-deployments", a.putManagedDeployments)
	mux.HandleFunc("GET /api/managed-deployments/status", a.getManagedDeploymentsStatus)

	// 远程主机管理
	mux.HandleFunc("GET /api/hosts", a.listHosts)
	mux.HandleFunc("POST /api/hosts", a.createHost)
	mux.HandleFunc("PUT /api/hosts/{id}", a.updateHost)
	mux.HandleFunc("GET /api/agents", a.listAgents)
	mux.HandleFunc("POST /api/agents", a.createAgent)
	mux.HandleFunc("GET /api/agents/{host_id}", a.getAgent)
	mux.HandleFunc("PUT /api/agents/{host_id}", a.updateAgent)
	mux.HandleFunc("PUT /api/agents/{host_id}/transport", a.updateAgentTransport)
	mux.HandleFunc("PUT /api/agents/{host_id}/config", a.updateAgentConfig)
	mux.HandleFunc("DELETE /api/agents/{host_id}", a.deleteAgent)
	mux.HandleFunc("POST /api/agents/{host_id}/check", a.checkAgent)
	mux.HandleFunc("GET /api/agents/update-target", a.getAgentUpdateTarget)
	mux.HandleFunc("POST /api/agents/{host_id}/install", a.installAgent)
	mux.HandleFunc("POST /api/agents/{host_id}/restart", a.restartAgent)
	mux.HandleFunc("POST /api/agents/{host_id}/update-binary", a.updateAgentBinary)
	mux.HandleFunc("POST /api/agents/{host_id}/install-command", a.generateAgentInstallCommand)
	mux.HandleFunc("GET /api/agents/install.sh", a.serveAgentInstallScript)
	mux.HandleFunc("GET /api/agents/install-binary", a.serveAgentInstallBinary)
	mux.HandleFunc("POST /api/agents/{host_id}/transports/test", a.testAgentTransport)
	mux.HandleFunc("POST /api/agents/{host_id}/provision", a.provisionAgent)
	mux.HandleFunc("GET /api/hosts/{id}/managed-deployments/status", a.getHostManagedDeploymentsStatus)
	mux.HandleFunc("DELETE /api/hosts/{id}", a.deleteHost)

	// 远程日志源管理
	mux.HandleFunc("GET /api/log-sources", a.listLogSources)
	mux.HandleFunc("POST /api/log-sources", a.createLogSource)
	mux.HandleFunc("PUT /api/log-sources/{id}", a.updateLogSource)
	mux.HandleFunc("DELETE /api/log-sources/{id}", a.deleteLogSource)

	// SSH config 导入
	mux.HandleFunc("GET /api/ssh-config/hosts", a.listSSHConfigHosts)

	// 隧道管理
	mux.HandleFunc("GET /api/tunnels", a.listTunnels)
	mux.HandleFunc("POST /api/tunnels/{host_id}", a.connectTunnel)
	mux.HandleFunc("DELETE /api/tunnels/{host_id}", a.disconnectTunnel)
	mux.HandleFunc("GET /ws/tunnels", a.wsTunnels)

	// 远程监听聚合视图
	mux.HandleFunc("GET /api/remote/view", a.remoteView)
	mux.HandleFunc("GET /api/remote-log-search", a.remoteLogSearch)

	// Deployment 统一日志接口（location 无关）
	mux.HandleFunc("GET /api/deployments/{id}/logs", a.fetchDeploymentLogs)
	mux.HandleFunc("GET /api/deployments/{id}/search", a.searchDeploymentLogs)
	mux.HandleFunc("GET /ws/deployments/{id}/logs", a.wsDeploymentLogs)
	mux.HandleFunc("POST /api/deployments/{id}/debug/continue", a.continueDeploymentDebug)
	mux.HandleFunc("POST /api/deployments/{id}/debug/capture", a.deploymentDebugCapture)
	mux.HandleFunc("POST /api/deployments/{id}/debug/inspect", a.deploymentDebugInspect)
	mux.HandleFunc("POST /api/deployments/{id}/debug/breakpoints", a.deploymentDebugBreakpoints)
	mux.HandleFunc("POST /api/deployments/{id}/debug/continue-thread", a.deploymentDebugContinueThread)
	mux.HandleFunc("POST /api/deployments/{id}/debug/pause", a.deploymentDebugPause)
	mux.HandleFunc("POST /api/deployments/{id}/debug/step-over", a.deploymentDebugStepOver)
	mux.HandleFunc("POST /api/deployments/{id}/debug/step-in", a.deploymentDebugStepIn)
	mux.HandleFunc("POST /api/deployments/{id}/debug/step-out", a.deploymentDebugStepOut)
	mux.HandleFunc("POST /api/deployments/{id}/debug/stack", a.deploymentDebugStack)
	mux.HandleFunc("POST /api/deployments/{id}/debug/scopes", a.deploymentDebugScopes)
	mux.HandleFunc("POST /api/deployments/{id}/debug/variables", a.deploymentDebugVariables)
	mux.HandleFunc("POST /api/deployments/{id}/debug/evaluate", a.deploymentDebugEvaluate)

	// Pipeline 模板与预览
	mux.HandleFunc("GET /api/pipeline/reserved-variables", a.listPipelineReservedVariables)
	mux.HandleFunc("GET /api/pipeline/templates", a.listPipelineTemplates)
	mux.HandleFunc("GET /api/pipeline/templates/{source}/{id}", a.getPipelineTemplate)
	mux.HandleFunc("POST /api/pipeline/templates/preview", a.previewPipelineTemplate)
	mux.HandleFunc("POST /api/pipeline/templates/import", a.importPipelineTemplate)
	mux.HandleFunc("POST /api/projects/{id}/pipelines/{pipelineId}/preview", a.previewProjectPipeline)
	mux.HandleFunc("POST /api/projects/{id}/pipelines/{pipelineId}/deploy", a.deployProjectPipeline)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}/runs", a.listProjectPipelineRuns)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}/runs/{runId}", a.getProjectPipelineRun)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}/runs/{runId}/logs", a.readProjectPipelineRunLogs)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}/artifacts", a.listProjectArtifactsForPipeline)
	mux.HandleFunc("GET /ws/runs/{runId}/logs", a.wsRunLogs)

	// Deployment 进程控制
	mux.HandleFunc("POST /api/deployments/{id}/start", a.startDeployment)
	mux.HandleFunc("POST /api/deployments/{id}/stop", a.stopDeployment)
	mux.HandleFunc("POST /api/deployments/{id}/restart", a.restartDeployment)
	mux.HandleFunc("POST /api/deployments/{id}/hosts/{host_id}/start", a.startDeploymentHost)
	mux.HandleFunc("POST /api/deployments/{id}/hosts/{host_id}/stop", a.stopDeploymentHost)
	mux.HandleFunc("POST /api/deployments/{id}/hosts/{host_id}/restart", a.restartDeploymentHost)

	return cors(a.withSecurity(mux))
}

// Start 加载注册表中的已有项目，然后监听 addr 地址。
//
// 参数：
//   - addr: 监听地址，如 ":8080"
//
// 返回：
//   - ListenAndServe 返回的错误
func (a *App) Start(addr string) error {
	a.loadRegisteredProjects()
	a.loadManagedDeployments()
	a.startProcessReconcileLoop()
	a.startManagedDeploymentReconciler()
	server := &http.Server{Addr: addr, Handler: a.Handler()}
	tlsConfig, enabled, err := a.tlsConfigForListen()
	if err != nil {
		return err
	}
	if enabled {
		server.TLSConfig = tlsConfig
		return server.ListenAndServeTLS("", "")
	}
	return server.ListenAndServe()
}

func (a *App) tlsConfigForListen() (*tls.Config, bool, error) {
	if a.cfg.TLSCertFile != "" || a.cfg.TLSKeyFile != "" {
		if a.cfg.TLSCertFile == "" || a.cfg.TLSKeyFile == "" {
			return nil, false, errors.New("tls cert file and key file must be configured together")
		}
		cert, err := tls.LoadX509KeyPair(a.cfg.TLSCertFile, a.cfg.TLSKeyFile)
		if err != nil {
			return nil, false, err
		}
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, true, nil
	}
	if a.securityStore == nil {
		return nil, false, nil
	}
	state := a.securityStore.State()
	if state.TLSMode != security.TLSModeAuto || state.ServerCert == "" || state.ServerKey == "" {
		return nil, false, nil
	}
	cert, err := tls.X509KeyPair([]byte(state.ServerCert), []byte(state.ServerKey))
	if err != nil {
		return nil, false, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, true, nil
}

func (a *App) startManagedDeploymentReconciler() {
	if a.managedReconciler == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.managedReconcileCancel = cancel
	a.managedReconciler.Start(ctx)
	go func() {
		if err := a.managedReconciler.ReconcileAll(ctx); err != nil {
			log.Printf("[SuperDev] initial managed deployment reconcile failed: %v", err)
		}
	}()
}

// loadRegisteredProjects 从注册表读取所有项目路径并加载到内存。
//
// 注意：
//   - 若某个路径的配置加载失败，跳过该项目（不中断整体加载）
//   - 为每个项目、服务和 deployment 分配 UUID（若 ID 为空）
func (a *App) loadRegisteredProjects() {
	a.pidStore.KillAll()
	paths := a.registry.List()
	used := newProjectIdentitySet()
	for _, path := range paths {
		loader := config.NewLoader(path)
		p, err := loader.Load()
		if err != nil {
			continue
		}
		assignIDsAvoiding(&p, &used)
		// 将新生成的 ID 写回配置，避免重启后 ID 变化
		_ = loader.Save(p)
		a.mu.Lock()
		a.appendProjectLocked(p)
		a.mu.Unlock()
	}
}

func (a *App) appendProjectLocked(p model.Project) {
	a.projects = append(a.projects, p)
	a.registerProjectBackendsLocked(p)
}

func (a *App) reconcileProjectsAsync(projects ...model.Project) {
	if a.managedReconciler == nil {
		return
	}
	// projects 只用于计算受影响 host，真正下发的 desired 清单会在 goroutine 中
	// 重新读取当前 a.projects。删除项目后必须依赖这个时序，才能推送“已删除后”的空缺清单。
	hostIDs := remoteDeploymentHostIDs(projects)
	for _, hostID := range hostIDs {
		hostID := hostID
		go func() {
			if err := a.managedReconciler.Reconcile(context.Background(), hostID); err != nil {
				log.Printf("[SuperDev] managed deployment reconcile failed for host %s: %v", hostID, err)
			}
		}()
	}
}

func unionProjectsForReconcile(before, after model.Project) []model.Project {
	return []model.Project{before, after}
}

func (a *App) registerProjectBackendsLocked(p model.Project) {
	for _, svc := range p.Services {
		for _, dep := range svc.Deployments {
			// 新增项目和启动时加载项目必须共享同一套 backend 构建逻辑，
			// 否则运行期注册项目后 deployment 日志接口会短暂或永久 404。
			b := buildBackend(dep, svc.ID, a.store, a.buf, a.nodeTransport)
			a.backends[dep.ID] = b
		}
	}
}

func (a *App) clearProjectBackendsLocked(project model.Project) {
	for _, svc := range project.Services {
		for _, dep := range svc.Deployments {
			delete(a.backends, dep.ID)
		}
	}
}

// findProject 在项目列表中按 ID 查找项目。
//
// 注意：调用方需自行持有 RLock 或在安全上下文中调用。
func (a *App) findProject(id string) (model.Project, bool) {
	for _, p := range a.projects {
		if p.ID == id {
			return p, true
		}
	}
	return model.Project{}, false
}

// getOrCreateManager 获取或创建指定项目的进程管理器。
//
// 参数：
//   - projectID: 项目唯一标识
//
// 返回：
//   - 对应的 *process.Manager（总是非 nil）
//
// 注意：写操作需要持有写锁，此函数内部完成加锁。
func (a *App) getOrCreateManager(projectID string) *process.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mgr, ok := a.managers[projectID]; ok {
		return mgr
	}
	mgr := process.NewManager(a.buf.Append)
	a.managers[projectID] = mgr
	return mgr
}

// assignIDs 为 Project、Services 及 Deployments 分配 UUID（若 ID 为空字符串）。
func assignIDs(p *model.Project) {
	used := newProjectIdentitySet()
	assignIDsAvoiding(p, &used)
}

type projectIdentitySet struct {
	projectIDs    map[string]struct{}
	deploymentIDs map[string]struct{}
}

func newProjectIdentitySet() projectIdentitySet {
	return projectIdentitySet{
		projectIDs:    map[string]struct{}{},
		deploymentIDs: map[string]struct{}{},
	}
}

func (s *projectIdentitySet) addProjectID(id string) {
	if id == "" {
		return
	}
	s.projectIDs[id] = struct{}{}
}

func (s *projectIdentitySet) hasProjectID(id string) bool {
	if id == "" {
		return false
	}
	_, ok := s.projectIDs[id]
	return ok
}

func (s *projectIdentitySet) addDeploymentID(id string) {
	if id == "" {
		return
	}
	s.deploymentIDs[id] = struct{}{}
}

func (s *projectIdentitySet) hasDeploymentID(id string) bool {
	if id == "" {
		return false
	}
	_, ok := s.deploymentIDs[id]
	return ok
}

func (s *projectIdentitySet) addProject(project model.Project) {
	s.addProjectID(project.ID)
	for _, svc := range project.Services {
		for _, dep := range svc.Deployments {
			s.addDeploymentID(dep.ID)
		}
	}
}

func (a *App) projectIdentitySetLocked(excludeIndex int) projectIdentitySet {
	used := newProjectIdentitySet()
	for i, project := range a.projects {
		if i == excludeIndex {
			continue
		}
		used.addProject(project)
	}
	return used
}

func (a *App) projectIdentitySetExcludingRootPathLocked(rootPath string) projectIdentitySet {
	used := newProjectIdentitySet()
	for _, project := range a.projects {
		if rootPath != "" && project.RootPath == rootPath {
			continue
		}
		used.addProject(project)
	}
	return used
}

// assignIDsAvoiding 为项目补齐身份，并在复制项目携带旧 ID 时避开已有 project/deployment ID。
func assignIDsAvoiding(p *model.Project, used *projectIdentitySet) {
	if p.ID == "" {
		p.ID = uniqueUUID(used.hasProjectID)
	} else if used.hasProjectID(p.ID) {
		// 复制项目目录会把 .superdev/config.yaml 中的 ID 一起复制。
		// project/deployment ID 在运行态是全局身份，冲突时必须为新项目重分配。
		p.ID = uniqueUUID(used.hasProjectID)
	}
	used.addProjectID(p.ID)
	for i := range p.Services {
		if p.Services[i].ID == "" {
			p.Services[i].ID = uuid.NewString()
		}
		p.Services[i].ProjectID = p.ID
		for j := range p.Services[i].Deployments {
			if p.Services[i].Deployments[j].ID == "" {
				p.Services[i].Deployments[j].ID = uniqueUUID(used.hasDeploymentID)
			} else if used.hasDeploymentID(p.Services[i].Deployments[j].ID) {
				p.Services[i].Deployments[j].ID = uniqueUUID(used.hasDeploymentID)
			}
			used.addDeploymentID(p.Services[i].Deployments[j].ID)
		}
	}
}

func uniqueUUID(exists func(string) bool) string {
	for {
		id := uuid.NewString()
		if !exists(id) {
			return id
		}
	}
}

// WriteTestLog 供测试注入日志条目。生产代码不调用此方法。
func (a *App) WriteTestLog(e model.LogEntry) {
	a.buf.Append(e)
}

// SetBackendForTest 供测试直接注入 backend。生产代码不调用此方法。
func (a *App) SetBackendForTest(depID string, b logbackend.LogBackend) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.backends == nil {
		a.backends = map[string]logbackend.LogBackend{}
	}
	a.backends[depID] = b
}
