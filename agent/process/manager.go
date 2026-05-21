package process

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/superdev/agent/logparse"
	"github.com/superdev/agent/model"
)

// Manager 管理多个服务进程的生命周期。
//
// 职责：
//   - 按 model.Service 启动/停止子进程
//   - 监控进程退出并更新状态
//   - 支持按 Order 字段分组串行启动，同组并行
//   - 将进程输出通过 onLog 回调传递给上层
//
// 边界：
//   - 不持久化状态，仅在内存维护 runners/status 映射
//   - 不解析配置文件，仅消费 model.Service 数据结构
//   - 不直接写日志存储，日志处理由 onLog 回调负责
type Manager struct {
	mu          sync.Mutex
	runners     map[string]*Runner
	status      map[string]model.ServiceStatus
	generations map[string]uint64 // 每次 Start 递增，防止旧监控 goroutine 覆盖新进程状态
	onLog       func(model.LogEntry)
	runID       string
}

// NewManager 创建一个新的 Manager。
//
// 参数：
//   - onLog: 每当有日志行产生时调用，调用方负责写入存储或广播
func NewManager(onLog func(model.LogEntry)) *Manager {
	return &Manager{
		runners:     map[string]*Runner{},
		status:      map[string]model.ServiceStatus{},
		generations: map[string]uint64{},
		onLog:       onLog,
	}
}

// SetRunID 设置当前运行会话 ID，写入后续产生的所有 LogEntry.RunID。
//
// 通常在每次批量启动前调用，用于区分同一服务的多次运行日志。
func (m *Manager) SetRunID(id string) {
	m.mu.Lock()
	m.runID = id
	m.mu.Unlock()
}

// Start 启动单个服务进程。
//
// 参数：
//   - svc: 服务定义，包含 ID、Command、WorkDir、Env 等信息
//
// 返回：
//   - 启动成功返回 nil；启动失败返回错误并将状态置为 StatusFailed
//
// 注意：
//   - 启动后在后台 goroutine 轮询进程状态，退出时自动置为 StatusStopped
//   - 本 session 内已启动过的服务（runners 中仍有记录）重复调用 Start 为空操作，须先 Stop/Restart
//   - 与 Swift ProcessManager 一致：guard runners[service.id] == nil，不依赖 IsRunning()
//     （后台化命令如 `npm run dev &` 会使 sh 退出但子进程继续，IsRunning 不可靠）
func (m *Manager) Start(svc model.Service) error {
	m.mu.Lock()
	if m.status[svc.ID] == model.StatusStarting {
		m.mu.Unlock()
		return nil
	}
	if _, ok := m.runners[svc.ID]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	m.setStatus(svc.ID, model.StatusStarting)

	r := NewRunner(RunnerConfig{
		Command: svc.Command,
		WorkDir: svc.WorkDir,
		Env:     svc.Env,
		EnvFile: svc.EnvFile,
		OnLine: func(line, stream string) {
			m.mu.Lock()
			runID := m.runID
			m.mu.Unlock()
			m.onLog(model.LogEntry{
				ServiceID: svc.ID,
				RunID:     runID,
				Timestamp: time.Now().UTC(),
				Level:     logparse.DetectLevel(line),
				Message:   line,
				Stream:    stream,
			})
		},
	})

	if err := r.Start(); err != nil {
		m.setStatus(svc.ID, model.StatusFailed)
		m.emitLog(svc.ID, "ERROR", "stderr", "启动失败: "+err.Error())
		return err
	}

	m.mu.Lock()
	m.runners[svc.ID] = r
	m.mu.Unlock()
	m.setStatus(svc.ID, model.StatusRunning)

	gen := m.bumpGeneration(svc.ID)
	// 后台监控进程退出，自动更新状态。
	// 仅当 generation 未变时写回，避免重启后旧 goroutine 把 running 覆盖为 stopped。
	go func() {
		for r.IsRunning() {
			time.Sleep(200 * time.Millisecond)
		}
		if m.generation(svc.ID) != gen {
			return
		}
		exitCode := r.ExitCode()
		if exitCode != 0 {
			m.setStatus(svc.ID, model.StatusFailed)
			m.emitLog(svc.ID, "ERROR", "stderr",
				fmt.Sprintf("进程异常退出，退出码 %d", exitCode))
		} else {
			m.setStatus(svc.ID, model.StatusStopped)
		}
	}()

	return nil
}

// Restart 停止后重新启动服务。
func (m *Manager) Restart(svc model.Service) error {
	m.Stop(svc.ID)
	return m.Start(svc)
}

// StartGroup 按 Order 字段分组，从小到大串行启动各组；同组内并行启动。
//
// 参数：
//   - services: 待启动的服务列表，可混合不同 Order 值
//
// 返回：
//   - 任意服务启动失败时立即返回错误，后续组不再启动
//
// 注意：
//   - 每组内的启动并发执行，组间严格按 Order 升序串行
func (m *Manager) StartGroup(services []model.Service) error {
	groups := groupByOrder(services)
	for _, group := range groups {
		var wg sync.WaitGroup
		errCh := make(chan error, len(group))
		for _, svc := range group {
			wg.Add(1)
			go func(s model.Service) {
				defer wg.Done()
				if err := m.Start(s); err != nil {
					errCh <- err
				}
			}(svc)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			return err
		}
	}
	return nil
}

// Stop 强制终止指定服务进程，并立即将状态置为 StatusStopped。
//
// 注意：
//   - 进程未启动或已退出时调用为空操作
//   - bumpGeneration 保证后台监控 goroutine 不会在 Stop 后把状态覆盖为 failed
func (m *Manager) Stop(serviceID string) {
	m.bumpGeneration(serviceID)
	m.mu.Lock()
	r := m.runners[serviceID]
	delete(m.runners, serviceID)
	m.mu.Unlock()
	if r != nil {
		r.Stop()
	}
	m.setStatus(serviceID, model.StatusStopped)
}

// StopAll 停止所有已知服务进程。
func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.runners))
	for id := range m.runners {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// IsActive 表示服务在本 session 内是否已启动且尚未 Stop。
//
// 对后台化命令（sh 已退出、子进程仍在）也返回 true，与 Swift runners 语义一致。
func (m *Manager) IsActive(serviceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.status[serviceID]; st == model.StatusStarting || st == model.StatusRunning {
		return true
	}
	_, ok := m.runners[serviceID]
	return ok
}

// Status 返回指定服务的当前状态。
//
// 未曾启动的服务返回 StatusStopped（零值）。
func (m *Manager) Status(serviceID string) model.ServiceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.status[serviceID]; ok {
		return st
	}
	return model.StatusStopped
}

// PID 返回指定服务进程的 PID；未启动或已退出时返回 0。
func (m *Manager) PID(serviceID string) int {
	m.mu.Lock()
	r := m.runners[serviceID]
	m.mu.Unlock()
	if r != nil {
		return r.PID()
	}
	return 0
}

// setStatus 线程安全地更新服务状态。
func (m *Manager) setStatus(id string, st model.ServiceStatus) {
	m.mu.Lock()
	m.status[id] = st
	m.mu.Unlock()
}

// emitLog 通过 onLog 回调发送一条系统日志，level 为 "ERROR"/"INFO" 等。
func (m *Manager) emitLog(serviceID, level, stream, message string) {
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	m.onLog(model.LogEntry{
		ServiceID: serviceID,
		RunID:     runID,
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   message,
		Stream:    stream,
	})
}

func (m *Manager) bumpGeneration(serviceID string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generations[serviceID]++
	return m.generations[serviceID]
}

func (m *Manager) generation(serviceID string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generations[serviceID]
}

// groupByOrder 将服务列表按 Order 字段分组，返回按 Order 升序排列的二维切片。
//
// 同一 Order 值的服务归入同一组，组内顺序不保证。
func groupByOrder(services []model.Service) [][]model.Service {
	orders := []int{}
	byOrder := map[int][]model.Service{}
	for _, s := range services {
		if _, ok := byOrder[s.Order]; !ok {
			orders = append(orders, s.Order)
		}
		byOrder[s.Order] = append(byOrder[s.Order], s)
	}
	slices.Sort(orders)
	groups := make([][]model.Service, len(orders))
	for i, o := range orders {
		groups[i] = byOrder[o]
	}
	return groups
}
