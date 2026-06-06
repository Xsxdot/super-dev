package api

import (
	"context"
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

func TestBuildBackend_LocalReturnsSQLiteBackend(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	dep := model.Deployment{
		ID:       "d-1",
		Location: model.LocationLocal,
		Command:  "go run .",
	}
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{})
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
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{})
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
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubNodeTransport{})
	assert.NotNil(t, b)
	_, isFed := b.(*logbackend.FederatedBackend)
	assert.True(t, isFed, "multi-host remote deployment should return FederatedBackend")
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
