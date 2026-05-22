// manager.go 实现 collector.Manager:按 (name, type) 启停虚拟 Service。
//
// 职责：
//   - 维护 (name, type) → collector_id 的映射
//   - 启动时调用 Probe 校验目标存在性,然后通过 process.Manager 启动虚拟 Service
//   - 关闭时停止虚拟 Service 并清理映射
//
// 边界：
//   - 不直接执行命令,使用现有的 process.Manager + process.Runner
//   - 不写存储,collector 是运行时状态;断电/重启后丢失,本机重连后重新 EnsureCollector
package collector

import (
	"errors"
	"strings"
	"sync"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

// ErrTargetNotFound 表示远端目标不存在(systemd 单元或 docker 容器未找到)。
var ErrTargetNotFound = errors.New("target not found on host")

// Probe 是探测某个 (type, name) 是否存在于本机的接口。
//
// 实现可调用 `systemctl list-units` 或 `docker inspect`,
// 测试时注入桩(总是返回 nil 或固定错误)。
type Probe interface {
	Exists(t model.LogSourceType, name string) error
}

// ProbeFunc 将函数适配为 Probe 接口。
type ProbeFunc func(t model.LogSourceType, name string) error

// Exists 直接调用底层函数。
func (f ProbeFunc) Exists(t model.LogSourceType, name string) error { return f(t, name) }

// Manager 管理远端 collector 任务。
type Manager struct {
	mu      sync.Mutex
	procMgr *process.Manager
	probe   Probe
	items   map[string]model.Collector
}

// NewManager 创建新的 collector.Manager。
//
// 参数：
//   - procMgr: 已初始化的 process.Manager,用于跑虚拟 Service
//   - probe: 目标存在性探测器
func NewManager(procMgr *process.Manager, probe Probe) *Manager {
	return &Manager{
		procMgr: procMgr,
		probe:   probe,
		items:   map[string]model.Collector{},
	}
}

// Start 启动 (name, type) 对应的采集任务。
//
// 参数：
//   - name: 校验过的服务名/容器名
//   - t: 采集类型
//
// 返回：
//   - 采集任务的稳定 ID
//   - name 非法时返回 ErrInvalidName;type 不支持返回 ErrUnsupportedType
//   - probe 返回 ErrTargetNotFound 时透传
//
// 注意：同一 (name, type) 重复调用幂等,返回同一 ID。
func (m *Manager) Start(name string, t model.LogSourceType) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	argv, err := BuildCommand(t, name)
	if err != nil {
		return "", err
	}
	if err := m.probe.Exists(t, name); err != nil {
		return "", err
	}
	return m.startWithArgv(name, t, argv)
}

// StartForTest 测试入口:跳过 BuildCommand,直接用指定 argv 启动。
//
// 仅用于测试,生产代码不要调用。
func (m *Manager) StartForTest(name string, t model.LogSourceType, argv []string) (string, error) {
	return m.startWithArgv(name, t, argv)
}

// startWithArgv 内部入口,跳过 name/type 校验和 probe。
func (m *Manager) startWithArgv(name string, t model.LogSourceType, argv []string) (string, error) {
	id := CollectorID(name, t)

	m.mu.Lock()
	if existing, ok := m.items[id]; ok {
		m.mu.Unlock()
		return existing.ID, nil
	}
	m.items[id] = model.Collector{
		ID:        id,
		Name:      name,
		Type:      t,
		ServiceID: id,
		Status:    model.StatusStarting,
	}
	m.mu.Unlock()

	svc := model.Service{
		ID:      id,
		Name:    name,
		Command: shellQuote(argv),
	}
	if err := m.procMgr.Start(svc); err != nil {
		m.mu.Lock()
		delete(m.items, id)
		m.mu.Unlock()
		return "", err
	}

	m.mu.Lock()
	col := m.items[id]
	col.Status = model.StatusRunning
	m.items[id] = col
	m.mu.Unlock()
	return id, nil
}

// shellQuote 把 argv 拼成 sh -c 可解释的字符串。
//
// process.Runner 当前只接受 Command 字符串并通过 sh -c 执行；这里对每个 token
// 做单引号转义，避免未来扩展 argv token 时把内容交给 shell 重新解释。
// 注意：argv 由上层 buildArgv 从硬编码模板生成，name/type 均经过正则校验，
// 不存在用户任意字符串注入的路径；shellQuote 是额外的防御层。
// 若未来 Runner 支持 argv 模式，可移除 shellQuote 并直接传递 argv 列表。
func shellQuote(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

// Stop 终止指定 collector,并从映射中移除。
//
// 参数：
//   - id: collector ID
//
// 返回：
//   - 永远返回 nil(进程已不存在视为成功);未来扩展时再增加错误情况
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	_, ok := m.items[id]
	delete(m.items, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	m.procMgr.Stop(id)
	return nil
}

// Get 查询 id 对应的 collector;不存在时返回 (zero, false)。
func (m *Manager) Get(id string) (model.Collector, bool) {
	m.mu.Lock()
	c, ok := m.items[id]
	m.mu.Unlock()
	if !ok {
		return model.Collector{}, false
	}
	c.Status = m.procMgr.Status(c.ServiceID)
	return c, true
}

// List 返回当前所有 collector 的快照。
func (m *Manager) List() []model.Collector {
	m.mu.Lock()
	out := make([]model.Collector, 0, len(m.items))
	for _, c := range m.items {
		out = append(out, c)
	}
	m.mu.Unlock()
	for i := range out {
		out[i].Status = m.procMgr.Status(out[i].ServiceID)
	}
	return out
}
