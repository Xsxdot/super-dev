package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/sshconfig"
)

func TestListSSHConfigHosts(t *testing.T) {
	tmpHome := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpHome, ".ssh"), 0o700))
	cfg := "Host c01\n  HostName 1.2.3.4\n  User ops\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpHome, ".ssh", "config"), []byte(cfg), 0o600))

	t.Setenv("HOME", tmpHome)

	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/ssh-config/hosts")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []sshconfig.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "c01", got[0].Name)
	assert.Equal(t, "1.2.3.4", got[0].HostName)
}

func TestListSSHConfigHostsMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/ssh-config/hosts")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []sshconfig.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Empty(t, got)
}
