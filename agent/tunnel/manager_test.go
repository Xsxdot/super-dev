package tunnel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/tunnel"
)

type fakeDialer struct {
	port   int
	failOn map[string]error
	calls  int
}

func (f *fakeDialer) Dial(host model.Host) (*tunnel.Conn, error) {
	f.calls++
	if err, ok := f.failOn[host.ID]; ok {
		return nil, err
	}
	return tunnel.NewFakeConn(f.port), nil
}

func TestManagerEnsureConnectedIsIdempotent(t *testing.T) {
	dialer := &fakeDialer{port: 12345}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	h := model.Host{ID: "h-1", Name: "c01"}
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

	_, err := mgr.EnsureConnected(model.Host{ID: "h-1"})
	require.Error(t, err)
	assert.Equal(t, tunnel.StatusFailed, mgr.Status("h-1"))
}

func TestManagerDisconnect(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(model.Host{ID: "h-1"})
	require.NoError(t, err)

	mgr.Disconnect("h-1")
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status("h-1"))
}

func TestManagerStatusSubscribe(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	ch := mgr.Subscribe("sub-1")
	defer mgr.Unsubscribe("sub-1")

	go func() {
		_, _ = mgr.EnsureConnected(model.Host{ID: "h-x"})
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
