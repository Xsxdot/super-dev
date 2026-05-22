package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListTunnelsEmpty(t *testing.T) {
	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/tunnels")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Empty(t, got)
}

func TestConnectTunnelHostNotFound(t *testing.T) {
	srv, _ := newTestApp(t)
	resp, err := http.Post(srv.URL+"/api/tunnels/nonexistent/connect", "application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
