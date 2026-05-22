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
	var h1 model.Host
	_ = json.NewDecoder(r1.Body).Decode(&h1)
	_ = r1.Body.Close()
	r2, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h2Body))
	var h2 model.Host
	_ = json.NewDecoder(r2.Body).Decode(&h2)
	_ = r2.Body.Close()

	lsBody, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{h1.ID, h2.ID},
	})
	rls, _ := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	_ = rls.Body.Close()

	resp, err := http.Get(srv.URL + "/api/remote/view")
	require.NoError(t, err)
	defer resp.Body.Close()

	var view struct {
		LogSources []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Type   string `json:"type"`
			Groups []struct {
				Tag     string   `json:"tag"`
				HostIDs []string `json:"host_ids"`
			} `json:"groups"`
		} `json:"log_sources"`
		Hosts []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"hosts"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&view))
	require.Len(t, view.LogSources, 1)
	require.Len(t, view.Hosts, 2)

	tagsSeen := map[string]bool{}
	for _, g := range view.LogSources[0].Groups {
		tagsSeen[g.Tag] = true
	}
	assert.True(t, tagsSeen["all"])
	assert.True(t, tagsSeen["prod"])
	assert.True(t, tagsSeen["temp"])
}
