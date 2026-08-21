package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/operation"
)

type recordingApprovalStore struct {
	operation.ApprovalStore
	requested bool
	plan      operation.Plan
}

func (s *recordingApprovalStore) FindOrCreatePending(ctx context.Context, plan operation.Plan, requestedBy, requesterLabel string) (operation.Approval, error) {
	s.requested = true
	s.plan = plan
	return s.ApprovalStore.FindOrCreatePending(ctx, plan, requestedBy, requesterLabel)
}

func newTestGate(t *testing.T, enabled bool) (dbprovision.ApprovalGate, *recordingApprovalStore) {
	t.Helper()
	root := t.TempDir()
	settings := config.DefaultAgentSettings()
	settings.Approval.TestDatabaseTerminateConns = enabled
	settingsStore := config.NewSettingsStore(root)
	require.NoError(t, settingsStore.Save(settings))
	recorder := &recordingApprovalStore{
		ApprovalStore: operation.NewApprovalFileStore(filepath.Join(root, "approvals.json")),
	}
	return NewProvisionApprovalGate(settingsStore, recorder), recorder
}

func TestGateSkipsApprovalWhenNoSideEffects(t *testing.T) {
	gate, rec := newTestGate(t, true)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{Kind: dbprovision.KindPostgres}})
	require.NoError(t, err)
	require.False(t, rec.requested)
}

func TestGateSkipsApprovalWhenPolicyDisabled(t *testing.T) {
	gate, rec := newTestGate(t, false)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{
		Kind:        dbprovision.KindPostgres,
		SideEffects: []dbprovision.SideEffect{{Kind: dbprovision.SideEffectTerminateConnections, Target: "tk_dev", Count: 3}},
	}})
	require.NoError(t, err)
	require.False(t, rec.requested)
}

func TestGateRequiresApprovalWhenSideEffectsAndPolicyEnabled(t *testing.T) {
	gate, rec := newTestGate(t, true)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{
		Kind:        dbprovision.KindPostgres,
		SideEffects: []dbprovision.SideEffect{{Kind: dbprovision.SideEffectTerminateConnections, Target: "tk_dev", Count: 3}},
	}})
	var required ApprovalRequiredError
	require.True(t, errors.As(err, &required))
	require.True(t, rec.requested)
	require.Equal(t, operation.OperationTestDatabaseTerminate, rec.plan.Kind)
	// approval.Plan 保留稳定的 kind/target/count，且不含 DSN 或密码字段。
	require.Equal(t, operation.OperationTestDatabaseTerminate, required.Approval.Plan.Kind)
	require.Equal(t, "proj-1", required.Approval.Plan.Target.ProjectID)
	require.True(t, strings.Contains(required.Approval.Plan.TargetSummary, "tk_dev"))
}
