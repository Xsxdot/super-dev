// launchd.go 提供 macOS launchd 生命周期控制能力。
//
// 职责：
//   - 将 Deployment.Runtime 中的 launchd 配置转换为 launchctl 命令
//   - 执行 bootstrap / kickstart / bootout，并向 Manager 返回明确错误
//
// 边界：
//   - 不解析 plist 内容，不生成 LaunchAgent 文件
//   - 不采集日志；macOS 日志由 logs 配置和日志 backend 处理
package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/hostpaths"
	"github.com/xsxdot/super-dev/agent/model"
)

type launchdCommand struct {
	name string
	args []string
}

func launchdDomain(uid int) string {
	return fmt.Sprintf("gui/%d", uid)
}

func launchdTarget(uid int, label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", errors.New("launchd label 不能为空")
	}
	return launchdDomain(uid) + "/" + label, nil
}

func launchdRuntime(dep model.Deployment) (*model.RuntimeConfig, error) {
	if dep.Runtime == nil || dep.Runtime.Type != model.RuntimeTypeLaunchd {
		return nil, errors.New("deployment runtime 不是 launchd")
	}
	if strings.TrimSpace(dep.Runtime.Label) == "" {
		return nil, errors.New("launchd label 不能为空")
	}
	return dep.Runtime, nil
}

func launchdStartCommands(uid int, dep model.Deployment) ([]launchdCommand, launchdCommand, error) {
	runtimeConfig, err := launchdRuntime(dep)
	if err != nil {
		return nil, launchdCommand{}, err
	}
	target, err := launchdTarget(uid, runtimeConfig.Label)
	if err != nil {
		return nil, launchdCommand{}, err
	}

	var bootstrap []launchdCommand
	if plistPath := strings.TrimSpace(runtimeConfig.PlistPath); plistPath != "" {
		bootstrap = append(bootstrap, launchdCommand{
			name: "launchctl",
			args: []string{"bootstrap", launchdDomain(uid), expandHomePath(plistPath)},
		})
	}

	return bootstrap, launchdCommand{
		name: "launchctl",
		args: []string{"kickstart", "-k", target},
	}, nil
}

func launchdStopCommand(uid int, dep model.Deployment) (launchdCommand, error) {
	runtimeConfig, err := launchdRuntime(dep)
	if err != nil {
		return launchdCommand{}, err
	}
	target, err := launchdTarget(uid, runtimeConfig.Label)
	if err != nil {
		return launchdCommand{}, err
	}
	return launchdCommand{name: "launchctl", args: []string{"bootout", target}}, nil
}

func runLaunchdStart(ctx context.Context, uid int, dep model.Deployment) error {
	if runtime.GOOS != "darwin" {
		return errors.New("launchd 仅支持 macOS")
	}
	bootstrap, kickstart, err := launchdStartCommands(uid, dep)
	if err != nil {
		return err
	}
	for _, cmd := range bootstrap {
		if err := runLaunchdCommand(ctx, cmd); err != nil && !isAlreadyBootstrapped(err) {
			return err
		}
	}
	return runLaunchdCommand(ctx, kickstart)
}

func runLaunchdStop(ctx context.Context, uid int, dep model.Deployment) error {
	if runtime.GOOS != "darwin" {
		return errors.New("launchd 仅支持 macOS")
	}
	cmd, err := launchdStopCommand(uid, dep)
	if err != nil {
		return err
	}
	return runLaunchdCommand(ctx, cmd)
}

func runLaunchdCommand(parent context.Context, cmd launchdCommand) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd.name, cmd.args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s 失败: %s: %w", cmd.name, strings.Join(cmd.args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

func macOSLogStreamCommand(target string) string {
	escaped := strings.ReplaceAll(target, `"`, `\"`)
	predicate := fmt.Sprintf(`subsystem == "%s" OR process == "%s" OR eventMessage CONTAINS[c] "%s"`, escaped, escaped, escaped)
	return "log stream --style compact --predicate " + shellSingleQuote(predicate)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func isAlreadyBootstrapped(err error) bool {
	message := err.Error()
	return strings.Contains(message, "Bootstrap failed: 5") ||
		strings.Contains(message, "Service is already loaded") ||
		strings.Contains(message, "service already loaded")
}

func expandHomePath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := hostpaths.UserHome()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
