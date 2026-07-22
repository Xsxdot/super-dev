package tunnel_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

const rotatedTestHostKeyFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type fakeDialer struct {
	port    int
	failOn  map[string]error
	calls   int
	hostKey tunnel.HostKeyEvidence
}

type blockingDialer struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type rotationBlockingDialer struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newRotationBlockingDialer() *rotationBlockingDialer {
	return &rotationBlockingDialer{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *rotationBlockingDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.mu.Lock()
	d.calls++
	call := d.calls
	d.mu.Unlock()
	if call == 2 {
		d.once.Do(func() { close(d.started) })
		<-d.release
	}
	identity, err := tunnel.HostKeyIdentitySHA256(target.SSHHostKeyFingerprint)
	if err != nil {
		return nil, err
	}
	return tunnel.NewFakeVerifiedConn(9000, identity), nil
}

func newBlockingDialer() *blockingDialer {
	return &blockingDialer{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.once.Do(func() { close(d.started) })
	<-d.release
	identity, err := tunnel.HostKeyIdentitySHA256(target.SSHHostKeyFingerprint)
	if err != nil {
		return nil, err
	}
	return tunnel.NewFakeVerifiedConn(9000, identity), nil
}

func (d *blockingDialer) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (f *fakeDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	f.calls++
	if err, ok := f.failOn[target.HostID]; ok {
		return nil, err
	}
	if f.hostKey.Verified {
		return tunnel.NewFakeVerifiedConn(f.port, f.hostKey.IdentitySHA256), nil
	}
	identity := "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68"
	if target.SSHHostKeyFingerprint == rotatedTestHostKeyFingerprint {
		identity = "568af50282cce162b257b4100cc889359994586bd4fbb45bb4fdfddaa8f22007"
	}
	return tunnel.NewFakeVerifiedConn(f.port, identity), nil
}

func TestManagerEnsureConnectedIsIdempotent(t *testing.T) {
	dialer := &fakeDialer{port: 12345}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	h := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}
	port1, err := mgr.EnsureConnected(h)
	require.NoError(t, err)
	assert.Equal(t, 12345, port1)
	assert.Equal(t, tunnel.StatusConnected, mgr.Status("h-1"))

	port2, err := mgr.EnsureConnected(h)
	require.NoError(t, err)
	assert.Equal(t, port1, port2)
	assert.Equal(t, 1, dialer.calls, "second EnsureConnected should not redial")
}

func TestManagerDialFailureMarkedFailed(t *testing.T) {
	dialer := &fakeDialer{failOn: map[string]error{"h-1": errors.New("bad")}}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint})
	require.Error(t, err)
	assert.Equal(t, tunnel.StatusFailed, mgr.Status("h-1"))
}

func TestManagerDisconnect(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint})
	require.NoError(t, err)

	mgr.Disconnect("h-1")
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status("h-1"))
}

func TestManagerRecordsVerifiedHostKeyEvidenceUntilDisconnect(t *testing.T) {
	dialer := &fakeDialer{
		port: 9000,
		hostKey: tunnel.HostKeyEvidence{
			Verified:       true,
			IdentitySHA256: "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68",
		},
	}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint})
	require.NoError(t, err)
	assert.Equal(t, dialer.hostKey, mgr.HostKeyEvidenceOf("h-1"))

	mgr.Disconnect("h-1")
	assert.Equal(t, tunnel.HostKeyEvidence{}, mgr.HostKeyEvidenceOf("h-1"))
}

func TestManagerDoesNotReuseConnectionAfterHostKeyPinRotation(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()
	initial := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}

	_, err := mgr.EnsureConnected(initial)
	require.NoError(t, err)
	_, err = mgr.EnsureConnected(initial)
	require.NoError(t, err)
	assert.Equal(t, 1, dialer.calls, "unchanged trusted pin should reuse the verified connection")

	rotated := initial
	rotated.SSHHostKeyFingerprint = rotatedTestHostKeyFingerprint
	_, err = mgr.EnsureConnected(rotated)
	require.NoError(t, err)
	assert.Equal(t, 2, dialer.calls, "rotated pin must force a fresh SSH handshake")
	assert.Equal(t, tunnel.HostKeyEvidence{
		Verified:       true,
		IdentitySHA256: "568af50282cce162b257b4100cc889359994586bd4fbb45bb4fdfddaa8f22007",
	}, mgr.HostKeyEvidenceOf("h-1"))
}

func TestManagerDisconnectInvalidatesInFlightHostKeyAttempt(t *testing.T) {
	dialer := newBlockingDialer()
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()
	target := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}
	result := make(chan error, 1)
	go func() {
		_, err := mgr.EnsureConnected(target)
		result <- err
	}()
	<-dialer.started

	mgr.Disconnect(target.HostID)
	close(dialer.release)

	require.Error(t, <-result)
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status(target.HostID))
	assert.Zero(t, mgr.LocalPort(target.HostID))
	assert.Equal(t, tunnel.HostKeyEvidence{}, mgr.HostKeyEvidenceOf(target.HostID))
}

func TestManagerDisconnectInvalidatesAllCallersWaitingOnSameAttempt(t *testing.T) {
	dialer := newBlockingDialer()
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()
	target := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}
	results := make(chan error, 2)
	go func() {
		_, err := mgr.EnsureConnected(target)
		results <- err
	}()
	<-dialer.started
	secondEntered := make(chan struct{})
	go func() {
		close(secondEntered)
		_, err := mgr.EnsureConnected(target)
		results <- err
	}()
	<-secondEntered
	// 第二个调用不触发 Dial，给它一个调度窗口进入同一 attempt 的等待分支。
	time.Sleep(20 * time.Millisecond)

	mgr.Disconnect(target.HostID)
	close(dialer.release)

	require.Error(t, <-results)
	require.Error(t, <-results)
	assert.Equal(t, 1, dialer.CallCount())
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status(target.HostID))
	assert.Zero(t, mgr.LocalPort(target.HostID))
	assert.Equal(t, tunnel.HostKeyEvidence{}, mgr.HostKeyEvidenceOf(target.HostID))
}

func TestManagerCloseInvalidatesInFlightHostKeyAttempt(t *testing.T) {
	dialer := newBlockingDialer()
	mgr := tunnel.NewManager(dialer)
	target := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}
	result := make(chan error, 1)
	go func() {
		_, err := mgr.EnsureConnected(target)
		result <- err
	}()
	<-dialer.started

	mgr.Close()
	close(dialer.release)

	require.Error(t, <-result)
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status(target.HostID))
	assert.Zero(t, mgr.LocalPort(target.HostID))
	assert.Equal(t, tunnel.HostKeyEvidence{}, mgr.HostKeyEvidenceOf(target.HostID))
	_, err := mgr.EnsureConnected(target)
	require.Error(t, err)
}

func TestManagerInvalidatorWinsRaceWithInFlightPinRotation(t *testing.T) {
	tests := map[string]func(*tunnel.Manager, string){
		"disconnect": func(manager *tunnel.Manager, hostID string) { manager.Disconnect(hostID) },
		"close":      func(manager *tunnel.Manager, _ string) { manager.Close() },
	}
	for name, invalidate := range tests {
		t.Run(name, func(t *testing.T) {
			dialer := newRotationBlockingDialer()
			mgr := tunnel.NewManager(dialer)
			defer mgr.Close()
			initial := tunnel.Target{HostID: "h-1", SSHHostKeyFingerprint: testHostKeyFingerprint}
			_, err := mgr.EnsureConnected(initial)
			require.NoError(t, err)

			rotated := initial
			rotated.SSHHostKeyFingerprint = rotatedTestHostKeyFingerprint
			result := make(chan error, 1)
			go func() {
				_, connectErr := mgr.EnsureConnected(rotated)
				result <- connectErr
			}()
			<-dialer.started

			invalidate(mgr, initial.HostID)
			close(dialer.release)

			require.Error(t, <-result)
			assert.Equal(t, tunnel.StatusDisconnected, mgr.Status(initial.HostID))
			assert.Zero(t, mgr.LocalPort(initial.HostID))
			assert.Equal(t, tunnel.HostKeyEvidence{}, mgr.HostKeyEvidenceOf(initial.HostID))
		})
	}
}

func TestManagerStatusSubscribe(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	ch := mgr.Subscribe("sub-1")
	defer mgr.Unsubscribe("sub-1")

	go func() {
		_, _ = mgr.EnsureConnected(tunnel.Target{HostID: "h-x", SSHHostKeyFingerprint: testHostKeyFingerprint})
	}()

	select {
	case ev := <-ch:
		assert.Equal(t, "h-x", ev.HostID)
		assert.Contains(t,
			[]tunnel.Status{tunnel.StatusConnecting, tunnel.StatusConnected},
			ev.Status,
		)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status event")
	}
}

func TestCredentialsFromHostPrefersStoredPrivateKeyMaterial(t *testing.T) {
	host := model.Host{
		SSHUser:               "deploy",
		SSHPassword:           "pw",
		SSHPrivateKey:         "inline-key",
		SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	}
	creds, err := tunnel.CredentialsFromHost(host)

	require.NoError(t, err)
	assert.Equal(t, "deploy", creds.User)
	assert.Equal(t, "pw", creds.Password)
	assert.Equal(t, []byte("inline-key"), creds.PrivateKey)
	assert.Equal(t, "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A", creds.HostKeyFingerprint)
}
