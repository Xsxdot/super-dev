package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// staticResolver 把固定 hostID 映射到固定 URL,测试用。
type staticResolver struct {
	table map[string]string
}

// BaseURL 返回 hostID 对应的测试远端 URL。
func (s *staticResolver) BaseURL(hostID string) (string, error) {
	if u, ok := s.table[hostID]; ok {
		return u, nil
	}
	return "", remote.ErrHostUnreachable
}

func fakeRemoteWithSearch(t *testing.T, items map[string][]model.LogEntry) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/log-search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		dep := q.Get("deployment")
		entries := items[dep]
		if strings.TrimSpace(q.Get("q")) == "" {
			entries = []model.LogEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":             q.Get("q"),
			"total":             len(entries),
			"items":             entries,
			"deployment_counts": map[string]int{dep: len(entries)},
			"has_more":          false,
		})
	})
	mux.HandleFunc("POST /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string              `json:"name"`
			Type model.LogSourceType `json:"type"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := collector.CollectorID(req.Name, req.Type)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.Collector{
			ID: id, Name: req.Name, Type: req.Type, DeploymentID: id,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteLogSearchMergesAcrossHosts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	colID := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)

	srvH1 := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 1, DeploymentID: colID, Timestamp: now, Message: "from h1 #1"},
			{ID: 3, DeploymentID: colID, Timestamp: now.Add(2 * time.Second), Message: "from h1 #2"},
		},
	})
	srvH2 := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 2, DeploymentID: colID, Timestamp: now.Add(time.Second), Message: "from h2 #1"},
		},
	})

	resolver := &staticResolver{table: map[string]string{
		"h1": srvH1.URL,
		"h2": srvH2.URL,
	}}
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: resolver,
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	for _, hostID := range []string{"h1", "h2"} {
		body, _ := json.Marshal(model.Host{ID: hostID, Name: hostID, Tags: []string{"prod"}})
		resp, err := http.Post(httpSrv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}
	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"h1", "h2"},
	})
	resp, err := http.Post(httpSrv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ls))
	_ = resp.Body.Close()

	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("q", "from")
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Entries []struct {
			HostID string         `json:"host_id"`
			Entry  model.LogEntry `json:"entry"`
		} `json:"entries"`
		HostsFailed []string `json:"hosts_failed"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Entries, 3)
	assert.Equal(t, "h1", result.Entries[0].HostID)
	assert.Equal(t, "h2", result.Entries[1].HostID)
	assert.Equal(t, "h1", result.Entries[2].HostID)
	assert.Empty(t, result.HostsFailed)
}

func TestRemoteLogSearchAcceptsSpecQueryParam(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	colID := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	remoteSrv := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {{ID: 1, DeploymentID: colID, Timestamp: now, Message: "from query param"}},
	})
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	hbody, _ := json.Marshal(model.Host{ID: "h1", Name: "h1", Tags: []string{"prod"}})
	resp, err := http.Post(httpSrv.URL+"/api/hosts", "application/json", bytes.NewReader(hbody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
	lsBody, _ := json.Marshal(model.LogSource{Name: "nova-api", Type: model.LogSourceTypeJournalctl, HostIDs: []string{"h1"}})
	resp, err = http.Post(httpSrv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ls))
	_ = resp.Body.Close()

	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("query", "from")
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		Entries []struct {
			HostID string `json:"host_id"`
		} `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Entries, 1)
	assert.Equal(t, "h1", result.Entries[0].HostID)
}

func TestRemoteLogSearchHandlesUnreachable(t *testing.T) {
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	hbody, _ := json.Marshal(model.Host{ID: "h-x", Name: "h-x", Tags: []string{"prod"}})
	resp, err := http.Post(httpSrv.URL+"/api/hosts", "application/json", bytes.NewReader(hbody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-x"},
	})
	resp, err = http.Post(httpSrv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ls))
	_ = resp.Body.Close()

	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("q", "x")
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Entries     []any    `json:"entries"`
		HostsFailed []string `json:"hosts_failed"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Empty(t, result.Entries)
	assert.Contains(t, result.HostsFailed, "h-x")
}

func writeRemoteSearchProjectConfig(t *testing.T, services ...model.Service) string {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	var b strings.Builder
	b.WriteString("id: proj-remote\n")
	b.WriteString("name: Remote Project\n")
	b.WriteString("services:\n")
	for _, svc := range services {
		b.WriteString("  - id: " + svc.ID + "\n")
		b.WriteString("    name: " + svc.Name + "\n")
		b.WriteString("    command: echo " + svc.Name + "\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(b.String()), 0o644))
	return root
}

func createRemoteSearchProject(t *testing.T, srv *httptest.Server) model.Project {
	t.Helper()
	root := writeRemoteSearchProjectConfig(t,
		model.Service{ID: "svc-api", Name: "api"},
		model.Service{ID: "svc-worker", Name: "worker"},
	)
	resp, err := http.Post(
		srv.URL+"/api/projects",
		"application/json",
		strings.NewReader(`{"root_path": "`+root+`"}`),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var project model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&project))
	return project
}

func createRemoteSearchHost(t *testing.T, srv *httptest.Server, host model.Host) {
	t.Helper()
	body, err := json.Marshal(host)
	require.NoError(t, err)
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func createRemoteSearchLogSource(t *testing.T, srv *httptest.Server, source model.LogSource) model.LogSource {
	t.Helper()
	body, err := json.Marshal(source)
	require.NoError(t, err)
	resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	return created
}

func TestRemoteLogSearch_ProjectModeResolvesAllVisibleTargets(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	apiCollector := collector.CollectorID("api", model.LogSourceTypeJournalctl)
	workerCollector := collector.CollectorID("worker", model.LogSourceTypeJournalctl)
	remoteSrv := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		apiCollector: {
			{ID: 101, DeploymentID: apiCollector, Timestamp: now, Message: "api error from host"},
		},
		workerCollector: {
			{ID: 101, DeploymentID: workerCollector, Timestamp: now.Add(time.Second), Message: "worker error from host"},
		},
	})
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL, "h2": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h2", Name: "node-b", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1"}, Tags: []string{"prod"},
	})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "worker", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-worker", HostIDs: []string{"h2"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "error")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Status         string `json:"status"`
		ServiceColumns []struct {
			ServiceID   string `json:"service_id"`
			Status      string `json:"status"`
			ResultCount int    `json:"result_count"`
			NodeCount   int    `json:"node_count"`
			Nodes       []struct {
				HostID string `json:"host_id"`
				Status string `json:"status"`
				Count  int    `json:"count"`
			} `json:"nodes"`
			Entries []struct {
				Key         string `json:"key"`
				ServiceID   string `json:"service_id"`
				LogSourceID string `json:"log_source_id"`
				HostID      string `json:"host_id"`
				Message     string `json:"message"`
			} `json:"entries"`
		} `json:"service_columns"`
		Failures []any `json:"failures"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "success", result.Status)
	require.Len(t, result.ServiceColumns, 2)
	assert.Equal(t, "svc-api", result.ServiceColumns[0].ServiceID)
	assert.Equal(t, "svc-worker", result.ServiceColumns[1].ServiceID)
	assert.Equal(t, 1, result.ServiceColumns[0].ResultCount)
	assert.Equal(t, 1, result.ServiceColumns[0].NodeCount)
	assert.Equal(t, "h1", result.ServiceColumns[0].Nodes[0].HostID)
	assert.Equal(t, "success", result.ServiceColumns[0].Nodes[0].Status)
	assert.Contains(t, result.ServiceColumns[0].Entries[0].Key, "svc-api")
	assert.Contains(t, result.ServiceColumns[0].Entries[0].Key, "h1")
	assert.Contains(t, result.ServiceColumns[1].Entries[0].Key, "svc-worker")
	assert.Contains(t, result.ServiceColumns[1].Entries[0].Key, "h2")
	assert.Empty(t, result.Failures)
}

func TestRemoteLogSearch_ProjectModeRejectsBlankQuery(t *testing.T) {
	app, err := api.NewApp(api.AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "   ")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRemoteLogSearch_ProjectModeAppliesServiceAndHostFilters(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	apiCollector := collector.CollectorID("api", model.LogSourceTypeJournalctl)
	workerCollector := collector.CollectorID("worker", model.LogSourceTypeJournalctl)
	var requestedCollectors []string
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedCollectors = append(requestedCollectors, r.URL.Query().Get("deployment"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 1,
			"items": []model.LogEntry{{ID: 7, DeploymentID: apiCollector, Timestamp: now, Message: "api scoped trace"}},
		})
	}))
	defer remoteSrv.Close()

	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL, "h2": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h2", Name: "node-b", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1", "h2"}, Tags: []string{"prod"},
	})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "worker", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-worker", HostIDs: []string{"h2"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "trace")
	q.Set("service_id", "svc-api")
	q.Set("host_id", "h1")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		Status         string `json:"status"`
		ServiceColumns []struct {
			ServiceID string `json:"service_id"`
			Nodes     []struct {
				HostID string `json:"host_id"`
			} `json:"nodes"`
		} `json:"service_columns"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "success", result.Status)
	require.Len(t, result.ServiceColumns, 1)
	assert.Equal(t, "svc-api", result.ServiceColumns[0].ServiceID)
	require.Len(t, result.ServiceColumns[0].Nodes, 1)
	assert.Equal(t, "h1", result.ServiceColumns[0].Nodes[0].HostID)
	assert.Equal(t, []string{apiCollector}, requestedCollectors)
	assert.NotContains(t, requestedCollectors, workerCollector)
}

func TestRemoteLogSearch_ProjectModePartialFailureAndAllFailureStatuses(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	apiCollector := collector.CollectorID("api", model.LogSourceTypeJournalctl)
	remoteSrv := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		apiCollector: {{ID: 1, DeploymentID: apiCollector, Timestamp: now, Message: "api survives failure"}},
	})
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h-missing", Name: "node-b", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1", "h-missing"}, Tags: []string{"prod"},
	})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "worker", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-worker", HostIDs: []string{"h-missing"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("service_id", "svc-api")
	q.Set("group", "all")
	q.Set("q", "api")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var partial struct {
		Status         string `json:"status"`
		ServiceColumns []struct {
			ServiceID string `json:"service_id"`
			Status    string `json:"status"`
			Entries   []any  `json:"entries"`
			Nodes     []struct {
				HostID string `json:"host_id"`
				Status string `json:"status"`
			} `json:"nodes"`
		} `json:"service_columns"`
		Failures []struct {
			ServiceID string `json:"service_id"`
			HostID    string `json:"host_id"`
			Kind      string `json:"kind"`
		} `json:"failures"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&partial))
	require.Equal(t, "partial_failed", partial.Status)
	require.Len(t, partial.ServiceColumns, 1)
	assert.Equal(t, "svc-api", partial.ServiceColumns[0].ServiceID)
	assert.Equal(t, "partial_failed", partial.ServiceColumns[0].Status)
	assert.Len(t, partial.ServiceColumns[0].Entries, 1)
	assert.Contains(t, partial.Failures, struct {
		ServiceID string `json:"service_id"`
		HostID    string `json:"host_id"`
		Kind      string `json:"kind"`
	}{ServiceID: "svc-api", HostID: "h-missing", Kind: "failed"})

	q.Set("service_id", "svc-worker")
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var failed struct {
		Status         string `json:"status"`
		ServiceColumns []struct {
			ServiceID string `json:"service_id"`
			Status    string `json:"status"`
			Entries   []any  `json:"entries"`
		} `json:"service_columns"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&failed))
	assert.Equal(t, "failed", failed.Status)
	require.Len(t, failed.ServiceColumns, 1)
	assert.Equal(t, "failed", failed.ServiceColumns[0].Status)
	assert.Empty(t, failed.ServiceColumns[0].Entries)
}

func TestRemoteLogSearch_ProjectModeUsesCursorForNextPage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	apiCollector := collector.CollectorID("api", model.LogSourceTypeJournalctl)
	var seenCursorIDs []string
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seenCursorIDs = append(seenCursorIDs, q.Get("cursor_id"))
		entries := []model.LogEntry{
			{ID: 1, DeploymentID: apiCollector, Timestamp: now, Message: "first trace"},
			{ID: 2, DeploymentID: apiCollector, Timestamp: now.Add(time.Second), Message: "second trace"},
		}
		if q.Get("cursor_id") == "1" {
			entries = entries[1:]
		} else {
			entries = entries[:1]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 2,
			"items": entries,
		})
	}))
	defer remoteSrv.Close()

	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "trace")
	q.Set("limit", "1")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first struct {
		ServiceColumns []struct {
			Entries []struct {
				ID int64 `json:"id"`
			} `json:"entries"`
		} `json:"service_columns"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	require.True(t, first.HasMore)
	require.Len(t, first.ServiceColumns[0].Entries, 1)
	assert.Equal(t, int64(1), first.ServiceColumns[0].Entries[0].ID)

	q.Set("cursor", first.NextCursor)
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var second struct {
		ServiceColumns []struct {
			Entries []struct {
				ID int64 `json:"id"`
			} `json:"entries"`
		} `json:"service_columns"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&second))
	require.Len(t, seenCursorIDs, 2)
	assert.Equal(t, "1", seenCursorIDs[1])
	require.Len(t, second.ServiceColumns[0].Entries, 1)
	assert.Equal(t, int64(2), second.ServiceColumns[0].Entries[0].ID)
}

func TestRemoteLogSearch_ProjectModeCursorDoesNotRepeatReturnedMultiTargetEntries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	apiCollector := collector.CollectorID("api", model.LogSourceTypeJournalctl)
	workerCollector := collector.CollectorID("worker", model.LogSourceTypeJournalctl)
	remoteFor := func(entries []model.LogEntry) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			cursorID := q.Get("cursor_id")
			page := entries
			if cursorID != "" {
				cursor, err := strconv.ParseInt(cursorID, 10, 64)
				require.NoError(t, err)
				page = nil
				for _, entry := range entries {
					if entry.ID > cursor {
						page = append(page, entry)
					}
				}
			}
			if len(page) > 1 {
				page = page[:1]
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": len(entries),
				"items": page,
			})
		}))
	}
	apiSrv := remoteFor([]model.LogEntry{
		{ID: 1, DeploymentID: apiCollector, Timestamp: now, Message: "api trace one"},
		{ID: 3, DeploymentID: apiCollector, Timestamp: now.Add(2 * time.Second), Message: "api trace two"},
	})
	defer apiSrv.Close()
	workerSrv := remoteFor([]model.LogEntry{
		{ID: 2, DeploymentID: workerCollector, Timestamp: now.Add(time.Second), Message: "worker trace one"},
		{ID: 4, DeploymentID: workerCollector, Timestamp: now.Add(3 * time.Second), Message: "worker trace two"},
	})
	defer workerSrv.Close()

	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": apiSrv.URL, "h2": workerSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h2", Name: "node-b", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1"}, Tags: []string{"prod"},
	})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "worker", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-worker", HostIDs: []string{"h2"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "trace")
	q.Set("limit", "1")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first struct {
		ServiceColumns []struct {
			Entries []struct {
				Key string `json:"key"`
			} `json:"entries"`
		} `json:"service_columns"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	require.True(t, first.HasMore)

	firstKeys := map[string]bool{}
	for _, column := range first.ServiceColumns {
		for _, entry := range column.Entries {
			firstKeys[entry.Key] = true
		}
	}
	require.Len(t, firstKeys, 2)

	q.Set("cursor", first.NextCursor)
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var second struct {
		ServiceColumns []struct {
			Entries []struct {
				Key string `json:"key"`
			} `json:"entries"`
		} `json:"service_columns"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&second))
	for _, column := range second.ServiceColumns {
		for _, entry := range column.Entries {
			assert.False(t, firstKeys[entry.Key], "second page repeated first page entry %s", entry.Key)
		}
	}
}

func TestRemoteLogSearch_ProjectModeClassifiesTimeout(t *testing.T) {
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer remoteSrv.Close()

	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"h1": remoteSrv.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	project := createRemoteSearchProject(t, httpSrv)
	createRemoteSearchHost(t, httpSrv, model.Host{ID: "h1", Name: "node-a", Tags: []string{"prod"}})
	createRemoteSearchLogSource(t, httpSrv, model.LogSource{
		Name: "api", Type: model.LogSourceTypeJournalctl, ProjectID: project.ID, ServiceID: "svc-api", HostIDs: []string{"h1"}, Tags: []string{"prod"},
	})

	q := url.Values{}
	q.Set("project_id", project.ID)
	q.Set("group", "all")
	q.Set("q", "trace")
	resp, err := http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		Status         string `json:"status"`
		ServiceColumns []struct {
			Status string `json:"status"`
			Nodes  []struct {
				HostID string `json:"host_id"`
				Status string `json:"status"`
			} `json:"nodes"`
		} `json:"service_columns"`
		Failures []struct {
			HostID string `json:"host_id"`
			Kind   string `json:"kind"`
		} `json:"failures"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "failed", result.Status)
	require.Len(t, result.ServiceColumns, 1)
	assert.Equal(t, "timeout", result.ServiceColumns[0].Status)
	require.Len(t, result.ServiceColumns[0].Nodes, 1)
	assert.Equal(t, "timeout", result.ServiceColumns[0].Nodes[0].Status)
	require.Len(t, result.Failures, 1)
	assert.Equal(t, "h1", result.Failures[0].HostID)
	assert.Equal(t, "timeout", result.Failures[0].Kind)
}
