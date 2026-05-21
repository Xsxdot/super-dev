package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestStartCollectorOK(t *testing.T) {
	srv, _ := newTestApp(t)

	body := bytes.NewBufferString(`{"name":"sleep-test","type":"journalctl"}`)
	// 注:newTestApp 内的 probe 是放行所有的桩(见 server.go 的 ProbeOverride)。
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got model.Collector
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, "sleep-test", got.Name)
	assert.Equal(t, model.LogSourceTypeJournalctl, got.Type)
}

func TestStartCollectorRejectsBadName(t *testing.T) {
	srv, _ := newTestApp(t)
	body := bytes.NewBufferString(`{"name":"; rm -rf /","type":"journalctl"}`)
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStartCollectorRejectsBadType(t *testing.T) {
	srv, _ := newTestApp(t)
	body := bytes.NewBufferString(`{"name":"ok","type":"kubectl"}`)
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListCollectors(t *testing.T) {
	srv, _ := newTestApp(t)
	_ = postJSON(t, srv.URL+"/api/collectors",
		map[string]string{"name": "alpha", "type": "journalctl"})

	resp, err := http.Get(srv.URL + "/api/collectors")
	require.NoError(t, err)
	defer resp.Body.Close()
	var list []model.Collector
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Len(t, list, 1)
	assert.Equal(t, "alpha", list[0].Name)
}

func TestDeleteCollector(t *testing.T) {
	srv, _ := newTestApp(t)
	created := postJSON(t, srv.URL+"/api/collectors",
		map[string]string{"name": "beta", "type": "journalctl"})
	id := created["id"].(string)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/collectors/"+id, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 再次列表应为空。
	list := getJSONArray(t, srv.URL+"/api/collectors")
	assert.Empty(t, list)
}

func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got
}

func getJSONArray(t *testing.T, url string) []map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got
}
