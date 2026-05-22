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
	var h1 struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r1.Body).Decode(&h1)
	_ = r1.Body.Close()
	r2, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h2Body))
	var h2 struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r2.Body).Decode(&h2)
	_ = r2.Body.Close()

	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{h1.ID, h2.ID},
	})
	rls, _ := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	var ls struct{ ID string `json:"id"` }
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

	tagsSeen := map[string]bool{}
	for _, g := range view.Groups {
		tagsSeen[g.GroupKey] = true
	}
	assert.True(t, tagsSeen["all"])
	assert.True(t, tagsSeen["prod"])
	assert.True(t, tagsSeen["temp"])
}
