package api

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type stubNodeTransport struct{}

func (s *stubNodeTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	return nodetransport.NodeResponse{}, nil
}

func (s *stubNodeTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (s *stubNodeTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (s *stubNodeTransport) Covers() []string { return []string{} }

// recordingHostTransport 记录最近一次 Do 调用收到的 hostID，供
// TestBuildBackendRoutesHomeDeploymentLogs 断言 RemoteAgentBackend 确实
// 指向归属主机（而不仅仅是类型正确）。
type recordingHostTransport struct {
	hostID string
}

func (t *recordingHostTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	t.hostID = hostID
	return nodetransport.NodeResponse{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]"))}, nil
}

func (t *recordingHostTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t *recordingHostTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t *recordingHostTransport) Covers() []string { return []string{} }

func TestBuildBackend_LocalReturnsSQLiteBackend(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	dep := model.Deployment{
		ID:       "d-1",
		Location: model.LocationLocal,
		Command:  "go run .",
	}
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{}, false, "")
	assert.NotNil(t, b)
	_, isSQLite := b.(*logbackend.SQLiteBackend)
	assert.True(t, isSQLite, "local deployment should return SQLiteBackend")
}

func TestBuildBackend_RemoteSingleHostReturnsRemoteBackend(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	dep := model.Deployment{
		ID:        "d-2",
		Location:  model.LocationRemote,
		HostIDs:   []string{"host-1"},
		LogType:   model.LogSourceTypeJournalctl,
		LogTarget: "api-server.service",
	}
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{}, false, "")
	assert.NotNil(t, b)
	_, isRemote := b.(*logbackend.RemoteAgentBackend)
	assert.True(t, isRemote, "single-host remote deployment should return RemoteAgentBackend")
}

func TestBuildBackend_RemoteMultiHostReturnsFederated(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	dep := model.Deployment{
		ID:        "d-3",
		Location:  model.LocationRemote,
		HostIDs:   []string{"host-1", "host-2"},
		LogType:   model.LogSourceTypeDocker,
		LogTarget: "api-server",
	}
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{}, false, "")
	assert.NotNil(t, b)
	_, isFed := b.(*logbackend.FederatedBackend)
	assert.True(t, isFed, "multi-host remote deployment should return FederatedBackend")
}

// TestBuildBackendRoutesHomeDeploymentLogs 验证归属路由（Task 8）：
//   - dev deployment 未设归属（homeHostID==""）→ 仍是本机 SQLiteBackend（回归）
//   - dev deployment 已设归属 → RemoteAgentBackend，且确实寻址到归属主机
//     （用 recordingHostTransport 验证 Query 调用时携带的 hostID，而不只
//     是断言类型——类型相同但 host 错了会读到错误机器的日志，是本任务
//     要防的核心故障模式）
//   - prod deployment 即便项目已设归属也不受影响，保持本机 SQLite
func TestBuildBackendRoutesHomeDeploymentLogs(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	devDep := model.Deployment{
		ID:       "dep-web-dev",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 30",
	}

	noHome := buildBackend(devDep, "svc-1", app.store, app.buf, &stubNodeTransport{}, true, "")
	_, isSQLite := noHome.(*logbackend.SQLiteBackend)
	assert.True(t, isSQLite, "未设归属时 dev deployment 应仍走本机 SQLiteBackend（回归）")

	transport := &recordingHostTransport{}
	homed := buildBackend(devDep, "svc-1", app.store, app.buf, transport, true, "host-home")
	remote, isRemote := homed.(*logbackend.RemoteAgentBackend)
	require.True(t, isRemote, "已设归属的 dev deployment 应走 RemoteAgentBackend")
	_, _, _ = remote.Query(context.Background(), logbackend.QueryFilter{Limit: 1})
	assert.Equal(t, "host-home", transport.hostID, "RemoteAgentBackend 必须寻址到归属主机，而非任意/本机")

	prodDep := model.Deployment{
		ID:       "dep-web-prod",
		EnvName:  "prod",
		Location: model.LocationLocal,
		Command:  "sleep 30",
	}
	prodBackend := buildBackend(prodDep, "svc-1", app.store, app.buf, &stubNodeTransport{}, false, "host-home")
	_, prodIsSQLite := prodBackend.(*logbackend.SQLiteBackend)
	assert.True(t, prodIsSQLite, "prod deployment 不随项目归属改变，应保持本机 SQLiteBackend")
}

func TestDeploymentCollectorTargetUsesStructuredLogs(t *testing.T) {
	fileTail := model.Deployment{
		LogType:   model.LogSourceTypeJournalctl,
		LogTarget: "legacy.service",
		Logs: &model.LogConfig{
			Type: model.LogKindFileTail,
			Path: "/var/log/api/app.log",
		},
	}
	assert.Equal(t, model.LogSourceTypeFileTail, deploymentCollectorType(fileTail))
	assert.Equal(t, "/var/log/api/app.log", deploymentCollectorName(fileTail))

	command := model.Deployment{
		Logs: &model.LogConfig{
			Type:    model.LogKindCommand,
			Command: "tail -F /var/log/api/app.log",
		},
	}
	assert.Equal(t, model.LogSourceTypeCommand, deploymentCollectorType(command))
	assert.Equal(t, "tail -F /var/log/api/app.log", deploymentCollectorName(command))
}
