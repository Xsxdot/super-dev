// handler_remote_observation_test.go 锁定远程观察 HTTP 表面的认证、安全投影与运行态合同。
//
// 职责：
//   - 验证 NodeStatus.system 经 Registry 从 authenticated GET /api/nodes 返回
//   - 验证 direct-exposure 只接收 host_id，且不返回 IP 或底层错误
//   - 验证 managed status 分离 desired collector 与实际 active collector
//
// 边界：
//   - 不建立真实 SSH 隧道
//   - 直连探测网络分支由 remoteobservation 包测试覆盖
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

type fakeRemoteObservation struct {
	facts     remoteobservation.SystemFacts
	direct    remoteobservation.DirectExposureObservation
	directErr error
	hostIDs   []string
}

func (f *fakeRemoteObservation) LocalSystemFacts(context.Context) remoteobservation.SystemFacts {
	return f.facts
}

func (f *fakeRemoteObservation) ObserveDirectExposure(_ context.Context, hostID string) (remoteobservation.DirectExposureObservation, error) {
	f.hostIDs = append(f.hostIDs, hostID)
	return f.direct, f.directErr
}

func TestAuthenticatedNodeRegistryReturnsOnlyHashedSystemFacts(t *testing.T) {
	registry := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	observer := &fakeRemoteObservation{facts: remoteobservation.SystemFacts{
		OS: "linux", KernelArch: "x86_64", AgentArch: "amd64", AgentNodeID: "agent-node-01",
		MachineIDSHA256: "9c68dde752b9d1abaa475e2cd895eb0fbc8e29b05e3cab1430c01cc964c38c3d",
	}}
	app, err := NewApp(AppConfig{
		DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true,
		NodeRegistryOverride: registry, RemoteObservationOverride: observer,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	registry.ApplyForTest([]nodetransport.NodeStatus{app.nodeStatusSnapshot(context.Background(), "host-1", "host-1")})

	provision := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/provision",
		bytes.NewBufferString(`{"token":"long-token","tls_mode":"off"}`),
		map[string]string{"Authorization": "Bearer bootstrap"},
	)
	require.Equal(t, http.StatusOK, provision.Code)
	// 显式空 Authorization 关掉 helper 默认注入的本机 token，验证真正裸请求被拒。
	unauthorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/nodes", nil, map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	authorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/nodes", nil,
		map[string]string{"Authorization": "Bearer long-token"},
	)
	require.Equal(t, http.StatusOK, authorized.Code)
	assert.Contains(t, authorized.Body.String(), `"system":{"os":"linux","kernel_arch":"x86_64","agent_arch":"amd64","agent_node_id":"agent-node-01","machine_id_sha256":"9c68dde752b9d1abaa475e2cd895eb0fbc8e29b05e3cab1430c01cc964c38c3d"}`)
	assert.NotContains(t, authorized.Body.String(), "8de277067b3544d4b65c267d0edab928")
	assert.NotContains(t, authorized.Body.String(), `"machine_id":`)
	assert.NotContains(t, authorized.Body.String(), `"hostname":`)
}

func TestDirectExposureEndpointProjectsFixedSafeShapeAndRejectsCallerTargets(t *testing.T) {
	checkedAt := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	observer := &fakeRemoteObservation{direct: remoteobservation.DirectExposureObservation{
		HostID: "host-1", FixedPort: remoteobservation.DirectExposurePort, CandidateCount: 2,
		DialAttemptCount: 2, ReachableCount: 0, InconclusiveCount: 0, CheckedAtUTC: checkedAt,
	}}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), RemoteObservationOverride: observer})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents/host-1/direct-exposure", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var response map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	keys := make([]string, 0, len(response))
	for key := range response {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"candidate_count", "checked_at_utc", "dial_attempt_count", "fixed_port", "host_id", "inconclusive_count", "reachable_count",
	}, keys)
	assert.Equal(t, float64(57017), response["fixed_port"])
	assert.Equal(t, []string{"host-1"}, observer.hostIDs)
	assert.NotContains(t, rr.Body.String(), "10.20.30.40")

	ssrf := httptest.NewRecorder()
	ssrfReq := httptest.NewRequest(http.MethodGet, "/api/agents/host-1/direct-exposure?address=169.254.169.254&port=80", nil)
	ssrfReq.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(ssrf, ssrfReq)
	assert.Equal(t, http.StatusBadRequest, ssrf.Code)
	assert.Equal(t, []string{"host-1"}, observer.hostIDs, "query target must be rejected before the observer is called")
	assert.NotContains(t, ssrf.Body.String(), "169.254.169.254")

	bodyOverride := httptest.NewRecorder()
	bodyReq := httptest.NewRequest(http.MethodGet, "/api/agents/host-1/direct-exposure", bytes.NewBufferString(`{"address":"127.0.0.1","port":1}`))
	bodyReq.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(bodyOverride, bodyReq)
	assert.Equal(t, http.StatusBadRequest, bodyOverride.Code)
	assert.Equal(t, []string{"host-1"}, observer.hostIDs, "body target must be rejected before the observer is called")
	assert.NotContains(t, bodyOverride.Body.String(), "127.0.0.1")
}

func TestDirectExposureEndpointDoesNotReturnInternalObservationError(t *testing.T) {
	observer := &fakeRemoteObservation{directErr: errors.New("dial tcp 10.20.30.40:57017: secret route failure")}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), RemoteObservationOverride: observer})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents/host-1/direct-exposure", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.NotContains(t, rr.Body.String(), "10.20.30.40")
	assert.NotContains(t, rr.Body.String(), "secret route failure")
}

func TestDirectExposureEndpointRequiresAuthenticationAfterProvision(t *testing.T) {
	observer := &fakeRemoteObservation{direct: remoteobservation.DirectExposureObservation{
		HostID: "host-1", FixedPort: remoteobservation.DirectExposurePort, CheckedAtUTC: time.Now().UTC(),
	}}
	app, err := NewApp(AppConfig{
		DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true,
		RemoteObservationOverride: observer,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	provision := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/provision",
		bytes.NewBufferString(`{"token":"long-token","tls_mode":"off"}`),
		map[string]string{"Authorization": "Bearer bootstrap"},
	)
	require.Equal(t, http.StatusOK, provision.Code)

	// 显式空 Authorization 关掉 helper 默认注入的本机 token，验证真正裸请求被拒。
	unauthorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/agents/host-1/direct-exposure", nil, map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.Empty(t, observer.hostIDs)
	authorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/agents/host-1/direct-exposure", nil,
		map[string]string{"Authorization": "Bearer long-token"},
	)
	assert.Equal(t, http.StatusOK, authorized.Code)
	assert.Equal(t, []string{"host-1"}, observer.hostIDs)
}

func TestManagedStatusSeparatesDesiredZeroFromActualActiveCollector(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.collector.StartForTest("unmanaged-observed", model.LogSourceTypeCommand, []string{"sleep", "30"})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/managed-deployments/status", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var status model.ManagedDeploymentStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&status))
	assert.Equal(t, 0, status.CollectorCount)
	assert.Equal(t, 1, status.ActiveCollectorCount)
}

func TestHostManagedStatusProjectsRemoteActiveCountSeparatelyFromDesired(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, model.ManagedDeploymentStatus{CollectorCount: 0, ActiveCollectorCount: 1})
	}))
	t.Cleanup(remoteServer.Close)
	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "host-1", Name: "validation"})
	require.NoError(t, err)
	transport := testNodeTransport{table: map[string]string{"host-1": remoteServer.URL}}
	app.nodeTransport = transport
	app.managedReconciler = NewHostDeploymentReconciler(app, transport, time.Hour)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/host-1/managed-deployments/status", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var status model.HostManagedDeploymentStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&status))
	assert.Equal(t, 0, status.DesiredCollectorCount)
	assert.Equal(t, 1, status.ActiveCollectorCount)
}
