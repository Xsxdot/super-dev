// remote_node_mutation_app_test.go 验证远端节点连接配置应用服务的持久化与隧道失效合同。
//
// 职责：
//   - 锁定所有会改变 tunnel.Target 的 Host 与 Agent 字段
//   - 证明只有持久化成功后才会撤销旧隧道运行态
//   - 防止纯展示字段或安全状态更新误断现有隧道
//
// 边界：
//   - 不经过 HTTP 路由，直接测试应用服务事务顺序
//   - 不建立真实 SSH 连接，只记录失效请求
package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	testHostPinA = "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A"
	testHostPinB = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type fakeRemoteNodeHostStore struct {
	host      model.Host
	exists    bool
	listErr   error
	addErr    error
	updateErr error
	removeErr error
}

func (s *fakeRemoteNodeHostStore) ListHosts() ([]model.Host, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if !s.exists {
		return []model.Host{}, nil
	}
	return []model.Host{s.host}, nil
}

func (s *fakeRemoteNodeHostStore) AddHost(host model.Host) (model.Host, error) {
	if s.addErr != nil {
		return model.Host{}, s.addErr
	}
	if host.ID == "" {
		host.ID = "host-created"
	}
	s.host = host
	s.exists = true
	return host, nil
}

func (s *fakeRemoteNodeHostStore) UpdateHost(host model.Host) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.host = host
	s.exists = true
	return nil
}

func (s *fakeRemoteNodeHostStore) RemoveHost(string) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	s.exists = false
	return nil
}

type fakeRemoteNodeAgentStore struct {
	agent     model.Agent
	exists    bool
	lookupErr error
	upsertErr error
	removeErr error
}

func (s *fakeRemoteNodeAgentStore) AgentByHostID(string) (model.Agent, bool, error) {
	if s.lookupErr != nil {
		return model.Agent{}, false, s.lookupErr
	}
	return s.agent, s.exists, nil
}

func (s *fakeRemoteNodeAgentStore) UpsertAgent(agent model.Agent) (model.Agent, error) {
	if s.upsertErr != nil {
		return model.Agent{}, s.upsertErr
	}
	s.agent = agent
	s.exists = true
	return agent, nil
}

func (s *fakeRemoteNodeAgentStore) RemoveAgent(string) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	s.exists = false
	return nil
}

type recordingTunnelInvalidator struct {
	hostIDs []string
}

func (i *recordingTunnelInvalidator) Disconnect(hostID string) {
	i.hostIDs = append(i.hostIDs, hostID)
}

func TestRemoteNodeMutationApplicationUpdateHostInvalidatesEveryTunnelTargetField(t *testing.T) {
	base := remoteNodeMutationTestHost()
	tests := map[string]func(*hostWriteDTO){
		"ssh host":    func(dto *hostWriteDTO) { dto.SSHHost = "ssh-new.example.com" },
		"ssh port":    func(dto *hostWriteDTO) { dto.SSHPort = 2200 },
		"ssh user":    func(dto *hostWriteDTO) { dto.SSHUser = "release" },
		"password":    func(dto *hostWriteDTO) { dto.SSHPassword = "password-b" },
		"private key": func(dto *hostWriteDTO) { dto.SSHPrivateKey = "private-key-b" },
		"pin rotate":  func(dto *hostWriteDTO) { dto.SSHHostKeyFingerprint = testHostPinB },
		"pin clear": func(dto *hostWriteDTO) {
			dto.SSHHostKeyFingerprint = ""
			dto.ClearSSHHostKeyPin = true
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
			invalidator := &recordingTunnelInvalidator{}
			app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)
			dto := remoteNodeMutationHostDTO(base)
			mutate(&dto)

			_, err := app.UpdateHost(base.ID, dto)

			require.NoError(t, err)
			assert.Equal(t, []string{base.ID}, invalidator.hostIDs)
		})
	}
}

func TestRemoteNodeMutationApplicationUpdateHostDoesNotInvalidateDisplayFields(t *testing.T) {
	base := remoteNodeMutationTestHost()
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)
	dto := remoteNodeMutationHostDTO(base)
	dto.Name = "renamed"
	dto.Tags = []string{"prod", "gpu"}

	_, err := app.UpdateHost(base.ID, dto)

	require.NoError(t, err)
	assert.Empty(t, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationUpdateHostPersistenceFailureKeepsTunnel(t *testing.T) {
	base := remoteNodeMutationTestHost()
	storeErr := errors.New("disk unavailable")
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true, updateErr: storeErr}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)
	dto := remoteNodeMutationHostDTO(base)
	dto.SSHHost = "ssh-new.example.com"

	_, err := app.UpdateHost(base.ID, dto)

	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationHostAddAndRemoveInvalidationContract(t *testing.T) {
	t.Run("add does not invalidate", func(t *testing.T) {
		hosts := &fakeRemoteNodeHostStore{}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)

		_, err := app.AddHost(remoteNodeMutationHostDTO(remoteNodeMutationTestHost()))

		require.NoError(t, err)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("remove success invalidates", func(t *testing.T) {
		hosts := &fakeRemoteNodeHostStore{host: remoteNodeMutationTestHost(), exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)

		err := app.RemoveHost("host-1")

		require.NoError(t, err)
		assert.Equal(t, []string{"host-1"}, invalidator.hostIDs)
	})

	t.Run("remove persistence failure keeps tunnel", func(t *testing.T) {
		storeErr := errors.New("disk unavailable")
		hosts := &fakeRemoteNodeHostStore{host: remoteNodeMutationTestHost(), exists: true, removeErr: storeErr}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, invalidator)

		err := app.RemoveHost("host-1")

		require.ErrorIs(t, err, storeErr)
		assert.Empty(t, invalidator.hostIDs)
	})
}

func TestRemoteNodeMutationApplicationAgentPortChangeInvalidatesAfterPersistence(t *testing.T) {
	base := remoteNodeMutationTestAgent(57017)
	agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, invalidator)
	updated := remoteNodeMutationTestAgent(57018)

	_, err := app.UpsertAgent(updated)

	require.NoError(t, err)
	assert.Equal(t, []string{base.HostID}, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationAgentNonTunnelAndFailureContract(t *testing.T) {
	t.Run("non-target config security and runtime changes do not invalidate", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, invalidator)
		updated := base
		updated.Config.ListenPort = 58000
		updated.Secret.Token = "rotated-token"
		updated.Security.TokenConfigured = true
		updated.Runtime = model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true, LocalPort: 57123}

		_, err := app.UpsertAgent(updated)

		require.NoError(t, err)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("persistence failure keeps tunnel", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		storeErr := errors.New("disk unavailable")
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true, upsertErr: storeErr}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, invalidator)

		_, err := app.UpsertAgent(remoteNodeMutationTestAgent(57018))

		require.ErrorIs(t, err, storeErr)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("remove tunnel agent invalidates", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, invalidator)

		err := app.RemoveAgent(base.HostID)

		require.NoError(t, err)
		assert.Equal(t, []string{base.HostID}, invalidator.hostIDs)
	})
}

func remoteNodeMutationTestHost() model.Host {
	return model.Host{
		ID:                    "host-1",
		Name:                  "edge",
		Tags:                  []string{"prod"},
		SSHHost:               "ssh.example.com",
		SSHPort:               22,
		SSHUser:               "deploy",
		SSHPassword:           "password-a",
		SSHPrivateKey:         "private-key-a",
		SSHHostKeyFingerprint: testHostPinA,
	}
}

func remoteNodeMutationHostDTO(host model.Host) hostWriteDTO {
	return hostWriteDTO{
		ID:                    host.ID,
		Name:                  host.Name,
		PublicIP:              host.PublicIP,
		PrivateIP:             host.PrivateIP,
		Tags:                  append([]string(nil), host.Tags...),
		SSHHost:               host.SSHHost,
		SSHPort:               host.SSHPort,
		SSHUser:               host.SSHUser,
		SSHPassword:           host.SSHPassword,
		SSHPrivateKey:         host.SSHPrivateKey,
		SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
	}
}

func remoteNodeMutationTestAgent(port int) model.Agent {
	return model.Agent{
		HostID: "host-1",
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeTunnel,
			Tunnel: &model.TunnelParams{RemoteAgentPort: port},
		}}},
	}
}
