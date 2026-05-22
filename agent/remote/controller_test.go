package remote_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

func fakeRemote(t *testing.T) (*httptest.Server, *fakeRemoteState) {
	state := &fakeRemoteState{collectors: map[string]model.Collector{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string              `json:"name"`
			Type model.LogSourceType `json:"type"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		id := req.Name + "@" + string(req.Type)
		state.collectors[id] = model.Collector{ID: id, Name: req.Name, Type: req.Type, ServiceID: id}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.collectors[id])
	})
	mux.HandleFunc("DELETE /api/collectors/{id}", func(w http.ResponseWriter, r *http.Request) {
		delete(state.collectors, r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	mux.HandleFunc("GET /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		list := []model.Collector{}
		for _, c := range state.collectors {
			list = append(list, c)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type fakeRemoteState struct {
	collectors map[string]model.Collector
}

type fakeTunnel struct {
	baseURLs map[string]string
}

func (f *fakeTunnel) BaseURL(hostID string) (string, error) {
	if url, ok := f.baseURLs[hostID]; ok {
		return url, nil
	}
	return "", remote.ErrHostUnreachable
}

func newController(t *testing.T, remotes map[string]string) (*remote.Controller, *remote.Store) {
	dir := t.TempDir()
	store := remote.NewStore(filepath.Join(dir, "hosts.json"), filepath.Join(dir, "log_sources.json"))
	ctrl := remote.NewController(store, &fakeTunnel{baseURLs: remotes}, http.DefaultClient)
	return ctrl, store
}

func TestEnsureCollectorStartsRemote(t *testing.T) {
	srv, state := fakeRemote(t)
	ctrl, store := newController(t, map[string]string{"h-1": srv.URL})

	host, _ := store.AddHost(model.Host{ID: "h-1", Name: "c01"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{host.ID},
	})

	collectorID, err := ctrl.EnsureCollector(host.ID, ls.ID)
	require.NoError(t, err)
	assert.Equal(t, "nova-api@journalctl", collectorID)
	assert.Contains(t, state.collectors, collectorID)
}

func TestStopCollector(t *testing.T) {
	srv, state := fakeRemote(t)
	ctrl, store := newController(t, map[string]string{"h-1": srv.URL})

	host, _ := store.AddHost(model.Host{ID: "h-1"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name:    "alpha",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{host.ID},
	})
	_, _ = ctrl.EnsureCollector(host.ID, ls.ID)
	require.NotEmpty(t, state.collectors)

	require.NoError(t, ctrl.StopCollector(host.ID, ls.ID))
	assert.Empty(t, state.collectors)
}

func TestEnsureCollectorHostUnreachable(t *testing.T) {
	ctrl, store := newController(t, map[string]string{})
	host, _ := store.AddHost(model.Host{ID: "h-1"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name: "x", Type: model.LogSourceTypeJournalctl, HostIDs: []string{host.ID},
	})
	_, err := ctrl.EnsureCollector(host.ID, ls.ID)
	require.ErrorIs(t, err, remote.ErrHostUnreachable)
}

func TestEnsureCollectorRequiresHostInLogSource(t *testing.T) {
	srv, state := fakeRemote(t)
	ctrl, store := newController(t, map[string]string{"h-1": srv.URL})

	host, _ := store.AddHost(model.Host{ID: "h-1"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name: "x", Type: model.LogSourceTypeJournalctl, HostIDs: []string{"h-2"},
	})

	_, err := ctrl.EnsureCollector(host.ID, ls.ID)
	require.ErrorIs(t, err, remote.ErrNotFound)
	assert.Empty(t, state.collectors)
}
