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
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiassembler "github.com/xsxdot/super-dev/agent/api/internal/assembler"
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
	hostIDs       []string
	invalidations []tunnelRuntimeInvalidation
	recoveries    []tunnelRuntimeInvalidationRecovery
	prepareErr    error
	completeErr   error
	recoverErr    error
}

type blockingCompletionTunnelInvalidator struct {
	mu             sync.Mutex
	applyCalls     int
	firstPersisted chan struct{}
	releaseFirst   chan struct{}
	recoverCalled  chan struct{}
	recoverOnce    sync.Once
}

func (i *blockingCompletionTunnelInvalidator) Apply(_ context.Context, _ tunnelRuntimeInvalidation, persist func() error) (tunnelRuntimeInvalidationResult, error) {
	i.mu.Lock()
	i.applyCalls++
	call := i.applyCalls
	i.mu.Unlock()

	result := tunnelRuntimeInvalidationResult{AuditPrepared: true}
	if err := persist(); err != nil {
		return result, err
	}
	result.Persisted = true
	result.TunnelInvalidated = true
	if call == 1 {
		close(i.firstPersisted)
		<-i.releaseFirst
		return result, errors.New("audit completion unavailable")
	}
	result.AuditCompleted = true
	return result, nil
}

func (i *blockingCompletionTunnelInvalidator) Recover(_ context.Context, _ tunnelRuntimeInvalidationRecovery) (tunnelRuntimeInvalidationResult, error) {
	i.recoverOnce.Do(func() { close(i.recoverCalled) })
	return tunnelRuntimeInvalidationResult{
		AuditPrepared:     true,
		Persisted:         true,
		TunnelInvalidated: true,
		AuditCompleted:    true,
	}, nil
}

func (i *recordingTunnelInvalidator) Apply(_ context.Context, invalidation tunnelRuntimeInvalidation, persist func() error) (tunnelRuntimeInvalidationResult, error) {
	var result tunnelRuntimeInvalidationResult
	if i.prepareErr != nil {
		return result, i.prepareErr
	}
	result.AuditPrepared = true
	if err := persist(); err != nil {
		return result, err
	}
	result.Persisted = true
	result.TunnelInvalidated = true
	i.hostIDs = append(i.hostIDs, invalidation.HostID)
	i.invalidations = append(i.invalidations, invalidation)
	if i.completeErr != nil {
		return result, i.completeErr
	}
	result.AuditCompleted = true
	return result, nil
}

func (i *recordingTunnelInvalidator) Recover(_ context.Context, recovery tunnelRuntimeInvalidationRecovery) (tunnelRuntimeInvalidationResult, error) {
	i.recoveries = append(i.recoveries, recovery)
	if i.recoverErr != nil {
		return tunnelRuntimeInvalidationResult{}, i.recoverErr
	}
	return tunnelRuntimeInvalidationResult{AuditPrepared: true, AuditCompleted: true}, nil
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
			dto.ClearSSHHostKeyFingerprint = true
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
			invalidator := &recordingTunnelInvalidator{}
			app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
			dto := remoteNodeMutationHostDTO(base)
			mutate(&dto)

			_, err := app.UpdateHost(context.Background(), base.ID, dto)

			require.NoError(t, err)
			assert.Equal(t, []string{base.ID}, invalidator.hostIDs)
		})
	}
}

func TestRemoteNodeMutationApplicationUpdateHostDoesNotInvalidateDisplayFields(t *testing.T) {
	base := remoteNodeMutationTestHost()
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
	dto := remoteNodeMutationHostDTO(base)
	dto.Name = "renamed"
	dto.Tags = []string{"prod", "gpu"}
	dto.DevMachineMode = true // display/behavior 字段翻转：不动 SSH 身份，不应触发隧道失效

	updated, err := app.UpdateHost(context.Background(), base.ID, dto)

	require.NoError(t, err)
	assert.True(t, updated.DevMachineMode, "DevMachineMode 翻转应正常持久化")
	assert.Empty(t, invalidator.hostIDs, "display 字段变更不应调用 tunnel 失效")
	assert.Empty(t, hosts.host.PendingTunnelInvalidationRevision, "display 字段变更不应产生待完成的 tunnel 失效 outbox 标记")
}

func TestRemoteNodeMutationApplicationUpdateHostPersistenceFailureKeepsTunnel(t *testing.T) {
	base := remoteNodeMutationTestHost()
	storeErr := errors.New("disk unavailable")
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true, updateErr: storeErr}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
	dto := remoteNodeMutationHostDTO(base)
	dto.SSHHost = "ssh-new.example.com"

	_, err := app.UpdateHost(context.Background(), base.ID, dto)

	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationUpdateHostReturnsPartialErrorAfterInvalidationAuditFailure(t *testing.T) {
	base := remoteNodeMutationTestHost()
	auditErr := errors.New("audit unavailable")
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
	invalidator := &recordingTunnelInvalidator{completeErr: auditErr}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
	dto := remoteNodeMutationHostDTO(base)
	dto.SSHHost = "ssh-new.example.com"

	updated, err := app.UpdateHost(context.Background(), base.ID, dto)

	require.Error(t, err)
	assert.ErrorIs(t, err, auditErr)
	assert.True(t, isTunnelInvalidationAuditError(err))
	assert.Equal(t, "ssh-new.example.com", updated.SSHHost)
	assert.Equal(t, "ssh-new.example.com", hosts.host.SSHHost)
	assert.NotEmpty(t, hosts.host.PendingTunnelInvalidationRevision)
	assert.Equal(t, []string{base.ID}, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationSerializesSameHostMutations(t *testing.T) {
	base := remoteNodeMutationTestHost()
	hosts := &fakeRemoteNodeHostStore{host: base, exists: true}
	invalidator := &blockingCompletionTunnelInvalidator{
		firstPersisted: make(chan struct{}),
		releaseFirst:   make(chan struct{}),
		recoverCalled:  make(chan struct{}),
	}
	app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
	firstDTO := remoteNodeMutationHostDTO(base)
	firstDTO.SSHHost = "ssh-new.example.com"
	secondDTO := firstDTO
	secondDTO.Name = "renamed"

	firstErr := make(chan error, 1)
	go func() {
		_, err := app.UpdateHost(context.Background(), base.ID, firstDTO)
		firstErr <- err
	}()
	<-invalidator.firstPersisted

	secondStarted := make(chan struct{})
	secondErr := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := app.UpdateHost(context.Background(), base.ID, secondDTO)
		secondErr <- err
	}()
	<-secondStarted
	select {
	case <-invalidator.recoverCalled:
		t.Fatal("第二次 mutation 不应在第一次完成审计结果返回前恢复 pending plan")
	case <-time.After(100 * time.Millisecond):
	}

	close(invalidator.releaseFirst)
	require.Error(t, <-firstErr)
	require.NoError(t, <-secondErr)
	select {
	case <-invalidator.recoverCalled:
	case <-time.After(time.Second):
		t.Fatal("第一次 mutation 返回后，第二次 mutation 应恢复 pending plan")
	}
	assert.Equal(t, "ssh-new.example.com", hosts.host.SSHHost)
	assert.Equal(t, "renamed", hosts.host.Name)
	assert.Empty(t, hosts.host.PendingTunnelInvalidationRevision)
}

func TestRemoteNodeMutationApplicationHostAddAndRemoveInvalidationContract(t *testing.T) {
	t.Run("add does not invalidate", func(t *testing.T) {
		hosts := &fakeRemoteNodeHostStore{}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)

		_, err := app.AddHost(context.Background(), remoteNodeMutationHostDTO(remoteNodeMutationTestHost()))

		require.NoError(t, err)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("remove success invalidates", func(t *testing.T) {
		hosts := &fakeRemoteNodeHostStore{host: remoteNodeMutationTestHost(), exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)

		err := app.RemoveHost(context.Background(), "host-1")

		require.NoError(t, err)
		assert.Equal(t, []string{"host-1"}, invalidator.hostIDs)
	})

	t.Run("remove persistence failure keeps tunnel", func(t *testing.T) {
		storeErr := errors.New("disk unavailable")
		hosts := &fakeRemoteNodeHostStore{host: remoteNodeMutationTestHost(), exists: true, removeErr: storeErr}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(hosts, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)

		err := app.RemoveHost(context.Background(), "host-1")

		require.ErrorIs(t, err, storeErr)
		assert.Empty(t, invalidator.hostIDs)
	})
}

func TestRemoteNodeMutationApplicationAgentPortChangeInvalidatesAfterPersistence(t *testing.T) {
	base := remoteNodeMutationTestAgent(57017)
	agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, apiassembler.NewHostAssembler(), invalidator)
	updated := remoteNodeMutationTestAgent(57018)

	_, err := app.UpsertAgent(context.Background(), updated)

	require.NoError(t, err)
	assert.Equal(t, []string{base.HostID}, invalidator.hostIDs)
}

func TestRemoteNodeMutationApplicationAgentRetryRecoversPendingInvalidationAudit(t *testing.T) {
	base := remoteNodeMutationTestAgent(57017)
	agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
	auditErr := errors.New("audit completion unavailable")
	invalidator := &recordingTunnelInvalidator{completeErr: auditErr}
	app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, apiassembler.NewHostAssembler(), invalidator)
	updated := remoteNodeMutationTestAgent(57018)

	first, err := app.UpsertAgent(context.Background(), updated)

	require.Error(t, err)
	assert.True(t, isTunnelInvalidationAuditError(err))
	assert.NotEmpty(t, first.PendingTunnelInvalidationRevision)
	assert.NotEmpty(t, agents.agent.PendingTunnelInvalidationRevision)

	invalidator.completeErr = nil
	second, err := app.UpsertAgent(context.Background(), updated)

	require.NoError(t, err)
	assert.Empty(t, second.PendingTunnelInvalidationRevision)
	assert.Empty(t, agents.agent.PendingTunnelInvalidationRevision)
	require.Len(t, invalidator.recoveries, 1)
	assert.Equal(t, first.PendingTunnelInvalidationRevision, invalidator.recoveries[0].ExpectedRevision)
}

func TestRemoteNodeMutationApplicationAgentNonTunnelAndFailureContract(t *testing.T) {
	t.Run("non-target config security and runtime changes do not invalidate", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, apiassembler.NewHostAssembler(), invalidator)
		updated := base
		updated.Config.ListenPort = 58000
		updated.Secret.Token = "rotated-token"
		updated.Security.TokenConfigured = true
		updated.Runtime = model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true, LocalPort: 57123}

		_, err := app.UpsertAgent(context.Background(), updated)

		require.NoError(t, err)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("persistence failure keeps tunnel", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		storeErr := errors.New("disk unavailable")
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true, upsertErr: storeErr}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, apiassembler.NewHostAssembler(), invalidator)

		_, err := app.UpsertAgent(context.Background(), remoteNodeMutationTestAgent(57018))

		require.ErrorIs(t, err, storeErr)
		assert.Empty(t, invalidator.hostIDs)
	})

	t.Run("remove tunnel agent invalidates", func(t *testing.T) {
		base := remoteNodeMutationTestAgent(57017)
		agents := &fakeRemoteNodeAgentStore{agent: base, exists: true}
		invalidator := &recordingTunnelInvalidator{}
		app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, agents, apiassembler.NewHostAssembler(), invalidator)

		err := app.RemoveAgent(context.Background(), base.HostID)

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
		DevMachineMode:        host.DevMachineMode,
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

// TestAgentRemovalRecoveryModeFailsClosedOnInvalidMode 验证恢复匹配模式非法取值时
// fail-closed：auditTrigger 返回错误，且 RecoverPendingAgentRemoval 在触碰 invalidator
// 与审计存储之前就拒绝执行，不产生任何恢复副作用。
func TestAgentRemovalRecoveryModeFailsClosedOnInvalidMode(t *testing.T) {
	cases := []struct {
		name      string
		mode      agentRemovalRecoveryMode
		want      string
		wantError bool
	}{
		{name: "uninstall only", mode: agentRemovalRecoveryUninstallOnly, want: tunnelInvalidationTriggerAgentRemoved},
		{name: "any origin", mode: agentRemovalRecoveryAnyOrigin, want: ""},
		{name: "invalid negative", mode: agentRemovalRecoveryMode(-1), wantError: true},
		{name: "invalid overflow", mode: agentRemovalRecoveryMode(255), wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.mode.auditTrigger()
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	invalidator := &recordingTunnelInvalidator{}
	app := newRemoteNodeMutationApplication(&fakeRemoteNodeHostStore{}, &fakeRemoteNodeAgentStore{}, apiassembler.NewHostAssembler(), invalidator)
	recovered, err := app.RecoverPendingAgentRemoval(context.Background(), "h1", agentRemovalRecoveryMode(255))
	require.Error(t, err)
	assert.False(t, recovered)
	assert.Empty(t, invalidator.recoveries, "非法模式不得触发 Recover 调用")
	assert.Empty(t, invalidator.invalidations, "非法模式不得触发 Apply 调用")
	assert.Empty(t, invalidator.hostIDs, "非法模式不得触碰 tunnel 状态查询")
}
