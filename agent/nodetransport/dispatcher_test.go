// dispatcher_test.go 验证 NodeTransport dispatcher 的路由行为。
//
// 职责：
//   - 证明请求按 Host.Agent.Transport.Type 分派到对应 provider
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

func TestDispatcherRoutesByHostTransportType(t *testing.T) {
	tunnel := &recordingTransport{name: "tunnel", covers: []string{"h1"}}
	direct := &recordingTransport{name: "direct", covers: []string{"h2"}}
	hosts := []model.Host{
		hostWithTransport("h1", model.TransportTypeTunnel),
		hostWithTransport("h2", model.TransportTypeDirect),
	}
	dispatcher := nodetransport.NewDispatcher(func() ([]model.Host, error) { return hosts, nil }, map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeTunnel: tunnel,
		model.TransportTypeDirect: direct,
	})

	resp, err := dispatcher.Do(context.Background(), "h2", nodetransport.NodeRequest{Path: "/api/exec/health"})

	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, []string{"h2"}, direct.doHosts)
	assert.Empty(t, tunnel.doHosts)
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
	tunnel := &recordingTransport{
		name:   "tunnel",
		covers: []string{"h1"},
		batches: [][]nodetransport.NodeStatus{{
			{HostID: "h1", Agent: model.AgentRuntime{Health: model.AgentHealthHealthy}},
		}},
	}
	direct := &recordingTransport{
		name:   "direct",
		covers: []string{"h2"},
		batches: [][]nodetransport.NodeStatus{{
			{HostID: "h2", Agent: model.AgentRuntime{Health: model.AgentHealthHealthy}},
		}},
	}
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

	seen := map[string]bool{}
	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			for _, status := range batch {
				seen[status.HostID] = true
			}
		default:
		}
		return seen["h1"] && seen["h2"]
	}, time.Second, 10*time.Millisecond)
	assert.ElementsMatch(t, []string{"h1", "h2"}, dispatcher.Covers())
}

type recordingTransport struct {
	name    string
	covers  []string
	doHosts []string
	batches [][]nodetransport.NodeStatus
}

func (r *recordingTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	r.doHosts = append(r.doHosts, hostID)
	return nodetransport.NodeResponse{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
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

func hostWithTransport(id string, typ model.TransportType) model.Host {
	return model.Host{
		ID:    id,
		Agent: &model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{{Type: typ}}}},
	}
}
