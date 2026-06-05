// Package pipeline_test 验证流水线静态校验不会执行插件。
//
// 职责：
//   - 验证 Engine.ValidatePlan 捕获插件、run_if、concurrency 和 target 形态错误
//
// 边界：
//   - 不执行真实命令
//   - 不访问远程主机或文件系统
package pipeline_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

type validatingPlugin struct {
	name        string
	validateErr error
}

func (p validatingPlugin) Name() string { return p.name }

func (p validatingPlugin) Validate(model.Step) error { return p.validateErr }

func (p validatingPlugin) Execute(*pipeline.RunContext, model.Step, []pipeline.Target) error {
	return errors.New("ValidatePlan must not execute plugins")
}

type targetOnlyPlugin struct{}

func (p targetOnlyPlugin) Name() string { return "target_only" }

func (p targetOnlyPlugin) Validate(model.Step) error { return nil }

func (p targetOnlyPlugin) ValidateTargets(_ model.Step, targets []pipeline.Target) error {
	if len(targets) == 0 {
		return errors.New("target_only requires targets")
	}
	return nil
}

func (p targetOnlyPlugin) Execute(*pipeline.RunContext, model.Step, []pipeline.Target) error {
	return errors.New("ValidatePlan must not execute plugins")
}

func TestValidatePlanRejectsUnknownPlugin(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Build: []model.Step{{Name: "Mystery", Type: "missing_plugin"}},
	}, nil)
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	err = engine.ValidatePlan(plan, run)

	require.ErrorContains(t, err, `build phase step "Mystery": plugin "missing_plugin" not registered`)
}

func TestValidatePlanRunsPluginValidateWithoutExecuting(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Build: []model.Step{{Name: "Build", Type: "needs_cmd"}},
	}, nil)
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	engine.Register(validatingPlugin{name: "needs_cmd", validateErr: errors.New("needs_cmd requires with.cmd")})
	err = engine.ValidatePlan(plan, run)

	require.ErrorContains(t, err, `build phase step "Build": needs_cmd requires with.cmd`)
}

func TestValidatePlanRejectsInvalidRunIf(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Build: []model.Step{{Name: "Build", Type: "noop", RunIf: "prod"}},
	}, nil)
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	engine.Register(validatingPlugin{name: "noop"})
	err = engine.ValidatePlan(plan, run)

	require.ErrorContains(t, err, `build phase step "Build": run_if expression "prod" is invalid`)
}

func TestValidatePlanRejectsInvalidConcurrency(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Build: []model.Step{{Name: "Build", Type: "noop", Concurrency: "batch:0"}},
	}, nil)
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	engine.Register(validatingPlugin{name: "noop"})
	err = engine.ValidatePlan(plan, run)

	require.ErrorContains(t, err, `build phase step "Build": invalid concurrency "batch:0"`)
}

func TestValidatePlanRejectsTargetValidatorWithoutTargets(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Deploy: []model.Step{{Name: "Remote", Type: "target_only"}},
	}, nil)
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	engine.Register(targetOnlyPlugin{})
	err = engine.ValidatePlan(plan, run)

	require.ErrorContains(t, err, `deploy phase step "Remote": target_only requires targets`)
}

func TestValidatePlanAcceptsTargetValidatorWithTargets(t *testing.T) {
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Roles:  map[string][]string{"compute": {"h1"}},
		Deploy: []model.Step{{Name: "Remote", Type: "target_only", Roles: []string{"compute"}}},
	}, []model.HostRef{{ID: "h1", Name: "host-1"}})
	require.NoError(t, err)

	engine := pipeline.NewEngine()
	engine.Register(targetOnlyPlugin{})
	err = engine.ValidatePlan(plan, run)

	require.NoError(t, err)
}
