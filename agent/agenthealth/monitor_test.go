// Package agenthealth_test 验证远端 agent 健康监控的状态判定与生命周期。
package agenthealth_test

import (
	"context"
	"errors"
	"testing"

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
