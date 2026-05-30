package process

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdev/agent/logparse"
	"github.com/superdev/agent/model"
)

// Manager 管理多个服务进程的生命周期。
//
// 职责：
//   - 按 model.Deployment 启动/停止子进程（StartDeployment/StopDeployment/RestartDeployment）
//   - 提供不绑定 deployment 概念的低阶启动原语 StartProcess（供 collector 等子系统使用）
//   - 监控进程退出并更新状态
//   - 将进程输出通过 onLog 回调传递给上层，日志以 DeploymentID 归属
//
// 边界：
//   - 不持久化状态，仅在内存维护 runners/status 映射
//   - 不解析配置文件，仅消费 model.Deployment / ProcessSpec 数据结构
//   - 不直接写日志存储，日志处理由 onLog 回调负责
type Manager struct {
	mu          sync.Mutex
	runners     map[string]*Runner
	status      map[string]model.ServiceStatus
	generations map[string]uint64 // 每次 Start 递增，防止旧监控 goroutine 覆盖新进程状态
	onLog       func(model.LogEntry)
	runID       string
	logSeq      atomic.Int64 // 单调递增，为每条 LogEntry 分配唯一 ID
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

// ProcessSpec 描述启动一个进程所需的最小配置，不依赖 model.Service/Deployment。
//
// 它是 process.Manager 对外的低阶启动契约：调用方（deployment 启停、collector
// 日志采集）只需提供命令与运行环境，由 Manager 负责进程生命周期与日志归属。
type ProcessSpec struct {
	Command string
	WorkDir string
	Env     map[string]string
	EnvFile string
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
func (m *Manager) emitLog(id, level, stream, message string) {
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	m.onLog(model.LogEntry{
		ID:           m.logSeq.Add(1),
		DeploymentID: id,
		RunID:        runID,
		Timestamp:    time.Now().UTC(),
		Level:        level,
		Message:      message,
		Stream:       stream,
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

// StartDeployment 以 Deployment 的配置启动一个进程，以 dep.ID 为键管理。
//
// 只读 deployment（location=remote 且 StartCommand/StopCommand 任一为空）直接返回 nil。
// location 为空时按 local 处理。
func (m *Manager) StartDeployment(dep model.Deployment) error {
	if dep.IsReadOnly() {
		return nil
	}
	return m.startByID(dep.ID, deploymentToSpec(dep))
}

// StopDeployment 停止指定 deployment 的进程。
// 与 Stop/Status 等方法共用同一 runners map，以 deploymentID 为键，语义上区分 service 和 deployment 两个命名空间。
func (m *Manager) StopDeployment(deploymentID string) {
	m.Stop(deploymentID)
}

// RestartDeployment 重启指定 deployment 的进程。
func (m *Manager) RestartDeployment(dep model.Deployment) error {
	m.StopDeployment(dep.ID)
	return m.StartDeployment(dep)
}

// DeploymentStatus 返回 deployment 进程的当前状态。
func (m *Manager) DeploymentStatus(deploymentID string) model.ServiceStatus {
	return m.Status(deploymentID)
}

// DeploymentPID 返回 deployment 进程的 PID；未启动或已退出时返回 0。
func (m *Manager) DeploymentPID(deploymentID string) int {
	return m.PID(deploymentID)
}

// IsDeploymentActive 报告 deployment 是否已启动且未停止。
func (m *Manager) IsDeploymentActive(deploymentID string) bool {
	return m.IsActive(deploymentID)
}

// StartProcess 以指定 id 为键启动一个进程，是不绑定 deployment 概念的低阶启动入口。
//
// 供 collector 等内部子系统直接启动采集进程使用；日志以 id 作为 DeploymentID 归属。
// 与 StartDeployment 共用 startByID 底座与同一 runners 命名空间。
func (m *Manager) StartProcess(id string, spec ProcessSpec) error {
	return m.startByID(id, spec)
}

// startByID 以指定的 id 为键启动进程，是所有启动路径的核心实现。
func (m *Manager) startByID(id string, spec ProcessSpec) error {
	m.mu.Lock()
	if m.status[id] == model.StatusStarting {
		m.mu.Unlock()
		return nil
	}
	if _, ok := m.runners[id]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	m.setStatus(id, model.StatusStarting)

	r := NewRunner(RunnerConfig{
		Command: spec.Command,
		WorkDir: spec.WorkDir,
		Env:     spec.Env,
		EnvFile: spec.EnvFile,
		OnLine: func(line, stream string) {
			m.mu.Lock()
			runID := m.runID
			m.mu.Unlock()
			m.onLog(model.LogEntry{
				ID:           m.logSeq.Add(1),
				DeploymentID: id,
				RunID:        runID,
				Timestamp:    time.Now().UTC(),
				Level:        logparse.DetectLevel(line),
				Message:      line,
				Stream:       stream,
			})
		},
	})

	if err := r.Start(); err != nil {
		m.setStatus(id, model.StatusFailed)
		m.emitLog(id, "ERROR", "stderr", "启动失败: "+err.Error())
		return err
	}

	m.mu.Lock()
	m.runners[id] = r
	m.mu.Unlock()
	m.setStatus(id, model.StatusRunning)

	gen := m.bumpGeneration(id)
	go func() {
		for r.IsRunning() {
			time.Sleep(200 * time.Millisecond)
		}
		if m.generation(id) != gen {
			return
		}
		exitCode := r.ExitCode()
		if exitCode != 0 {
			m.setStatus(id, model.StatusFailed)
			m.emitLog(id, "ERROR", "stderr",
				fmt.Sprintf("进程异常退出，退出码 %d", exitCode))
		} else {
			m.setStatus(id, model.StatusStopped)
		}
	}()

	return nil
}

// deploymentToSpec 将 Deployment 字段映射为 ProcessSpec。
// local deployment 用自身 Command/WorkDir/Env；
// remote deployment 用 StartCommand 作为命令，本机 env/workDir 不透传。
func deploymentToSpec(dep model.Deployment) ProcessSpec {
	cmd := dep.Command
	workDir := dep.WorkDir
	env := dep.Env
	envFile := dep.EnvFile
	if dep.Location == model.LocationRemote {
		cmd = dep.StartCommand
		workDir = ""
		env = nil
		envFile = ""
	}
	return ProcessSpec{Command: cmd, WorkDir: workDir, Env: env, EnvFile: envFile}
}
