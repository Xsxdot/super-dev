// dispatcher_test.go 验证 NodeTransport dispatcher 的路由行为。
//
// 职责：
//   - 证明请求按 Host.Agent.Transport.Chain 分派到对应 provider
//   - 证明缺 Agent 或缺 provider 时返回结构化错误 code
//   - 证明节点状态订阅会聚合所有 provider
//
// 边界：
//   - 不测试具体 tunnel/direct provider 的网络行为
//   - 不测试 App 装配，server.go 另由 API 包测试覆盖
package nodetransport_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestDispatcherFallsBackToTunnelWhenDirectUnavailable(t *testing.T) {
	direct := &recordingTransport{name: "direct", err: nodetransport.ErrHostUnreachable}
	tunnel := &recordingTransport{name: "tunnel", covers: []string{"h1"}}
	hosts := []model.Host{hostWithChain("h1",
		model.TransportEntry{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		model.TransportEntry{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57017}},
	)}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
		model.TransportTypeTunnel: tunnel,
	})
	dispatcher.SetProbeTimeoutForTest(20 * time.Millisecond)

	resp, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})

	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, []string{"h1", "h1"}, direct.doHosts)
	assert.Equal(t, []string{"h1", "h1", "h1"}, tunnel.doHosts)
	route, ok := dispatcher.RouteSnapshotForTest("h1")
	require.True(t, ok)
	assert.Equal(t, 1, route.SelectedIndex)
	assert.True(t, route.Degraded)
}

func TestDispatcherReturnsAgentNotConfigured(t *testing.T) {
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) {
		return []model.Host{{ID: "h1", Name: "no-agent"}}, nil
	}, nil)

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})

	require.Equal(t, nodetransport.CodeAgentNotConfigured, nodetransport.ErrorCode(err))
}

func TestDispatcherReturnsUnsupportedTransport(t *testing.T) {
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) {
		return []model.Host{hostWithTransport("h1", model.TransportTypeBridge)}, nil
	}, map[model.TransportType]nodetransport.NodeTransport{})

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})

	require.Equal(t, nodetransport.CodeUnsupportedTransport, nodetransport.ErrorCode(err))
}

func TestDispatcherSubscribeNodesAggregatesProviders(t *testing.T) {
	tunnel := newStatusRecordingTransport("tunnel")
	direct := newStatusRecordingTransport("direct")
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) {
		return []model.Host{
			hostWithTransport("h1", model.TransportTypeTunnel),
			hostWithTransport("h2", model.TransportTypeDirect),
		}, nil
	}, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeTunnel: tunnel,
		model.TransportTypeDirect: direct,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := dispatcher.SubscribeNodes(ctx)
	defer stop()

	tunnel.emit("h1", model.AgentHealthHealthy)
	direct.emit("h2", model.AgentHealthHealthy)

	seen := map[string]bool{}
	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			for _, status := range batch {
				if status.Route != nil {
					assert.False(t, status.Route.Degraded)
				}
				seen[status.HostID] = true
			}
		default:
		}
		return seen["h1"] && seen["h2"]
	}, time.Second, 10*time.Millisecond)
	assert.ElementsMatch(t, []string{"h1", "h2"}, dispatcher.Covers())
}

func TestDispatcherProbeMarksAuthFailedAfterProvisionedHealth(t *testing.T) {
	direct := &authFailedProbeTransport{}
	hosts := []model.Host{hostWithChain("h1",
		model.TransportEntry{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	)}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
	})
	dispatcher.SetProbeTimeoutForTest(20 * time.Millisecond)

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: nodetransport.SecurityAuthCheckPath})

	require.Error(t, err)
	route, ok := dispatcher.RouteSnapshotForTest("h1")
	require.True(t, ok)
	require.Len(t, route.LastResults, 1)
	assert.Equal(t, nodetransport.ProbeStatusAuthFailed, route.LastResults[0].Status)
	assert.Equal(t, []string{nodetransport.SecurityAuthCheckPath, nodetransport.SecurityHealthPath, nodetransport.SecurityAuthCheckPath}, direct.paths)
}

func TestDispatcherRecoversChainHeadOnProbe(t *testing.T) {
	direct := &recordingTransport{name: "direct", err: nodetransport.ErrHostUnreachable}
	tunnel := &recordingTransport{name: "tunnel"}
	hosts := []model.Host{hostWithChain("h1",
		model.TransportEntry{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		model.TransportEntry{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57017}},
	)}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
		model.TransportTypeTunnel: tunnel,
	})
	dispatcher.SetProbeTimeoutForTest(20 * time.Millisecond)

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	direct.err = nil
	dispatcher.RecoverChainHeadsForTest(context.Background())

	route, ok := dispatcher.RouteSnapshotForTest("h1")
	require.True(t, ok)
	assert.Equal(t, 0, route.SelectedIndex)
	assert.False(t, route.Degraded)
}

func TestDispatcherSubscribeNodesSwitchesWhenRouteChanges(t *testing.T) {
	direct := newStatusRecordingTransport("direct")
	tunnel := newStatusRecordingTransport("tunnel")
	direct.err = nodetransport.ErrHostUnreachable
	hosts := []model.Host{hostWithChain("h1",
		model.TransportEntry{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		model.TransportEntry{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57017}},
	)}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
		model.TransportTypeTunnel: tunnel,
	})
	dispatcher.SetProbeTimeoutForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := dispatcher.SubscribeNodes(ctx)
	defer stop()

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	tunnel.emit("h1", model.AgentHealthHealthy)

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].Route != nil && batch[0].Route.SelectedType == model.TransportTypeTunnel
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	direct.err = nil
	dispatcher.RecoverChainHeadsForTest(context.Background())
	direct.emit("h1", model.AgentHealthHealthy)

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].Route != nil && batch[0].Route.SelectedType == model.TransportTypeDirect
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestDispatcherSubscribeNodesRecoversChainHeadOnReachableStatus(t *testing.T) {
	direct := newStatusRecordingTransport("direct")
	tunnel := newStatusRecordingTransport("tunnel")
	direct.err = nodetransport.ErrHostUnreachable
	hosts := []model.Host{hostWithChain("h1",
		model.TransportEntry{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		model.TransportEntry{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57017}},
	)}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
		model.TransportTypeTunnel: tunnel,
	})
	dispatcher.SetProbeTimeoutForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := dispatcher.SubscribeNodes(ctx)
	defer stop()

	_, err := dispatcher.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	direct.err = nil
	tunnel.emit("h1", model.AgentHealthHealthy)

	require.Eventually(t, func() bool {
		route, ok := dispatcher.RouteSnapshotForTest("h1")
		return ok && route.SelectedType == model.TransportTypeDirect
	}, time.Second, 10*time.Millisecond)

	direct.emit("h1", model.AgentHealthHealthy)

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].Route != nil && batch[0].Route.SelectedType == model.TransportTypeDirect
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

type recordingTransport struct {
	name    string
	covers  []string
	doHosts []string
	batches [][]nodetransport.NodeStatus
	err     error
}

func (r *recordingTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	r.doHosts = append(r.doHosts, hostID)
	if r.err != nil {
		return nodetransport.NodeResponse{}, r.err
	}
	body := `{"version":"0.1.0","provision_state":"provisioned"}`
	return nodetransport.NodeResponse{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (r *recordingTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (r *recordingTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus, len(r.batches))
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(ch)
		for _, batch := range r.batches {
			select {
			case ch <- batch:
			case <-runCtx.Done():
				return
			}
		}
		<-runCtx.Done()
	}()
	return ch, cancel
}

func (r *recordingTransport) Covers() []string {
	return append([]string(nil), r.covers...)
}

type authFailedProbeTransport struct {
	paths []string
}

func (a *authFailedProbeTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	a.paths = append(a.paths, req.Path)
	if len(a.paths) == 1 {
		return nodetransport.NodeResponse{}, nodetransport.ErrHostUnreachable
	}
	if req.Path == nodetransport.SecurityHealthPath {
		body := `{"version":"0.1.0","provision_state":"provisioned"}`
		return nodetransport.NodeResponse{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	return nodetransport.NodeResponse{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (a *authFailedProbeTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (a *authFailedProbeTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (a *authFailedProbeTransport) Covers() []string { return []string{"h1"} }

type statusRecordingTransport struct {
	*recordingTransport
	status chan []nodetransport.NodeStatus
}

func newStatusRecordingTransport(name string) *statusRecordingTransport {
	return &statusRecordingTransport{
		recordingTransport: &recordingTransport{name: name},
		status:             make(chan []nodetransport.NodeStatus, 16),
	}
}

func (s *statusRecordingTransport) SubscribeHostNodes(ctx context.Context, host model.Host) (<-chan []nodetransport.NodeStatus, func()) {
	out := make(chan []nodetransport.NodeStatus, 16)
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		for {
			select {
			case <-runCtx.Done():
				return
			case batch := <-s.status:
				select {
				case out <- batch:
				case <-runCtx.Done():
					return
				}
			}
		}
	}()
	return out, cancel
}

func (s *statusRecordingTransport) emit(hostID string, health model.AgentHealth) {
	s.status <- []nodetransport.NodeStatus{{
		HostID:    hostID,
		Reachable: health == model.AgentHealthHealthy,
		Agent: model.AgentRuntime{
			Installed: health == model.AgentHealthHealthy,
			Health:    health,
			Reachable: health == model.AgentHealthHealthy,
		},
		UpdatedAt: time.Now().UTC(),
	}}
}

func hostWithTransport(id string, typ model.TransportType) model.Host {
	return hostWithChain(id, model.TransportEntry{Type: typ})
}

func hostWithChain(id string, entries ...model.TransportEntry) model.Host {
	return model.Host{
		ID: id,
		Agent: &model.Agent{Transport: model.TransportConfig{
			Chain: entries,
		}},
	}
}
