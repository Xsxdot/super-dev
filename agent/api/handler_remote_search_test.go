package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		svc := q.Get("service")
		entries := items[svc]
		if strings.TrimSpace(q.Get("q")) == "" {
			entries = []model.LogEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":          q.Get("q"),
			"total":          len(entries),
			"items":          entries,
			"service_counts": map[string]int{svc: len(entries)},
			"has_more":       false,
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
			ID: id, Name: req.Name, Type: req.Type, ServiceID: id,
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
			{ID: 1, ServiceID: colID, Timestamp: now, Message: "from h1 #1"},
			{ID: 3, ServiceID: colID, Timestamp: now.Add(2 * time.Second), Message: "from h1 #2"},
		},
	})
	srvH2 := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 2, ServiceID: colID, Timestamp: now.Add(time.Second), Message: "from h2 #1"},
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
		colID: {{ID: 1, ServiceID: colID, Timestamp: now, Message: "from query param"}},
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
