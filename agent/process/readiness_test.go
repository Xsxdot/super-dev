package process

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

func TestProbeReadyHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &model.ReadinessProbe{Type: "http", Target: srv.URL, TimeoutSeconds: 2}
	if err := ProbeReady(context.Background(), p); err != nil {
		t.Fatalf("expected ready, got %v", err)
	}
}

func TestProbeReadyTCPSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	p := &model.ReadinessProbe{Type: "tcp", Target: ln.Addr().String(), TimeoutSeconds: 2}
	if err := ProbeReady(context.Background(), p); err != nil {
		t.Fatalf("expected ready, got %v", err)
	}
}

func TestProbeReadyTimeout(t *testing.T) {
	p := &model.ReadinessProbe{Type: "tcp", Target: "127.0.0.1:1", TimeoutSeconds: 1}
	start := time.Now()
	if err := ProbeReady(context.Background(), p); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout overran: %v", time.Since(start))
	}
}
