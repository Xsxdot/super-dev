package process

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xsxdot/super-dev/agent/logparse"
	"github.com/xsxdot/super-dev/agent/model"
)

// Manager 管理多个服务进程的生命周期。
//
// 职责：
//   - 按 model.Deployment 启动/停止子进程（StartDeployment/StopDeployment/RestartDeployment）
//   - 提供不绑定 deployment 概念的低阶启动原语 StartProcess（供 collector 等子系统使用）
//   - 监控进程退出并更新状态
//   - 将进程输出通过 onLog 回调传递给上层，日志以 DeploymentID 归属
//   - 状态发生实际跃迁时通过可选的 onStatusChange 回调通知上层（SetOnStatusChange 构造后注入）
//
// 边界：
//   - 不持久化状态，仅在内存维护 runners/status 映射
//   - 不解析配置文件，仅消费 model.Deployment / ProcessSpec 数据结构
//   - 不直接写日志存储，日志处理由 onLog 回调负责
type Manager struct {
	mu           sync.Mutex
	runners      map[string]*Runner
	status       map[string]model.ServiceStatus
	generations  map[string]uint64 // 每次 Start/Stop 递增，防止旧退出回调覆盖新进程状态
	runtimes     map[string]model.RuntimeType
	launchdDeps  map[string]model.Deployment
	readiness    map[string]*model.ReadinessProbe
	backgrounded map[string]bool
	onLog        func(model.LogEntry)
	// onStatusChange 在 deployment 运行态变更时回调（starting/running/failed/stopped）。
	// 用于驱动节点状态事件帧——「运行即转发」的时延来源。回调在锁外调用，
	// 实现方不得阻塞（应只做 signal/入队）。
	onStatusChange func(deploymentID string, st model.ServiceStatus)
	runID          string
	logSeq         atomic.Int64 // 单调递增，为每条 LogEntry 分配唯一 ID
}

// NewManager 创建一个新的 Manager。
//
// 参数：
//   - onLog: 每当有日志行产生时调用，调用方负责写入存储或广播
func NewManager(onLog func(model.LogEntry)) *Manager {
	return &Manager{
		runners:      map[string]*Runner{},
		status:       map[string]model.ServiceStatus{},
		generations:  map[string]uint64{},
		runtimes:     map[string]model.RuntimeType{},
		launchdDeps:  map[string]model.Deployment{},
		readiness:    map[string]*model.ReadinessProbe{},
		backgrounded: map[string]bool{},
		onLog:        onLog,
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

// SetOnStatusChange 注册 deployment 状态跃迁回调，构造后可选调用（不调用则 onStatusChange 为 nil，
// setStatus 直接跳过通知）。
//
// 与 onLog 的差异：onLog 是 NewManager 的必填构造参数，每条日志行都会调用；onStatusChange
// 沿用 SetRunID 的 setter 惯例做构造后可选注入，仅在状态发生实际跃迁时才调用一次。
func (m *Manager) SetOnStatusChange(cb func(deploymentID string, st model.ServiceStatus)) {
	m.mu.Lock()
	m.onStatusChange = cb
	m.mu.Unlock()
}

// ProcessSpec 描述启动一个进程所需的最小配置，不依赖 model.Service/Deployment。
//
// 它是 process.Manager 对外的低阶启动契约：调用方（deployment 启停、collector
// 日志采集）只需提供命令与运行环境，由 Manager 负责进程生命周期与日志归属。
type ProcessSpec struct {
	Command string
	// PreRun 非空时在主进程启动前同步执行（如 go build）；失败即启动失败。
	PreRun  *CommandStep
	Argv    []string
	WorkDir string
	Env     map[string]string
	EnvFile string
}

// CommandStep 是 argv 形式的前置步骤。
type CommandStep struct {
	Argv []string
}

// Stop 强制终止指定服务进程，并立即将状态置为 StatusStopped。
//
// 注意：
//   - 进程未启动或已退出时调用为空操作
//   - bumpGeneration 保证旧退出回调不会在 Stop 后把状态覆盖为 failed
func (m *Manager) Stop(serviceID string) {
	m.bumpGeneration(serviceID)
	m.mu.Lock()
	r := m.runners[serviceID]
	delete(m.runners, serviceID)
	delete(m.runtimes, serviceID)
	delete(m.launchdDeps, serviceID)
	delete(m.readiness, serviceID)
	delete(m.backgrounded, serviceID)
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

// setStatus 线程安全地更新服务状态；状态发生实际跃迁时在锁外通知 onStatusChange。
func (m *Manager) setStatus(id string, st model.ServiceStatus) {
	m.mu.Lock()
	prev := m.status[id]
	m.status[id] = st
	cb := m.onStatusChange
	m.mu.Unlock()
	// 回调必须在锁外调用：回调可能重入 Manager（如调用 m.Status(id)），持锁调用会自死锁。
	// 只在状态真正跃迁（prev != st）时才通知，避免幂等的重复 setStatus（如已 running 时
	// 重复 StartDeployment 的跳过路径）产生冗余事件帧。
	if cb != nil && prev != st {
		cb(id, st)
	}
}

// setReadiness 登记 deployment 的就绪探测配置。nil 表示「进程起来即就绪」。
func (m *Manager) setReadiness(id string, p *model.ReadinessProbe) {
	m.mu.Lock()
	if p == nil {
		delete(m.readiness, id)
	} else {
		m.readiness[id] = p
	}
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
	m.setReadiness(dep.ID, dep.Readiness)
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLaunchd {
		return m.startLaunchdDeployment(dep)
	}
	if err := m.startByID(dep.ID, deploymentToSpec(dep)); err != nil {
		return err
	}
	m.recordRuntime(dep.ID, runtimeTypeOf(dep))
	return nil
}

// StartDeploymentSpec 以调用方提供的 ProcessSpec 启动 deployment（language runtime 用）。
//
// 与 StartDeployment 的区别：不从 deployment 扁平字段推导 spec；runtime 类型记录照旧。
func (m *Manager) StartDeploymentSpec(dep model.Deployment, spec ProcessSpec) error {
	if dep.IsReadOnly() {
		return nil
	}
	m.setReadiness(dep.ID, dep.Readiness)
	if err := m.startByID(dep.ID, spec); err != nil {
		return err
	}
	m.recordRuntime(dep.ID, runtimeTypeOf(dep))
	return nil
}

// StopDeployment 停止指定 deployment 的进程。
// 与 Stop/Status 等方法共用同一 runners map，以 deploymentID 为键，语义上区分 service 和 deployment 两个命名空间。
func (m *Manager) StopDeployment(deploymentID string) {
	m.mu.Lock()
	runtimeType := m.runtimes[deploymentID]
	dep := m.launchdDeps[deploymentID]
	m.mu.Unlock()
	if runtimeType == model.RuntimeTypeLaunchd {
		if err := runLaunchdStop(context.Background(), os.Getuid(), dep); err != nil {
			m.emitLog(deploymentID, "ERROR", "stderr", "停止 launchd 服务失败: "+err.Error())
		}
		m.bumpGeneration(deploymentID)
		m.mu.Lock()
		r := m.runners[deploymentID]
		delete(m.runners, deploymentID)
		delete(m.runtimes, deploymentID)
		delete(m.launchdDeps, deploymentID)
		delete(m.readiness, deploymentID)
		delete(m.backgrounded, deploymentID)
		m.mu.Unlock()
		if r != nil {
			r.Stop()
		}
		m.setStatus(deploymentID, model.StatusStopped)
		return
	}
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

// DeploymentPGID 返回 deployment 进程的进程组 ID（0 表示未运行或不可用）。
func (m *Manager) DeploymentPGID(deploymentID string) int {
	m.mu.Lock()
	runner, ok := m.runners[deploymentID]
	m.mu.Unlock()
	if !ok || runner == nil {
		return 0
	}
	return runner.ProcessGroupID()
}

// IsDeploymentActive 报告 deployment 是否已启动且未停止。
func (m *Manager) IsDeploymentActive(deploymentID string) bool {
	return m.IsActive(deploymentID)
}

// DeploymentArgv 返回 deployment 进程的启动 argv 副本（用于 prearm 语言从 --listen 反解端口）；
// 未运行或非 argv 直启时返回 nil。
func (m *Manager) DeploymentArgv(deploymentID string) []string {
	m.mu.Lock()
	runner, ok := m.runners[deploymentID]
	m.mu.Unlock()
	if !ok || runner == nil {
		return nil
	}
	return runner.Argv()
}

// DeploymentStderrTail 返回 deployment 进程最近的 stderr 行（用于运行时就绪信号解析）；
// 未运行时返回 nil。
func (m *Manager) DeploymentStderrTail(deploymentID string) []string {
	m.mu.Lock()
	runner, ok := m.runners[deploymentID]
	m.mu.Unlock()
	if !ok || runner == nil {
		return nil
	}
	return runner.StderrTail()
}

// StartProcess 以指定 id 为键启动一个进程，是不绑定 deployment 概念的低阶启动入口。
//
// 供 collector 等内部子系统直接启动采集进程使用；日志以 id 作为 DeploymentID 归属。
// 与 StartDeployment 共用 startByID 底座与同一 runners 命名空间。
func (m *Manager) StartProcess(id string, spec ProcessSpec) error {
	m.setReadiness(id, nil)
	return m.startByID(id, spec)
}

func (m *Manager) recordRuntime(id string, runtimeType model.RuntimeType) {
	m.mu.Lock()
	m.runtimes[id] = runtimeType
	m.mu.Unlock()
}

func (m *Manager) recordLaunchdDeployment(dep model.Deployment) {
	m.mu.Lock()
	m.runtimes[dep.ID] = model.RuntimeTypeLaunchd
	m.launchdDeps[dep.ID] = dep
	m.mu.Unlock()
}

func (m *Manager) startLaunchdDeployment(dep model.Deployment) error {
	m.setStatus(dep.ID, model.StatusStarting)
	if err := runLaunchdStart(context.Background(), os.Getuid(), dep); err != nil {
		m.setStatus(dep.ID, model.StatusFailed)
		m.emitLog(dep.ID, "ERROR", "stderr", "启动 launchd 服务失败: "+err.Error())
		return err
	}
	m.recordLaunchdDeployment(dep)
	if dep.Logs != nil && dep.Logs.Type == model.LogKindMacOSLog && dep.Logs.Target != "" {
		// launchd 自身不把 stdout/stderr 交给当前进程；默认用 macOS unified log
		// 拉起轻量采集进程，让日志面板仍按 deployment ID 接收实时日志。
		if err := m.startByID(dep.ID, ProcessSpec{Command: macOSLogStreamCommand(dep.Logs.Target)}); err != nil {
			m.setStatus(dep.ID, model.StatusFailed)
			m.emitLog(dep.ID, "ERROR", "stderr", "启动 macOS 日志采集失败: "+err.Error())
			return err
		}
		m.recordLaunchdDeployment(dep)
		return nil
	}
	m.setStatus(dep.ID, model.StatusRunning)
	m.emitLog(dep.ID, "INFO", "stdout", "launchd 服务已启动")
	return nil
}

// startByID 以指定的 id 为键启动进程，是所有启动路径的核心实现。
func (m *Manager) startByID(id string, spec ProcessSpec) error {
	// 进入启动决策前先对账，避免旧 runner 对象残留导致重启请求被静默跳过。
	m.Reconcile(id)

	m.mu.Lock()
	if m.status[id] == model.StatusStarting {
		m.mu.Unlock()
		return nil
	}
	if old, ok := m.runners[id]; ok {
		if old.ProcessGroupAlive() {
			m.mu.Unlock()
			return nil
		}
		delete(m.runners, id)
		delete(m.runtimes, id)
		delete(m.launchdDeps, id)
		delete(m.backgrounded, id)
	}
	m.mu.Unlock()

	m.setStatus(id, model.StatusStarting)
	gen := m.bumpGeneration(id)

	var r *Runner
	r = NewRunner(RunnerConfig{
		Command: spec.Command,
		PreRun:  spec.PreRun,
		Argv:    append([]string{}, spec.Argv...),
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
		OnExit: func(info ExitInfo) {
			m.handleRunnerExit(id, r, gen, info)
		},
	})

	m.mu.Lock()
	m.runners[id] = r
	m.mu.Unlock()

	if err := r.Start(); err != nil {
		info, ok := r.ExitInfo()
		if !ok {
			info = ExitInfo{Reason: ExitReasonStartFailed, ExitCode: -1, Error: err.Error()}
		}
		m.mu.Lock()
		if m.runners[id] == r {
			delete(m.runners, id)
			delete(m.runtimes, id)
			delete(m.launchdDeps, id)
			delete(m.readiness, id)
			delete(m.backgrounded, id)
			m.status[id] = model.StatusFailed
		}
		m.mu.Unlock()
		m.emitStartFailure(id, info)
		return err
	}

	if r.ProcessGroupAlive() {
		m.mu.Lock()
		probe := m.readiness[id]
		m.mu.Unlock()
		if probe == nil {
			// 无就绪探针：维持既有行为，进程组存活即视为运行中。
			m.setStatus(id, model.StatusRunning)
		} else {
			// 有探针：保持 starting，异步探测通过才转 running，超时转 failed。
			// gen 防止探测期间发生 stop/restart 导致回写到新一代进程。
			log.Printf("[SuperDev] readiness probe started: dep=%s type=%s target=%s", id, probe.Type, probe.Target)
			go m.awaitReadiness(id, gen, probe)
		}
	}

	return nil
}

// awaitReadiness 异步等待 deployment 就绪，把状态从 starting 翻为 running / failed。
//
// 注意：
//   - 仅当 generation 未变（期间无 stop/restart）时才回写状态，避免污染新一代进程
//   - 进程在探测期间已死则不强行翻 running，交由 handleRunnerExit 处理
func (m *Manager) awaitReadiness(id string, gen uint64, probe *model.ReadinessProbe) {
	err := ProbeReady(context.Background(), probe)
	if m.generation(id) != gen {
		return // 期间发生了 stop/restart，丢弃本次结果。
	}
	if err != nil {
		m.setStatus(id, model.StatusFailed)
		m.emitLog(id, "ERROR", "stderr", "就绪探测失败: "+err.Error())
		log.Printf("[SuperDev] readiness probe failed: dep=%s cause=%v", id, err)
		return
	}
	m.mu.Lock()
	r := m.runners[id]
	m.mu.Unlock()
	if r == nil || !r.ProcessGroupAlive() {
		// 探测通过但进程已退出：状态交由 handleRunnerExit 决定，这里不覆盖。
		return
	}
	m.setStatus(id, model.StatusRunning)
	log.Printf("[SuperDev] readiness probe passed, now running: dep=%s", id)
}

// handleRunnerExit 处理 Runner 的唯一 Wait 结果。
//
// 如果 shell 已退出但进程组仍有后台子进程存活，状态保持 running；
// 只有进程组整体死亡时，才根据 ExitInfo 翻 stopped/failed 并清理 runner。
func (m *Manager) handleRunnerExit(id string, r *Runner, gen uint64, info ExitInfo) {
	if m.generation(id) != gen {
		return
	}
	groupAlive := r.ProcessGroupAlive()

	var failed bool
	m.mu.Lock()
	if m.runners[id] != r {
		m.mu.Unlock()
		return
	}
	if groupAlive {
		if m.readiness[id] == nil {
			m.status[id] = model.StatusRunning
		}
		m.backgrounded[id] = true
		m.mu.Unlock()
		return
	}
	delete(m.runners, id)
	delete(m.runtimes, id)
	delete(m.launchdDeps, id)
	delete(m.readiness, id)
	delete(m.backgrounded, id)
	failed = info.ExitCode != 0 || info.Signaled
	if failed {
		m.status[id] = model.StatusFailed
	} else {
		m.status[id] = model.StatusStopped
	}
	m.mu.Unlock()

	if failed {
		m.emitExitFailure(id, info)
	}
}

// emitStartFailure 发出启动失败事件：命令未成功 spawn，没有 exit code。
func (m *Manager) emitStartFailure(id string, info ExitInfo) {
	m.emitLog(id, "ERROR", "stderr", "启动失败: "+info.Error)
}

// emitExitFailure 发出带结构化证据的失败日志：退出码或信号 + stderr 尾部。
func (m *Manager) emitExitFailure(id string, info ExitInfo) {
	if info.Signaled {
		m.emitLog(id, "ERROR", "stderr", fmt.Sprintf("进程被信号终止：%s", info.Signal))
	} else {
		m.emitLog(id, "ERROR", "stderr", fmt.Sprintf("进程异常退出，退出码 %d", info.ExitCode))
	}
	for _, line := range info.StderrTail {
		m.emitLog(id, "ERROR", "stderr", "  | "+line)
	}
}

// deploymentToSpec 将 Deployment 字段映射为 ProcessSpec。
// local deployment 用自身 Command/WorkDir/Env；command runtime 显式提供
// Executable 时用结构化 argv 直启；
// remote deployment 用 StartCommand 作为命令，本机 env/workDir 不透传。
func deploymentToSpec(dep model.Deployment) ProcessSpec {
	cmd := dep.Command
	workDir := dep.WorkDir
	env := dep.Env
	envFile := dep.EnvFile
	var argv []string
	if dep.Location == model.LocationRemote {
		cmd = dep.StartCommand
		workDir = ""
		env = nil
		envFile = ""
	} else if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeCommand && dep.Runtime.Executable != "" {
		// 机器生成的跨平台命令不能依赖 cmd/sh 的引号规则；保留 Command 供展示和旧客户端兼容，
		// 实际启动则把可执行文件与参数逐项交给操作系统。
		argv = append([]string{dep.Runtime.Executable}, dep.Runtime.Args...)
	}
	return ProcessSpec{Command: cmd, Argv: argv, WorkDir: workDir, Env: env, EnvFile: envFile}
}

func runtimeTypeOf(dep model.Deployment) model.RuntimeType {
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		return dep.Runtime.Type
	}
	return model.RuntimeTypeCommand
}
