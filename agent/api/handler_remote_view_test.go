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

func TestRemoteViewAggregation(t *testing.T) {
	srv, _ := newTestApp(t)

	h1Body, _ := json.Marshal(model.Host{Name: "c01", Tags: []string{"prod"}})
	h2Body, _ := json.Marshal(model.Host{Name: "c02", Tags: []string{"prod", "temp"}})
	r1, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h1Body))
	var h1 struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r1.Body).Decode(&h1)
	_ = r1.Body.Close()
	r2, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h2Body))
	var h2 struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&h2)
	_ = r2.Body.Close()

	// LogSource 打了 test、prod 两个标签，分组由 LogSource.Tags 决定，与 Host.Tags 无关
	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{h1.ID, h2.ID},
		Tags:    []string{"test", "prod"},
	})
	rls, _ := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	var ls struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rls.Body).Decode(&ls)
	_ = rls.Body.Close()

	// 必须传 log_source_id，否则 400
	t.Run("missing log_source_id returns 400", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/api/remote/view")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	resp, err := http.Get(srv.URL + "/api/remote/view?log_source_id=" + ls.ID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var view struct {
		LogSource struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"log_source"`
		Groups []struct {
			GroupKey string   `json:"group_key"`
			HostIDs  []string `json:"host_ids"`
		} `json:"groups"`
		Hosts []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"hosts"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&view))
	assert.Equal(t, ls.ID, view.LogSource.ID)
	require.Len(t, view.Hosts, 2)

	// 分组由 LogSource.Tags 决定：all + prod + test（字母序）
	tagsSeen := map[string]bool{}
	for _, g := range view.Groups {
		tagsSeen[g.GroupKey] = true
		// 每个分组都包含全部关联 Host
		assert.Len(t, g.HostIDs, 2)
	}
	assert.True(t, tagsSeen["all"])
	assert.True(t, tagsSeen["prod"])
	assert.True(t, tagsSeen["test"])
	// Host 自身有 temp tag，但 LogSource 没有，不应出现 temp 分组
	assert.False(t, tagsSeen["temp"])
}

func TestRemoteViewReturnsLogSourceBindingFields(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(model.LogSource{
		Name:      "bound-remote",
		Type:      model.LogSourceTypeJournalctl,
		HostIDs:   []string{},
		ProjectID: "project-a",
		ServiceID: "service-api",
	})
	createdResp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer createdResp.Body.Close()
	require.Equal(t, http.StatusOK, createdResp.StatusCode)

	var created model.LogSource
	require.NoError(t, json.NewDecoder(createdResp.Body).Decode(&created))

	resp, err := http.Get(srv.URL + "/api/remote/view?log_source_id=" + created.ID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var view struct {
		LogSource struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
			ServiceID string `json:"service_id"`
		} `json:"log_source"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&view))
	assert.Equal(t, created.ID, view.LogSource.ID)
	assert.Equal(t, "project-a", view.LogSource.ProjectID)
	assert.Equal(t, "service-api", view.LogSource.ServiceID)
}

func TestLogSourceProjectBinding(t *testing.T) {
	srv, _ := newTestApp(t)

	// 创建 LogSource 并带 project_id/service_id
	body, _ := json.Marshal(map[string]any{
		"name":       "server",
		"type":       "journalctl",
		"host_ids":   []string{},
		"project_id": "proj-abc",
		"service_id": "svc-xyz",
	})
	resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "proj-abc", created.ProjectID)
	assert.Equal(t, "svc-xyz", created.ServiceID)

	// 查询回来字段仍在
	listResp, err := http.Get(srv.URL + "/api/log-sources")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var list []model.LogSource
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 1)
	assert.Equal(t, "proj-abc", list[0].ProjectID)
	assert.Equal(t, "svc-xyz", list[0].ServiceID)
}
