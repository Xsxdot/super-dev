// Package agenthealth_test 验证远端 agent 健康监控的状态判定与生命周期。
package agenthealth_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/agenthealth"
)

// fakeProber 按 hostID 返回预设的探活结果，供单测注入。
type fakeProber struct {
	results map[string]agenthealth.ProbeResult
	errs    map[string]error
}

func (f *fakeProber) Probe(ctx context.Context, hostID string) (agenthealth.ProbeResult, error) {
	if err, ok := f.errs[hostID]; ok {
		return agenthealth.ProbeResult{}, err
	}
	return f.results[hostID], nil
}

func TestProbeOnceMapsHealthy(t *testing.T) {
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: true},
	}}
	m := agenthealth.NewMonitor(prober)

	m.ProbeOnce(context.Background(), "h1")

	assert.Equal(t, agenthealth.StatusHealthy, m.Status("h1"))
}

func TestProbeOnceRecordsVersionAndCheckedAt(t *testing.T) {
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: true, Version: "0.1.0"},
	}}
	m := agenthealth.NewMonitor(prober)
	before := time.Now().UTC()

	m.ProbeOnce(context.Background(), "h1")

	info := m.Info("h1")
	assert.Equal(t, agenthealth.StatusHealthy, info.Status)
	assert.Equal(t, "0.1.0", info.Version)
	assert.WithinDuration(t, before, info.CheckedAt, time.Second)
}

func TestProbeOnceMapsVersionMismatch(t *testing.T) {
	// 探得到但接口不全（某关键 endpoint 404）→ version-mismatch
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: false},
	}}
	m := agenthealth.NewMonitor(prober)

	m.ProbeOnce(context.Background(), "h1")

	assert.Equal(t, agenthealth.StatusVersionMismatch, m.Status("h1"))
}

func TestProbeOnceMapsUnreachable(t *testing.T) {
	// 探活报错（超时/连接失败）→ unreachable
	prober := &fakeProber{errs: map[string]error{"h1": errors.New("dial timeout")}}
	m := agenthealth.NewMonitor(prober)

	m.ProbeOnce(context.Background(), "h1")

	assert.Equal(t, agenthealth.StatusUnreachable, m.Status("h1"))
}

func TestStatusUnknownByDefault(t *testing.T) {
	m := agenthealth.NewMonitor(&fakeProber{})
	assert.Equal(t, agenthealth.StatusUnknown, m.Status("never-probed"))
}

// flappingProber 每次探活都在 healthy / version-mismatch 间切换，用来制造连续状态变化。
type flappingProber struct {
	n int32
}

func (f *flappingProber) Probe(ctx context.Context, hostID string) (agenthealth.ProbeResult, error) {
	next := atomic.AddInt32(&f.n, 1)
	return agenthealth.ProbeResult{AllEndpointsOK: next%2 == 0}, nil
}

func TestRunStartsPollingOnTunnelConnected(t *testing.T) {
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: true},
	}}
	m := agenthealth.NewMonitor(prober)
	m.SetPollInterval(5 * time.Millisecond)

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)

	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}

	// 轮询应在短时间内把状态推进到 healthy
	assert.Eventually(t, func() bool {
		return m.Status("h1") == agenthealth.StatusHealthy
	}, time.Second, 5*time.Millisecond)
}

func TestRunStopsPollingOnTunnelDisconnected(t *testing.T) {
	prober := &fakeProber{errs: map[string]error{"h1": errors.New("down")}}
	m := agenthealth.NewMonitor(prober)
	m.SetPollInterval(5 * time.Millisecond)

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)

	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}
	assert.Eventually(t, func() bool {
		return m.Status("h1") == agenthealth.StatusUnreachable
	}, time.Second, 5*time.Millisecond)

	// 断开后状态清回 unknown，且不再轮询
	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: false}
	assert.Eventually(t, func() bool {
		return m.Status("h1") == agenthealth.StatusUnknown
	}, time.Second, 5*time.Millisecond)
}

func TestSubscribeReceivesStatusChange(t *testing.T) {
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: true},
	}}
	m := agenthealth.NewMonitor(prober)
	m.SetPollInterval(5 * time.Millisecond)

	sub := m.Subscribe("sub-1")
	defer m.Unsubscribe("sub-1")

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)
	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}

	select {
	case ev := <-sub:
		assert.Equal(t, "h1", ev.HostID)
		assert.Equal(t, agenthealth.StatusHealthy, ev.Status)
	case <-time.After(time.Second):
		t.Fatal("expected agent health event, got none")
	}
}

func TestSubscribeReceivesVersionAndCheckedAt(t *testing.T) {
	prober := &fakeProber{results: map[string]agenthealth.ProbeResult{
		"h1": {AllEndpointsOK: true, Version: "0.1.0"},
	}}
	m := agenthealth.NewMonitor(prober)
	m.SetPollInterval(5 * time.Millisecond)

	sub := m.Subscribe("sub-meta")
	defer m.Unsubscribe("sub-meta")

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)
	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}

	select {
	case ev := <-sub:
		assert.Equal(t, "h1", ev.HostID)
		assert.Equal(t, agenthealth.StatusHealthy, ev.Status)
		assert.Equal(t, "0.1.0", ev.Version)
		assert.NotEmpty(t, ev.CheckedAt)
	case <-time.After(time.Second):
		t.Fatal("expected agent health event, got none")
	}
}

func TestRunDoesNotEmitWhenDisconnectKeepsUnknown(t *testing.T) {
	m := agenthealth.NewMonitor(&fakeProber{})

	sub := m.Subscribe("sub-unknown")
	defer m.Unsubscribe("sub-unknown")

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)
	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: false}

	select {
	case ev := <-sub:
		t.Fatalf("expected no event when status remains unknown, got %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestUnsubscribeConcurrentWithStatusEventsDoesNotPanic(t *testing.T) {
	m := agenthealth.NewMonitor(&flappingProber{})
	m.SetPollInterval(time.Millisecond)

	events := make(chan agenthealth.TunnelSignal, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, events)
	events <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}

	for i := 0; i < 200; i++ {
		subID := "sub-" + strconv.Itoa(i)
		_ = m.Subscribe(subID)
		m.Unsubscribe(subID)
	}

	assert.Eventually(t, func() bool {
		return m.Status("h1") == agenthealth.StatusHealthy ||
			m.Status("h1") == agenthealth.StatusVersionMismatch
	}, time.Second, 5*time.Millisecond)
}
