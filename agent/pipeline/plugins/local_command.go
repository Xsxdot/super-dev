// Package plugins implements built-in pipeline step plugins.
//
// 职责：
//   - local_command / remote_command / transfer / http_check / archive 等原子插件
//   - 校验各插件 with 参数
//
// 边界：
//   - 不调度 DAG，不持久化 Run 状态
//   - 不解析模板 include
package plugins

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

// LocalCommand runs a shell command on the agent host.
type LocalCommand struct{}

// NewLocalCommand creates LocalCommand.
//
// 返回：
//   - local_command 插件实例
func NewLocalCommand() *LocalCommand { return &LocalCommand{} }

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `local_command`
func (p *LocalCommand) Name() string { return "local_command" }

// Validate checks local_command step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - roles 非空或 with.cmd 缺失时返回错误
func (p *LocalCommand) Validate(step model.Step) error {
	if len(step.Roles) > 0 {
		return errors.New("local_command does not accept roles")
	}
	if withString(step.With, "cmd", "command") == "" {
		return errors.New("local_command requires with.cmd")
	}
	return nil
}

// Execute runs the configured local shell command.
//
// 参数：
//   - ctx: 插件运行上下文
//   - step: local_command 步骤
//   - targets: 本插件忽略 targets，必须通过 Validate 保证 roles 为空
//
// 返回：
//   - 命令启动、执行或退出失败时返回错误
func (p *LocalCommand) Execute(ctx *pipeline.RunContext, step model.Step, _ []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	cmdText := withString(step.With, "cmd", "command")
	workDir := withString(step.With, "workDir", "work_dir", "workdir")
	cmd := exec.CommandContext(ctx.Context, "sh", "-c", cmdText)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	if ctx.RunTempDir != "" {
		cmd.Env = append(cmd.Env, "RUN_TEMP_DIR="+ctx.RunTempDir)
	}
	cmd.Env = appendReservedEnv(cmd.Env, ctx.Vars)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	scan := func(stream string, scanner *bufio.Scanner) {
		for scanner.Scan() {
			ctx.LogLine(scanner.Text(), stream)
		}
		done <- struct{}{}
	}
	go scan("stdout", bufio.NewScanner(stdout))
	go scan("stderr", bufio.NewScanner(stderr))
	<-done
	<-done
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("local command failed: %w", err)
	}
	return nil
}

func appendReservedEnv(env []string, vars map[string]string) []string {
	for _, item := range []struct {
		key  string
		name string
	}{
		{key: "workspace", name: "WORKSPACE"},
		{key: "output", name: "OUTPUT"},
		{key: "artifacts", name: "ARTIFACTS"},
		{key: "version", name: "VERSION"},
		{key: "env", name: "ENV"},
		{key: "date", name: "DATE"},
		{key: "time", name: "TIME"},
		{key: "run_temp_dir", name: "RUN_TEMP_DIR"},
	} {
		if value := vars[item.key]; value != "" {
			env = append(env, item.name+"="+value)
		}
	}
	return env
}

func withString(values map[string]interface{}, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok {
			return s
		}
		return fmt.Sprint(raw)
	}
	return ""
}
