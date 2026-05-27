package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

type stubResolver struct{}

func (s *stubResolver) BaseURL(hostID string) (string, error) { return "http://127.0.0.1:9999", nil }

func TestBuildBackend_LocalReturnsSQLiteBackend(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	dep := model.Deployment{
		ID:       "d-1",
		Location: model.LocationLocal,
		Command:  "go run .",
	}
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubResolver{})
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
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubResolver{})
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
	b := buildBackend(dep, "svc-1", app.store, app.buf, &stubResolver{})
	assert.NotNil(t, b)
	_, isFed := b.(*logbackend.FederatedBackend)
	assert.True(t, isFed, "multi-host remote deployment should return FederatedBackend")
}
