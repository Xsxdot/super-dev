// handler_agent_uninstall_script_test.go verifies version-matched manual uninstall script delivery.
//
// Responsibilities:
//   - Prove the public HTTP route serves only the bundled Shell and PowerShell scripts.
//   - Prove responses identify the running Controller version and use attachment filenames.
//
// Boundaries:
//   - Does not execute the scripts.
//   - Does not fetch GitHub Release assets.
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/internal/buildinfo"
)

func TestAgentManualUninstallScriptsAreServedFromBundledVersionedResources(t *testing.T) {
	resourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resourceDir, "uninstall-agent.sh"), []byte("shell fixture\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(resourceDir, "uninstall-agent.ps1"), []byte("powershell fixture\n"), 0o644))
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallBinaryDir: resourceDir})
	require.NoError(t, err)
	defer app.Close()

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "uninstall-agent.sh", contentType: "text/x-shellscript", body: "shell fixture\n"},
		{name: "uninstall-agent.ps1", contentType: "text/plain", body: "powershell fixture\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptestDo(t, app, http.MethodGet, "/api/agents/uninstall-scripts/"+tc.name, nil)

			require.Equal(t, http.StatusOK, resp.Code)
			assert.Contains(t, resp.Header().Get("Content-Type"), tc.contentType)
			assert.Equal(t, `attachment; filename="`+tc.name+`"`, resp.Header().Get("Content-Disposition"))
			assert.Equal(t, buildinfo.Version, resp.Header().Get("X-SuperDev-Agent-Version"))
			assert.Equal(t, tc.body, resp.Body.String())
		})
	}
}

func TestAgentManualUninstallScriptRouteRejectsUnownedFiles(t *testing.T) {
	resourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resourceDir, "security.json"), []byte("secret"), 0o600))
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallBinaryDir: resourceDir})
	require.NoError(t, err)
	defer app.Close()

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/uninstall-scripts/security.json", nil)

	assert.Equal(t, http.StatusNotFound, resp.Code)
	assert.NotContains(t, resp.Body.String(), "secret")
}
