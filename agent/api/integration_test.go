// integration_test.go 验证远程日志监听的本机端完整冒烟链路。
//
// 职责：
//   - 串起 Host、LogSource、remote/view 和 remote-log-search 的核心流程
//   - 使用 httptest 远端替代真实 SSH 隧道和远端 agent
//
// 边界：
//   - 不连接真实 SSH
//   - 不启动真实 collector 进程
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

func TestEndToEndRemoteSearch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	colID := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	srvA := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 10, ServiceID: colID, Timestamp: now, Message: "A older"},
			{ID: 20, ServiceID: colID, Timestamp: now.Add(2 * time.Second), Message: "A newer"},
		},
	})
	srvB := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 15, ServiceID: colID, Timestamp: now.Add(time.Second), Message: "B middle"},
		},
	})

	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"hA": srvA.URL, "hB": srvB.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	for _, hostID := range []string{"hA", "hB"} {
		body, _ := json.Marshal(model.Host{ID: hostID, Name: hostID, Tags: []string{"prod"}})
		resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}

	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"hA", "hB"},
	})
	resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ls))
	_ = resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/remote/view")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var view struct {
		LogSources []any `json:"log_sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&view))
	_ = resp.Body.Close()
	require.NotEmpty(t, view.LogSources)

	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("query", "remote")
	resp, err = http.Get(srv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var sr struct {
		Entries []struct {
			HostID string         `json:"host_id"`
			Entry  model.LogEntry `json:"entry"`
		} `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sr))
	_ = resp.Body.Close()

	require.Len(t, sr.Entries, 3)
	assert.Equal(t, "hA", sr.Entries[0].HostID)
	assert.Equal(t, "hB", sr.Entries[1].HostID)
	assert.Equal(t, "hA", sr.Entries[2].HostID)
}
