// manager.go 实现端口镜像的 reconcile 核心：把「本机应该有哪些
// 127.0.0.1:port → 远端 host:port 转发」收敛到实际隧道转发。
//
// 职责：
//   - 期望态计算：对每台 DevMachineMode 主机，帧内 Health ∈ {running,healthy,
//     restarting} 的实例，其每个声明端口 → 一条应存在的转发
//   - 收敛（level-triggered）：每轮 ApplyNodes/ReconcileNow/Retry 做全量 diff，
//     建缺失、拆多余；冲突/失败带冷却记忆，避免每帧风暴 lsof/SSH
//   - 对外快照：Statuses/Subscribe 暴露每条「host × 端口」镜像状态给 UI
//
// 边界：
//   - 不做端口猜测：期望端口只来自帧内 InstanceStatus.Ports（共享层声明）
//   - 不做反向镜像：只建 本机→远端 的入向转发，不管远端来连本机
//   - 不持久化状态：全部由帧重建——agent 重启后第一帧就地重算，无需落盘
//   - 不自己建 SSH、不自己跑 lsof：分别委托注入的 TunnelController / Occupier
package portmirror

import (
	"errors"
	"log"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// MirrorState 是单条端口镜像的状态机取值。
type MirrorState string

const (
	// MirrorStatePending 表示期望存在但尚未建立（隧道连接中/等待重试）。
	MirrorStatePending MirrorState = "pending"
	// MirrorStateActive 表示转发已建立。
	MirrorStateActive MirrorState = "active"
	// MirrorStateConflict 表示本机端口被占（port_mirror_conflict）。
	MirrorStateConflict MirrorState = "conflict"
	// MirrorStateFailed 表示 SSH/其他错误（脱敏码）或重复端口声明。
	MirrorStateFailed MirrorState = "failed"
)

const (
	// errCodeConflict 是 conflict 态固定的对外 Error 码。
	errCodeConflict = "port_mirror_conflict"
	// errCodeDuplicate 标记同 host 同端口被多个 deployment 声明时的落败者。
	errCodeDuplicate = "duplicate_port_declaration"
	// errCodeTargetUnavailable 表示无法合成该 host 的 SSH 目标（Deps.Target 报错）。
	errCodeTargetUnavailable = "mirror_target_unavailable"
)

// defaultCooldown 是 conflict/failed 记忆的默认冷却时长。
//
// 为什么要冷却：LookupOccupier 会 shell 出 lsof/ps，EnsureConnected/EnsureForward
// 会走真实 SSH。帧每 ≤5s 一发，若每帧都对已知冲突/失败端口重试，就会把 lsof 和
// SSH 打成风暴。冷却让一条稳定失败在 30s 内只尝试一次；用户点 Retry 可立即绕过。
const defaultCooldown = 30 * time.Second

// MirrorStatus 是一条「host × 端口」镜像的对外快照。
type MirrorStatus struct {
	HostID       string      `json:"host_id"`
	HostName     string      `json:"host_name"`
	DeploymentID string      `json:"deployment_id"`
	ServiceName  string      `json:"service_name"`
	Port         int         `json:"port"`
	State        MirrorState `json:"state"`
	Error        string      `json:"error,omitempty"`    // conflict 固定 "port_mirror_conflict"；failed 为脱敏码/重复声明码
	Occupier     *Occupier   `json:"occupier,omitempty"` // 仅 conflict 且识别成功
	UpdatedAt    time.Time   `json:"updated_at"`
}

// TunnelController 抽象 tunnel.Manager 中镜像用到的子集，接口化以便单测注入假件。
type TunnelController interface {
	EnsureConnected(t tunnel.Target) (int, error)
	EnsureForward(hostID string, port int) error
	DropForward(hostID string, port int)
}

// Deps 是 Manager 的全部外部依赖，接口化以便单测注入假件。
type Deps struct {
	Hosts    func() []model.Host                        // 全部主机（Manager 自己过滤 DevMachineMode）
	Target   func(hostID string) (tunnel.Target, error) // host+agent 合成 SSH 目标
	Tunnels  TunnelController
	Occupier func(port int, resolve ManagedResolver) (*Occupier, error)
	Resolve  ManagedResolver
	// KnownDeployments 返回本控制面已知的 deployment id 集合；nil 表示不过滤。
	// 每轮 reconcile 现取而非装配期快照——装配期快照会漏掉装配之后新增的项目，
	// nodeRegistry.Start 一次性算 coverage 就踩过这个坑（新增主机后必须重启控制面）。
	KnownDeployments func() map[string]struct{}
}

// mirrorKey 是内部状态条目的身份：host + deployment + port。
//
// 为什么带 deploymentID：同 host 同端口可能被两个 deployment 同时声明，赢家
// 与落败者是两条不同的状态条目（一 active、一 failed），必须能各自寻址。
type mirrorKey struct {
	hostID       string
	deploymentID string
	port         int
}

// fwdKey 是转发身份：一台 host 上一个本机端口只对应一条转发。
type fwdKey struct {
	hostID string
	port   int
}

// mirrorEntry 是 loop 独占的可变状态条目（外部只读到它派生的 MirrorStatus）。
type mirrorEntry struct {
	hostID       string
	hostName     string
	deploymentID string
	serviceName  string
	port         int
	state        MirrorState
	errCode      string
	occupier     *Occupier
	lastAttempt  time.Time // 冷却记忆锚点：最近一次建立尝试时刻
	forwardUp    bool      // 是否已在隧道上建立了该端口转发
	updatedAt    time.Time
}

// expInfo 是一轮期望态里单个 key 的静态信息。
type expInfo struct {
	hostName     string
	serviceName  string
	deploymentID string
	hostID       string
	port         int
	// duplicate=true 表示该 key 是同 host 同端口的落败者（更大的 deploymentID），
	// 不获得转发，标 failed + duplicate_port_declaration。
	duplicate bool
}

// Manager 是长生命周期的镜像 reconcile 组件。
//
// 并发模型：ApplyNodes/ReconcileNow/Retry 都只写「入站状态 + 唤醒信号」，真正的
// reconcile 全部在单个 loop goroutine 里串行执行——entries 由 loop 独占，无需加锁，
// 也就不存在两轮 reconcile 相互竞态。对外快照走独立的 outMu，与 reconcile 逻辑解耦：
// loop 在每轮末尾把算好的快照发布到 outMu 保护的字段，Statuses/Subscribe 只读该字段，
// 既不碰 entries、也不会因为某轮 reconcile 里的慢 lsof 而被阻塞。
type Manager struct {
	deps     Deps
	cooldown time.Duration // 可被测试改写；生产恒为 defaultCooldown

	// inMu 保护入站：最新帧 + 重试请求。外部 goroutine 写，loop 读。
	inMu        sync.Mutex
	latestFrame []nodetransport.NodeStatus
	retryReqs   map[fwdKey]struct{}

	wake       chan struct{} // 缓冲 1 的唤醒信号（latest-wins）
	done       chan struct{}
	loopDone   chan struct{} // loop 退出时关闭
	closeOnce  sync.Once
	closedFlag atomic.Bool

	// entries 由 loop 独占，仅 reconcile goroutine 访问 → 无锁。
	entries       map[mirrorKey]*mirrorEntry
	lastBroadcast []MirrorStatus // loop 独占：上轮广播过的快照，用于变更检测
	// filteredOnce 记住已就「被管辖过滤掉」打过日志的 deployment id，
	// 避免每轮 reconcile（5s 一次）重复刷同一条。只增不减：这些 id 通常
	// 长期属于别的控制面，反复提醒没有价值。
	filteredOnce map[string]struct{}

	// outMu 保护对外快照与订阅集合。
	outMu     sync.Mutex
	snapshot  []MirrorStatus
	subs      map[int]chan []MirrorStatus
	subSeq    int
	subClosed bool

	// onReconcileForTest 仅测试用：每轮 reconcile 结束回调，作为同步点。生产为 nil。
	onReconcileForTest func()
}

// NewManager 创建并启动 Manager 的 reconcile 循环。
//
// 参数：
//   - deps: 全部外部依赖（主机列表、SSH 目标合成、隧道控制、占用者识别）
//
// 返回：
//   - 已启动 loop 的 Manager；使用完必须 Close 以拆除转发并回收 goroutine
func NewManager(deps Deps) *Manager {
	m := &Manager{
		deps:          deps,
		cooldown:      defaultCooldown,
		retryReqs:     map[fwdKey]struct{}{},
		wake:          make(chan struct{}, 1),
		done:          make(chan struct{}),
		loopDone:      make(chan struct{}),
		entries:       map[mirrorKey]*mirrorEntry{},
		lastBroadcast: []MirrorStatus{},
		filteredOnce:  map[string]struct{}{},
		subs:          map[int]chan []MirrorStatus{},
	}
	if deps.KnownDeployments == nil {
		log.Printf("[SuperDev] portmirror: KnownDeployments 未配置，不做管辖过滤——生产环境不应出现")
	}
	go m.loop()
	return m
}

// ApplyNodes 投喂最新节点帧（本机 registry 订阅转发进来），触发一轮 reconcile。
//
// 只落最新帧 + 唤醒信号即返回，绝不阻塞调用方（registry 慢消费者丢帧惯例）。
func (m *Manager) ApplyNodes(nodes []nodetransport.NodeStatus) {
	if m.closedFlag.Load() {
		return
	}
	m.inMu.Lock()
	m.latestFrame = nodes
	m.inMu.Unlock()
	m.signalWake()
}

// ReconcileNow 立即按当前缓存的帧重算（host 开关变更、重试按钮触发）。
func (m *Manager) ReconcileNow() {
	if m.closedFlag.Load() {
		return
	}
	m.signalWake()
}

// Retry 清除指定镜像的 conflict/failed 记忆并立即重试。
//
// 参数：
//   - hostID/port: 目标本机端口；清除该 host+port 上全部条目（含重复落败者）的冷却记忆
func (m *Manager) Retry(hostID string, port int) {
	if m.closedFlag.Load() {
		return
	}
	m.inMu.Lock()
	m.retryReqs[fwdKey{hostID: hostID, port: port}] = struct{}{}
	m.inMu.Unlock()
	m.signalWake()
}

// Statuses 返回全部镜像状态快照（含 conflict/failed），按 host+port 排序。
func (m *Manager) Statuses() []MirrorStatus {
	m.outMu.Lock()
	defer m.outMu.Unlock()
	if len(m.snapshot) == 0 {
		return []MirrorStatus{}
	}
	out := make([]MirrorStatus, len(m.snapshot))
	copy(out, m.snapshot)
	return out
}

// Subscribe 订阅状态变更：任何一条镜像状态变化后收到完整快照。满则丢帧（消费方以最新为准）。
//
// 返回：
//   - 快照 channel，订阅即刻收到一次当前快照作为基线
//   - 取消订阅函数
func (m *Manager) Subscribe() (<-chan []MirrorStatus, func()) {
	m.outMu.Lock()
	defer m.outMu.Unlock()
	ch := make(chan []MirrorStatus, 8)
	if m.subClosed {
		close(ch)
		return ch, func() {}
	}
	id := m.subSeq
	m.subSeq++
	m.subs[id] = ch
	// 立即推一次当前快照（可能为空），消费者拿到基线，与 registry Subscribe 惯例一致。
	initial := m.snapshot
	if initial == nil {
		initial = []MirrorStatus{}
	}
	ch <- initial // 新建缓冲 channel，必不阻塞
	return ch, func() {
		m.outMu.Lock()
		defer m.outMu.Unlock()
		if existing, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(existing)
		}
	}
}

// Close 拆除全部转发并停止 reconcile。幂等。
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.closedFlag.Store(true)
		close(m.done)
		<-m.loopDone // 等待 loop 拆除全部转发并退出，避免 goroutine 泄漏
		m.outMu.Lock()
		m.subClosed = true
		for id, ch := range m.subs {
			delete(m.subs, id)
			close(ch)
		}
		m.outMu.Unlock()
	})
}

// signalWake 非阻塞地投递唤醒信号；已有待处理信号时丢弃（latest-wins）。
func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// loop 是唯一的 reconcile goroutine。
//
// 为什么单 goroutine 串行：entries 只被本 goroutine 访问，天然无锁、无两轮 reconcile
// 竞态；ApplyNodes/ReconcileNow/Retry 只投递信号，互不阻塞。
func (m *Manager) loop() {
	defer close(m.loopDone)
	log.Printf("[SuperDev] portmirror: reconcile 循环启动")
	for {
		// 优先响应 Close，保证关闭及时（不被残留 wake 拖住）。
		select {
		case <-m.done:
			m.teardownAll()
			return
		default:
		}
		select {
		case <-m.done:
			m.teardownAll()
			return
		case <-m.wake:
			m.reconcile()
			if m.onReconcileForTest != nil {
				m.onReconcileForTest()
			}
		}
	}
}

// teardownAll 在 Close 时拆除全部已建立转发，清空状态。仅 loop 调用。
func (m *Manager) teardownAll() {
	count := 0
	for _, e := range m.entries {
		if e.forwardUp {
			m.deps.Tunnels.DropForward(e.hostID, e.port)
			count++
		}
	}
	m.entries = map[mirrorKey]*mirrorEntry{}
	log.Printf("[SuperDev] portmirror: 已关闭，拆除全部转发 count=%d", count)
}

// reconcile 执行一轮全量收敛。仅 loop 调用。
func (m *Manager) reconcile() {
	m.inMu.Lock()
	frame := m.latestFrame
	var retries map[fwdKey]struct{}
	if len(m.retryReqs) > 0 {
		retries = m.retryReqs
		m.retryReqs = map[fwdKey]struct{}{}
	}
	m.inMu.Unlock()

	now := time.Now()
	m.applyRetries(retries)

	// 当前全部主机（Deps.Hosts 返回所有，这里按 DevMachineMode 过滤在期望态计算内完成）。
	hosts := map[string]model.Host{}
	for _, h := range m.deps.Hosts() {
		hosts[h.ID] = h
	}

	var known map[string]struct{}
	if m.deps.KnownDeployments != nil {
		known = m.deps.KnownDeployments()
	}
	expected := computeExpected(frame, hosts, known, m.filteredOnce)

	m.teardownUnexpected(expected, frame, hosts)
	m.converge(expected, now)

	m.publishIfChanged()
}

// applyRetries 清除被 Retry 指定的 host+port 条目的冷却记忆，使其本轮被视为「到期」。
func (m *Manager) applyRetries(retries map[fwdKey]struct{}) {
	if len(retries) == 0 {
		return
	}
	for k, e := range m.entries {
		if _, ok := retries[fwdKey{hostID: k.hostID, port: k.port}]; !ok {
			continue
		}
		// 冲突/失败重置为 pending，本轮 isDue 判定为真，立即重试；active 无需动。
		e.lastAttempt = time.Time{}
		if e.state == MirrorStateConflict || e.state == MirrorStateFailed {
			e.state = MirrorStatePending
			e.errCode = ""
			e.occupier = nil
		}
	}
}

// teardownUnexpected 拆除并删除所有不再属于期望态的条目。
func (m *Manager) teardownUnexpected(expected map[mirrorKey]expInfo, frame []nodetransport.NodeStatus, hosts map[string]model.Host) {
	for k, e := range m.entries {
		if _, ok := expected[k]; ok {
			continue
		}
		if e.forwardUp {
			m.deps.Tunnels.DropForward(e.hostID, e.port)
			reason := teardownReason(k, frame, hosts)
			log.Printf("[SuperDev] portmirror: %s %s(%s) 127.0.0.1:%d ⇄ %s:%d", reason, e.serviceName, e.deploymentID, e.port, e.hostName, e.port)
		}
		// 期望消失即清除记忆（含冷却）——满足 spec：期望消失/开关关闭时清除记忆。
		delete(m.entries, k)
	}
}

// converge 建立/维持期望态里所有条目的转发。
func (m *Manager) converge(expected map[mirrorKey]expInfo, now time.Time) {
	// 先确定哪些 host 本轮有「到期的转发拥有者」需要建连，避免为纯冷却态的 host 白建连。
	hostDue := map[string]bool{}
	for k, info := range expected {
		if info.duplicate {
			continue
		}
		if m.isDue(m.entries[k], now) {
			hostDue[k.hostID] = true
		}
	}

	// 每台需要的 host 只 EnsureConnected 一次，缓存本轮结果（脱敏码，""=成功）。
	connCode := map[string]string{}
	for hostID := range hostDue {
		target, err := m.deps.Target(hostID)
		if err != nil {
			connCode[hostID] = errCodeTargetUnavailable
			continue
		}
		if _, err := m.deps.Tunnels.EnsureConnected(target); err != nil {
			connCode[hostID] = tunnel.PublicError(err)
		} else {
			connCode[hostID] = ""
		}
	}

	// 稳定顺序处理，日志与状态可预期。
	keys := make([]mirrorKey, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })

	for _, k := range keys {
		info := expected[k]
		e := m.ensureEntry(k, info, now)

		if info.duplicate {
			m.markDuplicate(e, now)
			continue
		}

		if !m.isDue(e, now) {
			// 冷却期内保持原状态，不重试（防 lsof/SSH 风暴）。
			continue
		}

		code, attempted := connCode[k.hostID]
		if attempted && code != "" {
			m.markConnectFailed(e, code, now)
			continue
		}
		m.attemptForward(e, now)
	}
}

// ensureEntry 取出或新建条目，并刷新展示字段。
func (m *Manager) ensureEntry(k mirrorKey, info expInfo, now time.Time) *mirrorEntry {
	e := m.entries[k]
	if e == nil {
		e = &mirrorEntry{
			hostID:       k.hostID,
			deploymentID: k.deploymentID,
			port:         k.port,
			hostName:     info.hostName,
			serviceName:  info.serviceName,
			state:        MirrorStatePending,
			updatedAt:    now,
		}
		m.entries[k] = e
		return e
	}
	e.hostName = info.hostName
	e.serviceName = info.serviceName
	return e
}

// markDuplicate 把重复端口的落败者标为 failed + duplicate_port_declaration。
//
// 冷却记忆同样适用：这是稳定失败，不应每帧重算重打日志。仅在状态跃迁时打一次。
func (m *Manager) markDuplicate(e *mirrorEntry, now time.Time) {
	if e.forwardUp {
		// 极端：曾是赢家、现降级为落败者（更小 deploymentID 出现），先拆掉它的转发。
		m.deps.Tunnels.DropForward(e.hostID, e.port)
		e.forwardUp = false
	}
	if e.state == MirrorStateFailed && e.errCode == errCodeDuplicate {
		return // 已是该态，保持（冷却记忆：不重复处理/打日志）
	}
	e.state = MirrorStateFailed
	e.errCode = errCodeDuplicate
	e.occupier = nil
	e.lastAttempt = now
	e.updatedAt = now
	log.Printf("[SuperDev] portmirror: 建立失败 code=%s host=%s port=%d", errCodeDuplicate, e.hostID, e.port)
}

// markConnectFailed 把 EnsureConnected/Target 失败投影到单个端口条目。
//
// EnsureConnected 失败会命中该 host 全部到期端口——每个都走这里标 failed + 冷却。
func (m *Manager) markConnectFailed(e *mirrorEntry, code string, now time.Time) {
	transition := e.state != MirrorStateFailed || e.errCode != code
	if e.forwardUp {
		m.deps.Tunnels.DropForward(e.hostID, e.port)
		e.forwardUp = false
	}
	e.state = MirrorStateFailed
	e.errCode = code
	e.occupier = nil
	e.lastAttempt = now
	e.updatedAt = now
	if transition {
		log.Printf("[SuperDev] portmirror: 建立失败 code=%s host=%s port=%d", code, e.hostID, e.port)
	}
}

// attemptForward 在已连接的隧道上建立该端口转发，并按结果落状态。
func (m *Manager) attemptForward(e *mirrorEntry, now time.Time) {
	prevState := e.state
	e.lastAttempt = now
	err := m.deps.Tunnels.EnsureForward(e.hostID, e.port)
	switch {
	case err == nil:
		e.forwardUp = true
		e.occupier = nil
		e.errCode = ""
		if prevState != MirrorStateActive {
			e.state = MirrorStateActive
			e.updatedAt = now
			// 首次建立（或从失败/冲突恢复）才打日志，稳态每帧重入不打。
			log.Printf("[SuperDev] portmirror: 已镜像 %s(%s) 127.0.0.1:%d ⇄ %s:%d", e.serviceName, e.deploymentID, e.port, e.hostName, e.port)
		}
	case errors.Is(err, tunnel.ErrLocalPortBusy):
		e.forwardUp = false
		e.errCode = errCodeConflict
		// 冲突时就地识别占用者。同步 lsof 由冷却记忆兜底：本条目下次到期在 cooldown 之后。
		e.occupier = m.lookupOccupier(e.port)
		if prevState != MirrorStateConflict {
			e.state = MirrorStateConflict
			e.updatedAt = now
			pid := 0
			if e.occupier != nil {
				pid = e.occupier.PID
			}
			log.Printf("[SuperDev] portmirror: 端口冲突 host=%s port=%d occupier_pid=%d", e.hostID, e.port, pid)
		}
	default:
		code := tunnel.PublicError(err)
		transition := e.state != MirrorStateFailed || e.errCode != code
		e.forwardUp = false
		e.occupier = nil
		e.state = MirrorStateFailed
		e.errCode = code
		if transition {
			e.updatedAt = now
			log.Printf("[SuperDev] portmirror: 建立失败 code=%s host=%s port=%d", code, e.hostID, e.port)
		}
	}
}

// isDue 判断条目本轮是否应该尝试建立。
//
// active/pending 恒到期：active 每轮重入 EnsureConnected+EnsureForward 是刻意的——
// Task 5 记录过一个良性竞态（pin 轮换时转发可能落在正在关闭的隧道上），只有靠每轮
// 幂等重加期望转发才能自愈；conflict/failed 则受冷却记忆约束，过 cooldown 才重试。
func (m *Manager) isDue(e *mirrorEntry, now time.Time) bool {
	if e == nil {
		return true
	}
	switch e.state {
	case MirrorStateActive, MirrorStatePending:
		return true
	case MirrorStateConflict, MirrorStateFailed:
		return now.Sub(e.lastAttempt) >= m.cooldown
	default:
		return true
	}
}

// lookupOccupier 识别占用本机端口的进程；未注入或失败时返回 nil（冲突照报，详情降级）。
func (m *Manager) lookupOccupier(port int) *Occupier {
	if m.deps.Occupier == nil {
		return nil
	}
	occ, err := m.deps.Occupier(port, m.deps.Resolve)
	if err != nil {
		return nil
	}
	return occ
}

// publishIfChanged 构建本轮快照，与上轮不同才发布并广播。
func (m *Manager) publishIfChanged() {
	snap := m.buildSnapshot()
	if reflect.DeepEqual(snap, m.lastBroadcast) {
		return
	}
	m.lastBroadcast = snap
	m.outMu.Lock()
	m.snapshot = snap
	for _, ch := range m.subs {
		// 非阻塞发送：慢消费者丢帧，以最新为准，绝不拖慢 reconcile。
		select {
		case ch <- snap:
		default:
		}
	}
	m.outMu.Unlock()
}

// buildSnapshot 从 entries 派生排序后的对外快照。
func (m *Manager) buildSnapshot() []MirrorStatus {
	out := make([]MirrorStatus, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, MirrorStatus{
			HostID:       e.hostID,
			HostName:     e.hostName,
			DeploymentID: e.deploymentID,
			ServiceName:  e.serviceName,
			Port:         e.port,
			State:        e.state,
			Error:        e.errCode,
			Occupier:     e.occupier,
			UpdatedAt:    e.updatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(
			mirrorKey{out[i].HostID, out[i].DeploymentID, out[i].Port},
			mirrorKey{out[j].HostID, out[j].DeploymentID, out[j].Port},
		)
	})
	return out
}

// computeExpected 计算期望镜像态。
//
// 参数：
//   - frame: 最新节点状态帧
//   - hosts: 全部主机（本函数内按 DevMachineMode 过滤）
//   - known: 本控制面已知的 deployment id 集合；**nil 表示不过滤**
//   - filteredOnce: 「已就该 id 打过日志」的记忆，由调用方持有并跨轮复用；
//     nil 表示不打过滤日志（测试直接调用时传 nil）
//
// 注意：known 为 nil 时不过滤，是为了兼容大量省略字段构造 Deps 的既有测试；
// 生产装配必须提供它，否则会为本控制面不管理的服务擅自占用本机端口。
//
// 本函数除了写入 filteredOnce 之外无副作用——保持它可被测试直接调用，
// 这也是「记忆」由调用方传入而不是挂在 Manager 上的原因。
func computeExpected(frame []nodetransport.NodeStatus, hosts map[string]model.Host, known map[string]struct{}, filteredOnce map[string]struct{}) map[mirrorKey]expInfo {
	expected := map[mirrorKey]expInfo{}
	claimedPorts := map[fwdKey]struct{}{}

	type cand struct {
		hostID       string
		hostName     string
		serviceName  string
		deploymentID string
		port         int
	}
	var cands []cand
	for _, n := range frame {
		h, ok := hosts[n.HostID]
		if !ok || !h.DevMachineMode {
			continue
		}
		for _, inst := range n.Deployments {
			// 只镜像本控制面自己管理的 deployment：帧口径放宽后会带来本控制面
			// 不认识的实例，为它们建立转发等于擅自占用用户本机端口。
			if known != nil {
				if _, ok := known[inst.DeploymentID]; !ok {
					// 「为什么这个服务没被镜像」是本设计最可能引发的疑问，
					// 必须留下可 grep 的证据；每个 id 只打一次，避免逐轮刷屏。
					if filteredOnce != nil {
						if _, seen := filteredOnce[inst.DeploymentID]; !seen {
							filteredOnce[inst.DeploymentID] = struct{}{}
							log.Printf("[SuperDev] portmirror: 跳过本控制面未管理的 deployment id=%s host=%s ports=%v", inst.DeploymentID, n.HostID, inst.Ports)
						}
					}
					continue
				}
			}
			if !isRunningHealth(inst.Metrics.Health) {
				continue
			}
			for _, p := range inst.Ports {
				if p <= 0 {
					continue
				}
				cands = append(cands, cand{
					hostID:       n.HostID,
					hostName:     hostDisplayName(h, n),
					serviceName:  inst.ServiceName,
					deploymentID: inst.DeploymentID,
					port:         p,
				})
			}
		}
	}
	// 按 host, port, deploymentID 排序，保证每个 (host,port) 第一个候选就是赢家。
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].hostID != cands[j].hostID {
			return cands[i].hostID < cands[j].hostID
		}
		if cands[i].port != cands[j].port {
			return cands[i].port < cands[j].port
		}
		return cands[i].deploymentID < cands[j].deploymentID
	})

	for _, c := range cands {
		k := mirrorKey{hostID: c.hostID, deploymentID: c.deploymentID, port: c.port}
		if _, exists := expected[k]; exists {
			// 同一 deployment 在 Ports 里重复声明了同端口——自我重复不算冲突，跳过。
			continue
		}
		fk := fwdKey{hostID: c.hostID, port: c.port}
		_, claimed := claimedPorts[fk]
		expected[k] = expInfo{
			hostName:     c.hostName,
			serviceName:  c.serviceName,
			deploymentID: c.deploymentID,
			hostID:       c.hostID,
			port:         c.port,
			duplicate:    claimed,
		}
		if !claimed {
			claimedPorts[fk] = struct{}{}
		}
	}
	return expected
}

// teardownReason 判定一条转发为何不再被期望，用于拆除日志。
func teardownReason(k mirrorKey, frame []nodetransport.NodeStatus, hosts map[string]model.Host) string {
	h, ok := hosts[k.hostID]
	if !ok || !h.DevMachineMode {
		return "开关关闭拆除"
	}
	for _, n := range frame {
		if n.HostID != k.hostID {
			continue
		}
		for _, inst := range n.Deployments {
			if inst.DeploymentID != k.deploymentID {
				continue
			}
			if !isRunningHealth(inst.Metrics.Health) {
				return "停止拆除"
			}
			// 实例仍在跑，但这个端口已从声明里移除。
			return "声明移除拆除"
		}
	}
	// 实例整个从帧里消失，按停止处理。
	return "停止拆除"
}

// isRunningHealth 判定健康值是否属于「应有转发」的运行集合。
func isRunningHealth(h model.Health) bool {
	return h == model.HealthRunning || h == model.HealthHealthy || h == model.HealthRestarting
}

// hostDisplayName 优先用配置里的 Host.Name，回退到帧里的名字。
func hostDisplayName(h model.Host, n nodetransport.NodeStatus) string {
	if h.Name != "" {
		return h.Name
	}
	return n.Name
}

// lessKey 定义状态排序：host → port → deploymentID。
func lessKey(a, b mirrorKey) bool {
	if a.hostID != b.hostID {
		return a.hostID < b.hostID
	}
	if a.port != b.port {
		return a.port < b.port
	}
	return a.deploymentID < b.deploymentID
}
