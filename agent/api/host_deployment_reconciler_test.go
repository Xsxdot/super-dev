// host_deployment_reconciler_test.go 验证桌面端 managed deployment 推送器。
//
// 职责：
//   - 覆盖按 host 投影 remote deployment 的规则
//   - 覆盖通过隧道 PUT 完整 desired 清单
//   - 覆盖隧道 connected 事件和周期 tick 触发 reconcile
//
// 边界：
//   - 不建立真实 SSH 隧道
//   - 不测试远端 agent apply 逻辑
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

func TestHostDeploymentReconcilerDesiredForHostRewritesLocationAndFilters(t *testing.T) {
	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(model.Project{
		ID: "proj", Name: "proj",
		Services: []model.Service{{
			ID: "svc-api", ProjectID: "proj", Name: "api",
			Deployments: []model.Deployment{
				{
					ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1", "h2"},
					Runtime: &model.RuntimeConfig{Type: model.RuntimeTypeSystemd, ServiceName: "api.service"},
					Logs:    &model.LogConfig{Type: model.LogKindJournalctl, Target: "api.service"},
				},
				{ID: "dep-local", EnvName: "dev", Location: model.LocationLocal},
			},
		}},
	})
	app.mu.Unlock()
	reconciler := NewHostDeploymentReconciler(app, testNodeTransport{}, time.Second)

	got := reconciler.DesiredForHost("h1")

	require.Len(t, got, 1)
	assert.Equal(t, "dep-remote", got[0].DeploymentID)
	assert.Equal(t, "svc-api", got[0].ServiceID)
	assert.Equal(t, "api", got[0].ServiceName)
	assert.Equal(t, "proj", got[0].ProjectID)
	assert.Equal(t, model.LocationLocal, got[0].Location)
	assert.Equal(t, model.RuntimeTypeSystemd, got[0].Runtime.Type)
	assert.Equal(t, model.LogKindJournalctl, got[0].Logs.Type)
}

func TestHostDeploymentReconcilerReconcileSkipsDisconnectedTunnel(t *testing.T) {
	app := newTestAppForPackage(t)
	reconciler := NewHostDeploymentReconciler(app, testNodeTransport{table: map[string]string{}}, time.Second)

	err := reconciler.Reconcile(context.Background(), "missing")

	require.NoError(t, err)
}

func TestHostDeploymentReconcilerReconcilePutsFullDesiredBody(t *testing.T) {
	var received []model.ManagedDeployment
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/api/managed-deployments", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		jsonOK(w, model.ManagedDeploymentReconcileResult{Persisted: true})
	}))
	t.Cleanup(remoteSrv.Close)

	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(model.Project{
		ID: "proj", Name: "proj",
		Services: []model.Service{{
			ID: "svc-api", ProjectID: "proj", Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1"},
				Logs: &model.LogConfig{Type: model.LogKindCommand, Command: "printf log"},
			}},
		}},
	})
	app.mu.Unlock()
	reconciler := NewHostDeploymentReconciler(app, testNodeTransport{table: map[string]string{"h1": remoteSrv.URL}}, time.Second)

	err := reconciler.Reconcile(context.Background(), "h1")

	require.NoError(t, err)
	require.Len(t, received, 1)
	assert.Equal(t, model.LocationLocal, received[0].Location)
	assert.Equal(t, "dep-remote", received[0].DeploymentID)
	assert.Equal(t, model.LogKindCommand, received[0].Logs.Type)
}

func TestHostDeploymentReconcilerSerializesSameHostReconciles(t *testing.T) {
	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(managedReconcileProject("proj-one", "dep-one"))
	app.mu.Unlock()
	transport := newBlockingManagedDeploymentTransport()
	reconciler := NewHostDeploymentReconciler(app, transport, time.Hour)

	firstErr := make(chan error, 1)
	go func() {
		firstErr <- reconciler.Reconcile(context.Background(), "h1")
	}()
	firstBody := receiveManagedDeploymentBody(t, transport.firstStarted)
	require.Len(t, firstBody, 1)

	app.mu.Lock()
	app.appendProjectLocked(managedReconcileProject("proj-two", "dep-two"))
	app.mu.Unlock()
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- reconciler.Reconcile(context.Background(), "h1")
	}()

	// 旧请求仍在传输层时，后续同 host 的完整清单不能抢先发送；
	// 否则旧清单若最后抵达远端，会把新 deployment 覆盖掉。
	select {
	case body := <-transport.secondStarted:
		t.Fatalf("second reconcile entered transport before first completed with %d deployments", len(body))
	case <-time.After(50 * time.Millisecond):
	}

	close(transport.releaseFirst)
	require.NoError(t, <-firstErr)
	secondBody := receiveManagedDeploymentBody(t, transport.secondStarted)
	require.Len(t, secondBody, 2)
	require.NoError(t, <-secondErr)
}

func TestHostDeploymentReconcilerRunHandlesConnectedEventAndTick(t *testing.T) {
	requests := make(chan string, 4)
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		jsonOK(w, model.ManagedDeploymentReconcileResult{Persisted: true})
	}))
	t.Cleanup(remoteSrv.Close)

	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(model.Project{
		ID: "proj", Name: "proj",
		Services: []model.Service{{
			ID: "svc-api", ProjectID: "proj", Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1"},
				Logs: &model.LogConfig{Type: model.LogKindCommand, Command: "printf log"},
			}},
		}},
	})
	app.mu.Unlock()
	reconciler := NewHostDeploymentReconciler(app, testNodeTransport{table: map[string]string{"h1": remoteSrv.URL}}, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan tunnel.Event, 1)
	ticks := make(chan time.Time, 1)
	go reconciler.run(ctx, events, ticks)

	events <- tunnel.Event{HostID: "h1", Status: tunnel.StatusConnected}
	require.Eventually(t, func() bool { return len(requests) >= 1 }, time.Second, 10*time.Millisecond)
	ticks <- time.Now()
	require.Eventually(t, func() bool { return len(requests) >= 2 }, time.Second, 10*time.Millisecond)
}

func TestReconcileProjectsAsyncPushesAffectedHost(t *testing.T) {
	requests := make(chan []model.ManagedDeployment, 1)
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []model.ManagedDeployment
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests <- body
		jsonOK(w, model.ManagedDeploymentReconcileResult{Persisted: true})
	}))
	t.Cleanup(remoteSrv.Close)

	app := newTestAppForPackage(t)
	app.managedReconciler = NewHostDeploymentReconciler(app, testNodeTransport{table: map[string]string{"h1": remoteSrv.URL}}, time.Hour)
	project := model.Project{
		ID: "proj", Name: "proj",
		Services: []model.Service{{
			ID: "svc-api", ProjectID: "proj", Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1"},
				Logs: &model.LogConfig{Type: model.LogKindCommand, Command: "printf log"},
			}},
		}},
	}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()

	app.reconcileProjectsAsync(project)

	require.Eventually(t, func() bool { return len(requests) == 1 }, time.Second, 10*time.Millisecond)
}

func TestGetHostManagedDeploymentsStatusProxiesRemoteState(t *testing.T) {
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/managed-deployments/status", r.URL.Path)
		jsonOK(w, model.ManagedDeploymentStatus{
			DeploymentCount: 1,
			CollectorCount:  1,
			Collectors: []model.ManagedCollectorStatus{{
				DeploymentID: "dep-remote",
				ServiceName:  "api",
				Name:         "api.service",
				Type:         model.LogSourceTypeJournalctl,
				Desired:      true,
				Running:      true,
			}},
		})
	}))
	t.Cleanup(remoteSrv.Close)

	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	transport := testNodeTransport{table: map[string]string{"h1": remoteSrv.URL}}
	app.nodeTransport = transport
	app.managedReconciler = NewHostDeploymentReconciler(app, transport, time.Hour)
	app.mu.Lock()
	app.appendProjectLocked(model.Project{
		ID: "proj", Name: "proj",
		Services: []model.Service{{
			ID: "svc-api", ProjectID: "proj", Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1"},
				Logs: &model.LogConfig{Type: model.LogKindJournalctl, Target: "api.service"},
			}},
		}},
	})
	app.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/h1/managed-deployments/status", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got model.HostManagedDeploymentStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.True(t, got.TunnelConnected)
	assert.Equal(t, 1, got.DesiredDeploymentCount)
	assert.Equal(t, 1, got.DesiredCollectorCount)
	require.NotNil(t, got.Remote)
	assert.Equal(t, 1, got.Remote.CollectorCount)
}

func managedReconcileProject(projectID, deploymentID string) model.Project {
	return model.Project{
		ID: projectID, Name: projectID,
		Services: []model.Service{{
			ID: "svc-" + projectID, ProjectID: projectID, Name: "api",
			Deployments: []model.Deployment{{
				ID: deploymentID, EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h1"},
				Logs: &model.LogConfig{Type: model.LogKindCommand, Command: "printf log"},
			}},
		}},
	}
}

type blockingManagedDeploymentTransport struct {
	mu            sync.Mutex
	calls         int
	firstStarted  chan []model.ManagedDeployment
	secondStarted chan []model.ManagedDeployment
	releaseFirst  chan struct{}
}

func newBlockingManagedDeploymentTransport() *blockingManagedDeploymentTransport {
	return &blockingManagedDeploymentTransport{
		firstStarted:  make(chan []model.ManagedDeployment, 1),
		secondStarted: make(chan []model.ManagedDeployment, 1),
		releaseFirst:  make(chan struct{}),
	}
}

func (t *blockingManagedDeploymentTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if hostID != "h1" {
		return nodetransport.NodeResponse{}, nodetransport.ErrHostUnreachable
	}
	var body []model.ManagedDeployment
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nodetransport.NodeResponse{}, err
		}
	}
	t.mu.Lock()
	t.calls++
	call := t.calls
	t.mu.Unlock()
	if call == 1 {
		t.firstStarted <- body
		select {
		case <-ctx.Done():
			return nodetransport.NodeResponse{}, ctx.Err()
		case <-t.releaseFirst:
		}
	} else {
		t.secondStarted <- body
	}
	return nodetransport.NodeResponse{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (t *blockingManagedDeploymentTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t *blockingManagedDeploymentTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t *blockingManagedDeploymentTransport) Covers() []string {
	return []string{"h1"}
}

func receiveManagedDeploymentBody(t *testing.T, ch <-chan []model.ManagedDeployment) []model.ManagedDeployment {
	t.Helper()
	select {
	case body := <-ch:
		return body
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for managed deployment reconcile request")
		return nil
	}
}
