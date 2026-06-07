package api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityHealthBypassesAuthWhilePendingBootstrap(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()

	resp := httptestDo(t, app, http.MethodGet, "/api/security/health", nil)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"provision_state":"pending-bootstrap"`)
}

func TestSecurityMiddlewareRequiresTokenAfterProvision(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()

	provision := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/provision",
		bytes.NewBufferString(`{"token":"long-token","tls_mode":"off"}`),
		http.Header{"Authorization": []string{"Bearer bootstrap"}},
	)
	require.Equal(t, http.StatusOK, provision.Code)

	unauthorized := httptestDo(t, app, http.MethodGet, "/api/exec/health", nil)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	authorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil,
		http.Header{"Authorization": []string{"Bearer long-token"}},
	)
	require.Equal(t, http.StatusOK, authorized.Code)
}
