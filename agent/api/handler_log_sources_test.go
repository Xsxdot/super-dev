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

func TestLogSourceCRUD(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-1"},
	})
	resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	require.NotEmpty(t, created.ID)

	created.HostIDs = []string{"h-1", "h-2"}
	body, _ = json.Marshal(created)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/log-sources/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = http.Get(srv.URL + "/api/log-sources")
	var list []model.LogSource
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	require.Len(t, list, 1)
	assert.Equal(t, []string{"h-1", "h-2"}, list[0].HostIDs)

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/log-sources/"+created.ID, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
