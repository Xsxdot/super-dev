// Package api 提供 SuperDev agent 的 HTTP REST API 和 WebSocket 日志流服务。
//
// 职责：
//   - 暴露项目管理接口（列表、添加、删除、规则读写）
//   - 暴露服务管理接口（列表、启动、停止、重启）
//   - 暴露日志查询接口（REST 分页 + WebSocket 实时推送）
//   - 生命周期管理：启动时从注册表加载已注册项目
//
// 边界：
//   - 不直接持久化日志，日志由 logbuf.Buffer → store.Store 异步写入
//   - 不持有进程的直接引用，通过 process.Manager 间接管理
//   - ID 分配（UUID）在此层完成，config.Loader 不负责 ID
package api

import (
	"net/http"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/superdev/agent/config"
	"github.com/superdev/agent/logbuf"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
	"github.com/superdev/agent/store"
)

// AppConfig 包含创建 App 所需的配置参数。
type AppConfig struct {
	// DataDir 是存放数据库文件和注册表文件的根目录。
	DataDir string
}

// App 是 HTTP API 服务的核心结构，持有所有运行时状态。
type App struct {
	cfg      AppConfig
	mu       sync.RWMutex
	projects []model.Project
	managers map[string]*process.Manager // projectID → manager
	buf      *logbuf.Buffer
	store    *store.Store
	registry *config.Registry
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

	buf := logbuf.New(s, 2000)
	registryPath := filepath.Join(cfg.DataDir, "projects.json")
	registry := config.NewRegistry(registryPath)

	return &App{
		cfg:      cfg,
		projects: []model.Project{},
		managers: map[string]*process.Manager{},
		buf:      buf,
		store:    s,
		registry: registry,
	}, nil
}

// Close 停止 Buffer 的 flush goroutine 并关闭 Store 数据库连接，释放所有资源。
//
// 应在 App 不再使用时调用，通常配合 defer 或测试 Cleanup 使用。
func (a *App) Close() {
	a.buf.Close()
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
	mux.HandleFunc("GET /api/projects/{id}/rules", a.getProjectRules)
	mux.HandleFunc("PUT /api/projects/{id}/rules", a.putProjectRules)

	// 服务管理
	mux.HandleFunc("GET /api/services", a.listServices)
	mux.HandleFunc("POST /api/services/{id}/start", a.startService)
	mux.HandleFunc("POST /api/services/{id}/stop", a.stopService)
	mux.HandleFunc("POST /api/services/{id}/restart", a.restartService)
	mux.HandleFunc("POST /api/projects/{id}/start-selected", a.startSelected)
	mux.HandleFunc("PUT /api/projects/{id}/selected", a.putSelected)

	// 日志
	mux.HandleFunc("GET /api/logs", a.fetchLogs)
	mux.HandleFunc("GET /ws/logs", a.wsLogs)

	return cors(mux)
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
	return http.ListenAndServe(addr, a.Handler())
}

// loadRegisteredProjects 从注册表读取所有项目路径并加载到内存。
//
// 注意：
//   - 若某个路径的配置加载失败，跳过该项目（不中断整体加载）
//   - 为每个项目和服务分配 UUID（若 ID 为空）
func (a *App) loadRegisteredProjects() {
	paths := a.registry.List()
	for _, path := range paths {
		loader := config.NewLoader(path)
		p, err := loader.Load()
		if err != nil {
			continue
		}
		assignIDs(&p)
		a.mu.Lock()
		a.projects = append(a.projects, p)
		a.mu.Unlock()
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

// assignIDs 为 Project 及其 Services 分配 UUID（若 ID 为空字符串）。
func assignIDs(p *model.Project) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	for i := range p.Services {
		if p.Services[i].ID == "" {
			p.Services[i].ID = uuid.NewString()
		}
		p.Services[i].ProjectID = p.ID
	}
}
