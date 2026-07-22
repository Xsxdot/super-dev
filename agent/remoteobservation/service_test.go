// service_test.go 锁定安全远程观察模块的身份、运行态与直连暴露合同。
//
// 职责：
//   - 验证 machine-id 只以 SHA-256 摘要对外投影
//   - 验证 active collector 来自实际运行态，不是期望清单
//   - 验证固定端口直连探测的 reachable、refused、inconclusive 与 no-candidate 分支
//
// 边界：
//   - 不发起真实网络连接
//   - 不经过 HTTP handler，HTTP 安全投影由 api 包测试覆盖
package remoteobservation

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

type staticHostSource struct {
	host  HostAddressFacts
	found bool
	err   error
}

func (s staticHostSource) ObservationHost(_ context.Context, _ string) (HostAddressFacts, bool, error) {
	return s.host, s.found, s.err
}

func TestServiceLocalSystemFactsHashesMachineIDWithoutRawIdentity(t *testing.T) {
	service := &Service{
		agentNodeID:     "agent-node-01",
		operatingSystem: "linux",
		agentArch:       "amd64",
		kernelArch:      func() string { return "x86_64" },
		machineID:       func() ([]byte, error) { return []byte("8de277067b3544d4b65c267d0edab928\n"), nil },
	}

	facts := service.LocalSystemFacts(context.Background())

	assert.Equal(t, SystemFacts{
		OS:              "linux",
		KernelArch:      "x86_64",
		AgentArch:       "amd64",
		AgentNodeID:     "agent-node-01",
		MachineIDSHA256: "9c68dde752b9d1abaa475e2cd895eb0fbc8e29b05e3cab1430c01cc964c38c3d",
	}, facts)
	payload, err := json.Marshal(facts)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "8de277067b3544d4b65c267d0edab928")
	assert.NotContains(t, string(payload), `"machine_id":`)
	assert.NotContains(t, string(payload), `"hostname":`)
}

func TestCountActiveCollectorsUsesObservedRunningState(t *testing.T) {
	collectors := []model.Collector{
		{ID: "actual-running", Status: model.StatusRunning},
		{ID: "desired-but-starting", Status: model.StatusStarting},
		{ID: "stopped", Status: model.StatusStopped},
	}

	assert.Equal(t, 1, CountActiveCollectors(collectors))
}

func TestServiceObserveDirectExposureClassifiesAllSafeBranches(t *testing.T) {
	checkedAt := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name        string
		host        HostAddressFacts
		dialError   error
		reachable   bool
		want        DirectExposureObservation
		wantAddress []string
	}{
		{
			name:      "reachable means exposed",
			host:      HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"},
			reachable: true,
			want: DirectExposureObservation{
				HostID: "host-1", FixedPort: DirectExposurePort, CandidateCount: 1,
				DialAttemptCount: 1, ReachableCount: 1, CheckedAtUTC: checkedAt,
			},
			wantAddress: []string{"203.0.113.10:57017"},
		},
		{
			name:      "connection refused is conclusive not exposed",
			host:      HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10", PrivateIP: "10.20.30.40"},
			dialError: syscall.ECONNREFUSED,
			want: DirectExposureObservation{
				HostID: "host-1", FixedPort: DirectExposurePort, CandidateCount: 2,
				DialAttemptCount: 2, CheckedAtUTC: checkedAt,
			},
			wantAddress: []string{"203.0.113.10:57017", "10.20.30.40:57017"},
		},
		{
			name:      "timeout is conclusive unreachable",
			host:      HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"},
			dialError: context.DeadlineExceeded,
			want: DirectExposureObservation{
				HostID: "host-1", FixedPort: DirectExposurePort, CandidateCount: 1,
				DialAttemptCount: 1, CheckedAtUTC: checkedAt,
			},
			wantAddress: []string{"203.0.113.10:57017"},
		},
		{
			name:      "no route is conclusive unreachable",
			host:      HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"},
			dialError: syscall.ENETUNREACH,
			want: DirectExposureObservation{
				HostID: "host-1", FixedPort: DirectExposurePort, CandidateCount: 1,
				DialAttemptCount: 1, CheckedAtUTC: checkedAt,
			},
			wantAddress: []string{"203.0.113.10:57017"},
		},
		{
			name: "non literal and unsafe addresses produce no candidate",
			host: HostAddressFacts{HostID: "host-1", PublicIP: "attacker.example", PrivateIP: "169.254.169.254"},
			want: DirectExposureObservation{
				HostID: "host-1", FixedPort: DirectExposurePort, CheckedAtUTC: checkedAt,
			},
			wantAddress: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			addresses := []string{}
			service := &Service{
				hostSource: staticHostSource{host: test.host, found: true},
				now:        func() time.Time { return checkedAt },
				dialContext: func(_ context.Context, _, address string) (net.Conn, error) {
					addresses = append(addresses, address)
					if test.reachable {
						client, peer := net.Pipe()
						_ = peer.Close()
						return client, nil
					}
					return nil, test.dialError
				},
			}

			observed, err := service.ObserveDirectExposure(context.Background(), "host-1")

			require.NoError(t, err)
			assert.Equal(t, test.want, observed)
			assert.Equal(t, test.wantAddress, addresses)
		})
	}
}

func TestServiceObserveDirectExposureDeduplicatesLiteralCandidatesAndNeverUsesCallerAddress(t *testing.T) {
	addresses := []string{}
	service := &Service{
		hostSource: staticHostSource{host: HostAddressFacts{
			HostID: "host-1", PublicIP: "2001:0db8::1", PrivateIP: "2001:db8::1",
		}, found: true},
		now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) },
		dialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			addresses = append(addresses, address)
			return nil, syscall.ECONNREFUSED
		},
	}

	observed, err := service.ObserveDirectExposure(context.Background(), "host-1")

	require.NoError(t, err)
	assert.Equal(t, 1, observed.CandidateCount)
	assert.Equal(t, 1, observed.DialAttemptCount)
	assert.Equal(t, []string{"[2001:db8::1]:57017"}, addresses)
	rawHostJSON, err := json.Marshal(HostAddressFacts{HostID: "host-1", PublicIP: "10.20.30.40", PrivateIP: "192.168.1.20"})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(rawHostJSON), "internal address facts must be impossible to serialize into an API response")
}

func TestServiceObserveDirectExposureDistinguishesMissingHostFromObservationResult(t *testing.T) {
	service := &Service{hostSource: staticHostSource{found: false}}

	_, err := service.ObserveDirectExposure(context.Background(), "missing")

	assert.True(t, errors.Is(err, ErrHostNotFound))
}

func TestServiceObserveDirectExposureKeepsOnlyNonObservationsInconclusive(t *testing.T) {
	checkedAt := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	t.Run("parent context canceled during attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		service := &Service{
			hostSource: staticHostSource{host: HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"}, found: true},
			now:        func() time.Time { return checkedAt },
			dialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				cancel()
				return nil, ctx.Err()
			},
		}

		observed, err := service.ObserveDirectExposure(ctx, "host-1")

		require.NoError(t, err)
		assert.Equal(t, 1, observed.DialAttemptCount)
		assert.Equal(t, 1, observed.InconclusiveCount)
	})

	t.Run("dialer unavailable means not attempted", func(t *testing.T) {
		service := &Service{
			hostSource: staticHostSource{host: HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"}, found: true},
			now:        func() time.Time { return checkedAt },
		}

		observed, err := service.ObserveDirectExposure(context.Background(), "host-1")

		require.NoError(t, err)
		assert.Equal(t, 0, observed.DialAttemptCount)
		assert.Equal(t, 1, observed.InconclusiveCount)
	})

	t.Run("nil connection without error is inconclusive", func(t *testing.T) {
		service := &Service{
			hostSource: staticHostSource{host: HostAddressFacts{HostID: "host-1", PublicIP: "203.0.113.10"}, found: true},
			now:        func() time.Time { return checkedAt },
			dialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, nil
			},
		}

		observed, err := service.ObserveDirectExposure(context.Background(), "host-1")

		require.NoError(t, err)
		assert.Equal(t, 1, observed.DialAttemptCount)
		assert.Equal(t, 1, observed.InconclusiveCount)
	})
}
