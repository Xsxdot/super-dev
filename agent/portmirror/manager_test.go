// manager_test.go 用假 TunnelController + 假 Occupier 验证镜像 reconcile 状态机——
// 全程不碰真 SSH、不碰真 lsof，只断言状态收敛与副作用调用。
//
// 采用内部测试包（package portmirror）以便注入两个测试专用接缝：
//   - m.cooldown：把 30s 冷却缩短到毫秒级，验证「冷却后帧触发自动重试」路径
//   - m.onReconcileForTest：每轮 reconcile 结束回调，作为「本轮已跑完」的同步点
//
// 两个接缝都只在首次 ApplyNodes 之前写入，与 loop 的读取之间靠 wake channel
// 建立 happens-before，-race 干净。
package portmirror

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// ---- 假件 ----------------------------------------------------------------

type fkey struct {
	host string
	port int
}

// fakeTunnel 是 TunnelController 的线程安全假件：记录调用、可注入错误。
type fakeTunnel struct {
	mu          sync.Mutex
	connErr     map[string]error // hostID -> EnsureConnected 返回的错误
	fwdErr      map[fkey]error   // {host,port} -> EnsureForward 返回的错误
	connCalls   map[string]int
	fwdCalls    map[fkey]int
	dropCalls   map[fkey]int
	established map[fkey]bool
}

func newFakeTunnel() *fakeTunnel {
	return &fakeTunnel{
		connErr:     map[string]error{},
		fwdErr:      map[fkey]error{},
		connCalls:   map[string]int{},
		fwdCalls:    map[fkey]int{},
		dropCalls:   map[fkey]int{},
		established: map[fkey]bool{},
	}
}

func (f *fakeTunnel) EnsureConnected(t tunnel.Target) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connCalls[t.HostID]++
	if err := f.connErr[t.HostID]; err != nil {
		return 0, err
	}
	return 40000, nil
}

func (f *fakeTunnel) EnsureForward(host string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fkey{host, port}
	f.fwdCalls[k]++
	if err := f.fwdErr[k]; err != nil {
		return err
	}
	f.established[k] = true
	return nil
}

func (f *fakeTunnel) DropForward(host string, port int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fkey{host, port}
	f.dropCalls[k]++
	delete(f.established, k)
}

func (f *fakeTunnel) setConnErr(host string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connErr[host] = err
}

func (f *fakeTunnel) setFwdErr(host string, port int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fwdErr[fkey{host, port}] = err
}

func (f *fakeTunnel) connectCalls(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connCalls[host]
}

func (f *fakeTunnel) forwardCalls(host string, port int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fwdCalls[fkey{host, port}]
}

func (f *fakeTunnel) dropped(host string, port int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dropCalls[fkey{host, port}]
}

func (f *fakeTunnel) isEstablished(host string, port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.established[fkey{host, port}]
}

// fakeOccupier 构造一个恒定返回指定 pid 的假 Occupier 查询函数。
func fakeOccupier(pid int) func(int, ManagedResolver) (*Occupier, error) {
	return func(port int, resolve ManagedResolver) (*Occupier, error) {
		return &Occupier{PID: pid, Name: "stealer"}, nil
	}
}

// ---- 测试脚手架 ----------------------------------------------------------

type mirrorHarness struct {
	m        *Manager
	tun      *fakeTunnel
	rec      chan struct{}
	setHosts func([]model.Host)
}

func newHarness(t *testing.T, occ func(int, ManagedResolver) (*Occupier, error)) *mirrorHarness {
	t.Helper()
	tun := newFakeTunnel()
	var mu sync.Mutex
	var hostsVal []model.Host
	getHosts := func() []model.Host {
		mu.Lock()
		defer mu.Unlock()
		return hostsVal
	}
	setHosts := func(h []model.Host) {
		mu.Lock()
		hostsVal = h
		mu.Unlock()
	}
	rec := make(chan struct{}, 256)
	m := NewManager(Deps{
		Hosts:    getHosts,
		Target:   func(id string) (tunnel.Target, error) { return tunnel.Target{HostID: id}, nil },
		Tunnels:  tun,
		Occupier: occ,
		Resolve:  nil,
	})
	// 测试接缝：在首次 ApplyNodes 之前写入，race-free（见文件头注释）。
	m.onReconcileForTest = func() {
		select {
		case rec <- struct{}{}:
		default:
		}
	}
	t.Cleanup(m.Close)
	return &mirrorHarness{m: m, tun: tun, rec: rec, setHosts: setHosts}
}

// waitReconcile 阻塞直到 loop 跑完一轮 reconcile（或超时失败）。
func (h *mirrorHarness) waitReconcile(t *testing.T) {
	t.Helper()
	select {
	case <-h.rec:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 reconcile 超时")
	}
}

// drain 清空遗留的 reconcile 信号，避免读到上一轮的信号。
func (h *mirrorHarness) drain() {
	for {
		select {
		case <-h.rec:
		default:
			return
		}
	}
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("条件未在时限内满足: %s", msg)
}

func devHosts(ids ...string) []model.Host {
	hs := make([]model.Host, 0, len(ids))
	for _, id := range ids {
		hs = append(hs, model.Host{ID: id, Name: id + "-name", DevMachineMode: true})
	}
	return hs
}

// frameHost 构造单 host 单 deployment 的节点帧。
func frameHost(hostID, dep, svc string, health model.Health, ports ...int) []nodetransport.NodeStatus {
	return []nodetransport.NodeStatus{{
		HostID:    hostID,
		Reachable: true,
		Deployments: []model.InstanceStatus{{
			DeploymentID: dep,
			ServiceName:  svc,
			Metrics:      model.InstanceMetrics{Health: health},
			Ports:        ports,
		}},
	}}
}

func findStatus(sts []MirrorStatus, host string, port int) *MirrorStatus {
	for i := range sts {
		if sts[i].HostID == host && sts[i].Port == port {
			return &sts[i]
		}
	}
	return nil
}

func waitForSnapshot(t *testing.T, ch <-chan []MirrorStatus, pred func([]MirrorStatus) bool) []MirrorStatus {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-ch:
			if pred(s) {
				return s
			}
		case <-deadline:
			t.Fatal("等待满足条件的快照超时")
			return nil
		}
	}
}

// drainSnapshots 清空订阅 channel 里遗留的快照，便于随后断言「无新广播」。
func drainSnapshots(ch <-chan []MirrorStatus) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// ---- 测试用例 ------------------------------------------------------------

func TestReconcileEstablishesForRunningPortsOnDevMachineHosts(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts([]model.Host{
		{ID: "A", Name: "host-a", DevMachineMode: true},
		{ID: "B", Name: "host-b", DevMachineMode: false},
	})
	frame := []nodetransport.NodeStatus{
		{HostID: "A", Reachable: true, Deployments: []model.InstanceStatus{
			{DeploymentID: "dep1", ServiceName: "svc1", Metrics: model.InstanceMetrics{Health: model.HealthRunning}, Ports: []int{9100, 9101}},
		}},
		{HostID: "B", Reachable: true, Deployments: []model.InstanceStatus{
			{DeploymentID: "dep2", ServiceName: "svc2", Metrics: model.InstanceMetrics{Health: model.HealthRunning}, Ports: []int{8080}},
		}},
	}
	h.m.ApplyNodes(frame)

	eventually(t, func() bool {
		return h.tun.isEstablished("A", 9100) && h.tun.isEstablished("A", 9101)
	}, "A 的 9100/9101 转发建立")

	// dev-machine host A 只连一次隧道，两路转发复用之。
	if got := h.tun.connectCalls("A"); got != 1 {
		t.Fatalf("host A EnsureConnected 应为 1 次，实际 %d", got)
	}
	// 非 dev-machine host B 零调用。
	if got := h.tun.connectCalls("B"); got != 0 {
		t.Fatalf("host B 不应建立隧道，EnsureConnected=%d", got)
	}
	if got := h.tun.forwardCalls("B", 8080); got != 0 {
		t.Fatalf("host B 不应建立转发，EnsureForward=%d", got)
	}

	sts := h.m.Statuses()
	if len(sts) != 2 {
		t.Fatalf("应有 2 条镜像状态，实际 %d: %+v", len(sts), sts)
	}
	for _, s := range sts {
		if s.HostID != "A" {
			t.Fatalf("状态条目 host 应全为 A，实际 %s", s.HostID)
		}
		if s.State != MirrorStateActive {
			t.Fatalf("端口 %d 应为 active，实际 %s", s.Port, s.State)
		}
	}
}

func TestReconcileTearsDownOnStop(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	eventually(t, func() bool { return h.tun.isEstablished("A", 9100) }, "建立 A:9100")

	// 投喂 stopped 帧 → 应拆除转发、状态条目消失。
	h.drain()
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthStopped, 9100))
	h.waitReconcile(t)

	eventually(t, func() bool {
		return h.tun.dropped("A", 9100) >= 1 && !h.tun.isEstablished("A", 9100)
	}, "拆除 A:9100")
	eventually(t, func() bool { return len(h.m.Statuses()) == 0 }, "状态清空")
}

func TestConflictPathCapturesOccupier(t *testing.T) {
	h := newHarness(t, fakeOccupier(123))
	h.setHosts(devHosts("A"))
	// EnsureForward 返回 wrap 了 ErrLocalPortBusy 的错误 → 冲突态。
	h.tun.setFwdErr("A", 9100, fmt.Errorf("listen 127.0.0.1:9100: %w", tunnel.ErrLocalPortBusy))
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))

	eventually(t, func() bool {
		s := findStatus(h.m.Statuses(), "A", 9100)
		return s != nil && s.State == MirrorStateConflict
	}, "进入 conflict")

	s := findStatus(h.m.Statuses(), "A", 9100)
	if s.Error != "port_mirror_conflict" {
		t.Fatalf("冲突 Error 应为 port_mirror_conflict，实际 %q", s.Error)
	}
	if s.Occupier == nil || s.Occupier.PID != 123 {
		t.Fatalf("冲突应携带 occupier pid=123，实际 %+v", s.Occupier)
	}

	// 冷却期内再投喂同帧：EnsureForward 不应被再次调用（记忆生效，防 lsof/SSH 风暴）。
	before := h.tun.forwardCalls("A", 9100)
	h.drain()
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	h.waitReconcile(t)
	if got := h.tun.forwardCalls("A", 9100); got != before {
		t.Fatalf("冷却期内不应重试 EnsureForward：before=%d after=%d", before, got)
	}

	// Retry 清除记忆并立即重试 → EnsureForward 再次被调用。
	h.drain()
	h.m.Retry("A", 9100)
	h.waitReconcile(t)
	eventually(t, func() bool {
		return h.tun.forwardCalls("A", 9100) > before
	}, "Retry 后重新尝试 EnsureForward")
}

func TestHostSwitchOffTearsDownAll(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100, 9101))
	eventually(t, func() bool {
		return h.tun.isEstablished("A", 9100) && h.tun.isEstablished("A", 9101)
	}, "建立两条")

	// host A 开关关闭 → ReconcileNow → 全部拆除。
	h.setHosts([]model.Host{{ID: "A", Name: "host-a", DevMachineMode: false}})
	h.drain()
	h.m.ReconcileNow()
	h.waitReconcile(t)

	eventually(t, func() bool {
		return h.tun.dropped("A", 9100) >= 1 && h.tun.dropped("A", 9101) >= 1 && len(h.m.Statuses()) == 0
	}, "开关关闭后全部拆除且状态清空")
}

func TestSubscribeEmitsOnChange(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	ch, unsub := h.m.Subscribe()
	defer unsub()

	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))

	got := waitForSnapshot(t, ch, func(s []MirrorStatus) bool {
		return len(s) == 1 && s[0].State == MirrorStateActive && s[0].Port == 9100
	})
	if got[0].HostID != "A" {
		t.Fatalf("快照 host 应为 A，实际 %s", got[0].HostID)
	}
}

func TestReconnectRebuildsSamePorts(t *testing.T) {
	h := newHarness(t, nil)
	// 缩短冷却，验证「连接失败 → 冷却后帧触发重连 → 同端口重建」。
	// 在首次 ApplyNodes 之前写入，race-free。
	h.m.cooldown = 30 * time.Millisecond
	h.setHosts(devHosts("A"))

	// EnsureConnected 先失败 → 该 host 全部端口标 failed。
	h.tun.setConnErr("A", errors.New("dial failed"))
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	eventually(t, func() bool {
		s := findStatus(h.m.Statuses(), "A", 9100)
		return s != nil && s.State == MirrorStateFailed
	}, "连接失败 → failed")

	// 改为成功，等待过冷却期，下一帧到达 → 同端口重建、状态回 active。
	h.tun.setConnErr("A", nil)
	time.Sleep(50 * time.Millisecond)
	h.drain()
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	h.waitReconcile(t)

	eventually(t, func() bool {
		if !h.tun.isEstablished("A", 9100) {
			return false
		}
		s := findStatus(h.m.Statuses(), "A", 9100)
		return s != nil && s.State == MirrorStateActive
	}, "同端口重建、状态回 active")
}

// TestDuplicatePortDeclaration 验证同 host 两 deployment 声明同端口：
// 按 deploymentID 排序，第一个获得转发，其余 failed + duplicate_port_declaration。
func TestDuplicatePortDeclaration(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	frame := []nodetransport.NodeStatus{{
		HostID:    "A",
		Reachable: true,
		Deployments: []model.InstanceStatus{
			{DeploymentID: "dep-b", ServiceName: "svcB", Metrics: model.InstanceMetrics{Health: model.HealthRunning}, Ports: []int{9100}},
			{DeploymentID: "dep-a", ServiceName: "svcA", Metrics: model.InstanceMetrics{Health: model.HealthRunning}, Ports: []int{9100}},
		},
	}}
	h.m.ApplyNodes(frame)

	eventually(t, func() bool {
		return h.tun.isEstablished("A", 9100)
	}, "端口 9100 被建立一次")

	sts := h.m.Statuses()
	if len(sts) != 2 {
		t.Fatalf("应有 2 条状态（赢家+重复），实际 %d: %+v", len(sts), sts)
	}
	var winner, loser *MirrorStatus
	for i := range sts {
		switch sts[i].DeploymentID {
		case "dep-a":
			winner = &sts[i]
		case "dep-b":
			loser = &sts[i]
		}
	}
	if winner == nil || winner.State != MirrorStateActive {
		t.Fatalf("dep-a（deploymentID 最小）应为 active 赢家，实际 %+v", winner)
	}
	if loser == nil || loser.State != MirrorStateFailed || loser.Error != "duplicate_port_declaration" {
		t.Fatalf("dep-b 应为 failed + duplicate_port_declaration，实际 %+v", loser)
	}
	// 只建立一路转发。
	if got := h.tun.forwardCalls("A", 9100); got != 1 {
		t.Fatalf("同端口只应 EnsureForward 一次，实际 %d", got)
	}
}

// TestCloseTearsDownAllForwards 验证 Close 拆除全部转发并停止 loop。
func TestCloseTearsDownAllForwards(t *testing.T) {
	tun := newFakeTunnel()
	m := NewManager(Deps{
		Hosts:   func() []model.Host { return devHosts("A") },
		Target:  func(id string) (tunnel.Target, error) { return tunnel.Target{HostID: id}, nil },
		Tunnels: tun,
	})
	rec := make(chan struct{}, 16)
	m.onReconcileForTest = func() {
		select {
		case rec <- struct{}{}:
		default:
		}
	}
	m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	eventually(t, func() bool { return tun.isEstablished("A", 9100) }, "建立 A:9100")

	m.Close()
	if got := tun.dropped("A", 9100); got < 1 {
		t.Fatalf("Close 应拆除 A:9100，dropped=%d", got)
	}
	// Close 后订阅返回已关闭 channel。
	ch, _ := m.Subscribe()
	if _, ok := <-ch; ok {
		t.Fatal("Close 后 Subscribe 应返回已关闭 channel")
	}
	// 二次 Close 幂等，不 panic。
	m.Close()
}

// TestActiveForwardReEnsuredEachCycle 是自愈不变量的守护测试（I1）。
//
// active 转发每轮 reconcile 都会被幂等重加（isDue 对 active 恒为真、converge 无
// active 早返回）——这正是治愈 Task 5 pin 轮换良性竞态的机制。若未来有重构给 active
// 加了早返回，本测试会红：断言 EnsureForward 调用数「严格增加」，且状态保持 active、
// 不产生新广播（幂等重加不改变快照）。
func TestActiveForwardReEnsuredEachCycle(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	ch, unsub := h.m.Subscribe()
	defer unsub()

	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100))
	eventually(t, func() bool { return h.tun.isEstablished("A", 9100) }, "建立 A:9100")
	// 消费掉建立时的 active 快照，随后 channel 应保持空。
	waitForSnapshot(t, ch, func(s []MirrorStatus) bool {
		return len(s) == 1 && s[0].State == MirrorStateActive
	})
	drainSnapshots(ch)

	before := h.tun.forwardCalls("A", 9100)
	h.drain()
	h.m.ReconcileNow()
	h.waitReconcile(t)

	// 自愈：EnsureForward 每轮重加，调用数严格增加。
	if after := h.tun.forwardCalls("A", 9100); after <= before {
		t.Fatalf("active 转发必须每轮被重加（自愈）：before=%d after=%d", before, after)
	}
	// 状态保持 active。
	s := findStatus(h.m.Statuses(), "A", 9100)
	if s == nil || s.State != MirrorStateActive {
		t.Fatalf("状态应保持 active，实际 %+v", s)
	}
	// 幂等重加不产生新广播（快照不变）。
	select {
	case snap := <-ch:
		t.Fatalf("幂等重加不应广播新快照，却收到 %+v", snap)
	case <-time.After(150 * time.Millisecond):
		// 正常：无新广播
	}
}

// TestExpectedStatePerHealth 覆盖 isRunningHealth 全部五个健康分支（M2）：
// running/healthy/restarting → 期望有转发；stopped/failed → 无。
func TestExpectedStatePerHealth(t *testing.T) {
	cases := []struct {
		health   model.Health
		expected bool
	}{
		{model.HealthRunning, true},
		{model.HealthHealthy, true},
		{model.HealthRestarting, true},
		{model.HealthStopped, false},
		{model.HealthFailed, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.health), func(t *testing.T) {
			h := newHarness(t, nil)
			h.setHosts(devHosts("A"))
			h.drain()
			h.m.ApplyNodes(frameHost("A", "dep1", "svc1", tc.health, 9100))
			h.waitReconcile(t)
			if tc.expected {
				eventually(t, func() bool { return h.tun.isEstablished("A", 9100) },
					"health="+string(tc.health)+" 应建立转发")
			} else {
				if h.tun.isEstablished("A", 9100) {
					t.Fatalf("health=%s 不应建立转发", tc.health)
				}
				if got := len(h.m.Statuses()); got != 0 {
					t.Fatalf("health=%s 不应产生镜像状态，实际 %d 条", tc.health, got)
				}
				if got := h.tun.connectCalls("A"); got != 0 {
					t.Fatalf("health=%s 不应建立隧道，EnsureConnected=%d", tc.health, got)
				}
			}
		})
	}
}

// TestEnsureConnectedFailureMarksAllPortsFailed 验证 M3：EnsureConnected 失败时
// 该 host 全部声明端口都标 failed（脱敏码），且隧道只尝试连一次（非每端口一次）。
func TestEnsureConnectedFailureMarksAllPortsFailed(t *testing.T) {
	h := newHarness(t, nil)
	h.setHosts(devHosts("A"))
	h.tun.setConnErr("A", errors.New("dial failed"))
	h.m.ApplyNodes(frameHost("A", "dep1", "svc1", model.HealthRunning, 9100, 9101))

	eventually(t, func() bool {
		s1 := findStatus(h.m.Statuses(), "A", 9100)
		s2 := findStatus(h.m.Statuses(), "A", 9101)
		return s1 != nil && s1.State == MirrorStateFailed && s2 != nil && s2.State == MirrorStateFailed
	}, "两个端口都因连接失败标 failed")

	s1 := findStatus(h.m.Statuses(), "A", 9100)
	s2 := findStatus(h.m.Statuses(), "A", 9101)
	if s1.Error != "ssh_connection_failed" || s2.Error != "ssh_connection_failed" {
		t.Fatalf("两个端口都应携带脱敏码，实际 %q / %q", s1.Error, s2.Error)
	}
	// EnsureConnected 每 host 只尝试一次。
	if got := h.tun.connectCalls("A"); got != 1 {
		t.Fatalf("EnsureConnected 应每 host 一次，实际 %d", got)
	}
	// 连接失败不应有任何转发建立。
	if h.tun.isEstablished("A", 9100) || h.tun.isEstablished("A", 9101) {
		t.Fatal("连接失败时不应建立任何转发")
	}
}
