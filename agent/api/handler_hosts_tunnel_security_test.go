// handler_hosts_tunnel_security_test.go 验证 Host trust 配置变更会立即失效旧 tunnel 证据。
//
// 职责：
//   - 锁定 pin rotation 与显式清除后的 tunnel 状态转换
//   - 防止 Host 当前配置与已缓存 SSH 握手证据发生漂移
//
// 边界：
//   - 不建立真实 SSH 连接，使用携带已验证证据的 fake dialer
//   - 不验证 host-key 加密算法，算法合同由 tunnel 包测试覆盖
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

type blockingHostPinDialer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type failingTunnelInvalidationAuditStore struct {
	err error
}

type recoverableTunnelInvalidationAuditStore struct {
	mu           sync.Mutex
	events       []operation.AuditEvent
	failExecuted bool
	nextID       int
}

func (s failingTunnelInvalidationAuditStore) Append(context.Context, operation.AuditEvent) (operation.AuditEvent, error) {
	return operation.AuditEvent{}, s.err
}

func (s failingTunnelInvalidationAuditStore) List(context.Context, operation.AuditFilter) ([]operation.AuditEvent, error) {
	return nil, s.err
}

func (s *recoverableTunnelInvalidationAuditStore) Append(_ context.Context, event operation.AuditEvent) (operation.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Action == operation.AuditExecuted && s.failExecuted {
		return operation.AuditEvent{}, errors.New("audit completion unavailable")
	}
	s.nextID++
	if event.ID == "" {
		event.ID = fmt.Sprintf("audit-%d", s.nextID)
	}
	s.events = append(s.events, event)
	return event, nil
}

func (s *recoverableTunnelInvalidationAuditStore) List(_ context.Context, filter operation.AuditFilter) ([]operation.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]operation.AuditEvent, 0, len(s.events))
	for i := len(s.events) - 1; i >= 0; i-- {
		if filter.Kind == "" || s.events[i].Kind == filter.Kind {
			out = append(out, s.events[i])
		}
	}
	return out, nil
}

func (s *recoverableTunnelInvalidationAuditStore) setFailExecuted(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failExecuted = fail
}

func newBlockingHostPinDialer() *blockingHostPinDialer {
	return &blockingHostPinDialer{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingHostPinDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.once.Do(func() { close(d.started) })
	<-d.release
	identity, err := tunnel.HostKeyIdentitySHA256(target.SSHHostKeyFingerprint)
	if err != nil {
		return nil, err
	}
	return tunnel.NewFakeVerifiedConn(57100, identity), nil
}

func TestUpdateHostPinChangeInvalidatesExistingTunnelEvidence(t *testing.T) {
	tests := map[string]string{
		"rotate": `"ssh_host_key_fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`,
		"clear":  `"clear_ssh_host_key_fingerprint":true`,
	}
	for name, pinMutation := range tests {
		t.Run(name, func(t *testing.T) {
			app, err := NewApp(AppConfig{DataDir: t.TempDir()})
			require.NoError(t, err)
			defer app.Close()
			app.tunnels = tunnel.NewManager(successTunnelDialer{})
			host, err := app.remoteStore.AddHost(model.Host{
				ID:                    "h1",
				Name:                  "edge",
				SSHHost:               "ssh.example.com",
				SSHUser:               "deploy",
				SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
			})
			require.NoError(t, err)
			_, err = app.tunnels.EnsureConnected(tunnel.Target{
				HostID:                host.ID,
				SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
			})
			require.NoError(t, err)
			assert.True(t, app.tunnels.HostKeyEvidenceOf(host.ID).Verified)

			body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy",` + pinMutation + `}`)
			req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
			req.SetPathValue("id", host.ID)
			rr := httptest.NewRecorder()

			app.updateHost(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))
			assert.Equal(t, tunnel.HostKeyEvidence{}, app.tunnels.HostKeyEvidenceOf(host.ID))

			events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
			require.NoError(t, err)
			require.Len(t, events, 2)
			assert.Equal(t, operation.AuditExecuted, events[0].Action)
			assert.Equal(t, operation.AuditPrepared, events[1].Action)
			assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
			assert.Equal(t, host.ID, events[0].Plan.Target.HostID)
			assert.False(t, events[0].Plan.RequiresApproval)
			assert.Equal(t, "host_connection_config_changed", events[0].Data["trigger"])
			assert.Equal(t, []any{"ssh_host_key_fingerprint"}, events[0].Data["changed_fields"])
		})
	}
}

func TestUpdateHostPinChangeInvalidatesInFlightTunnelAttempt(t *testing.T) {
	tests := map[string]string{
		"rotate": `"ssh_host_key_fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`,
		"clear":  `"clear_ssh_host_key_fingerprint":true`,
	}
	for name, pinMutation := range tests {
		t.Run(name, func(t *testing.T) {
			app, err := NewApp(AppConfig{DataDir: t.TempDir()})
			require.NoError(t, err)
			defer app.Close()
			dialer := newBlockingHostPinDialer()
			app.tunnels = tunnel.NewManager(dialer)
			host, err := app.remoteStore.AddHost(model.Host{
				ID:                    "h1",
				Name:                  "edge",
				SSHHost:               "ssh.example.com",
				SSHUser:               "deploy",
				SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
			})
			require.NoError(t, err)
			result := make(chan error, 1)
			go func() {
				_, connectErr := app.tunnels.EnsureConnected(tunnel.Target{
					HostID:                host.ID,
					SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
				})
				result <- connectErr
			}()
			<-dialer.started

			body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy",` + pinMutation + `}`)
			req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
			req.SetPathValue("id", host.ID)
			rr := httptest.NewRecorder()
			app.updateHost(rr, req)
			require.Equal(t, http.StatusOK, rr.Code)

			close(dialer.release)
			require.Error(t, <-result)
			assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))
			assert.Zero(t, app.tunnels.LocalPort(host.ID))
			assert.Equal(t, tunnel.HostKeyEvidence{}, app.tunnels.HostKeyEvidenceOf(host.ID))
		})
	}
}

func TestUpdateHostPinChangeDoesNotPersistWhenAuditPreparationFails(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditErr := errors.New("audit storage unavailable")
	app.operationAudit = failingTunnelInvalidationAuditStore{err: auditErr}
	host, err := app.remoteStore.AddHost(model.Host{
		ID:                    "h1",
		Name:                  "edge",
		SSHHost:               "ssh.example.com",
		SSHUser:               "deploy",
		SSHHostKeyFingerprint: testHostPinA,
	})
	require.NoError(t, err)
	_, err = app.tunnels.EnsureConnected(tunnel.Target{
		HostID:                host.ID,
		SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
	})
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy","ssh_host_key_fingerprint":"` + testHostPinB + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
	req.SetPathValue("id", host.ID)
	rr := httptest.NewRecorder()

	app.updateHost(rr, req)

	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	var response struct {
		Code  string `json:"code"`
		Error string `json:"error"`
		Data  struct {
			Persisted         bool `json:"persisted"`
			TunnelInvalidated bool `json:"tunnel_invalidated"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.Equal(t, "tunnel_invalidation_audit_unavailable", response.Code)
	assert.Contains(t, response.Error, "not changed")
	assert.False(t, response.Data.Persisted)
	assert.False(t, response.Data.TunnelInvalidated)
	assert.Equal(t, tunnel.StatusConnected, app.tunnels.Status(host.ID))

	stored, found, err := app.remoteHostByID(host.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, testHostPinA, stored.SSHHostKeyFingerprint)
}

func TestUpdateHostPinChangeRetryCompletesPreparedAudit(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditStore := &recoverableTunnelInvalidationAuditStore{failExecuted: true}
	app.operationAudit = auditStore
	host, err := app.remoteStore.AddHost(model.Host{
		ID:                    "h1",
		Name:                  "edge",
		SSHHost:               "ssh.example.com",
		SSHUser:               "deploy",
		SSHHostKeyFingerprint: testHostPinA,
	})
	require.NoError(t, err)
	_, err = app.tunnels.EnsureConnected(tunnel.Target{
		HostID:                host.ID,
		SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
	})
	require.NoError(t, err)

	update := func() *httptest.ResponseRecorder {
		body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy","ssh_host_key_fingerprint":"` + testHostPinB + `"}`)
		req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
		req.SetPathValue("id", host.ID)
		rr := httptest.NewRecorder()
		app.updateHost(rr, req)
		return rr
	}

	first := update()
	require.Equal(t, http.StatusServiceUnavailable, first.Code)
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, operation.AuditPrepared, events[0].Action)
	assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))
	stored, found, err := app.remoteHostByID(host.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, testHostPinB, stored.SSHHostKeyFingerprint)

	auditStore.setFailExecuted(false)
	second := update()
	require.Equal(t, http.StatusOK, second.Code)
	events, err = auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}

func TestDeleteHostRetryCompletesPreparedAuditAfterRecordIsGone(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditStore := &recoverableTunnelInvalidationAuditStore{failExecuted: true}
	app.operationAudit = auditStore
	host, err := app.remoteStore.AddHost(model.Host{
		ID:                    "h1",
		Name:                  "edge",
		SSHHost:               "ssh.example.com",
		SSHUser:               "deploy",
		SSHHostKeyFingerprint: testHostPinA,
	})
	require.NoError(t, err)
	_, err = app.tunnels.EnsureConnected(tunnel.Target{
		HostID:                host.ID,
		SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
	})
	require.NoError(t, err)

	remove := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/hosts/"+host.ID, nil)
		req.SetPathValue("id", host.ID)
		rr := httptest.NewRecorder()
		app.deleteHost(rr, req)
		return rr
	}

	first := remove()
	require.Equal(t, http.StatusServiceUnavailable, first.Code)
	_, found, err := app.remoteHostByID(host.ID)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))

	auditStore.setFailExecuted(false)
	second := remove()
	require.Equal(t, http.StatusOK, second.Code)
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, operation.AuditPrepared, events[1].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}
