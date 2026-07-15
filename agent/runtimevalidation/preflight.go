// preflight.go 在 active marker 之前完成所有外部依赖与只读拓扑检查。
//
// 职责：
//   - 验证七语言工具链、系统 adapter、浏览器、js-debug 与 target-native Playwright driver
//   - 验证调试/审批策略、loopback 端口能力和 borrowed Host/Agent 身份
//   - 把外部准备缺失归类为 BLOCKED，把已签名 bundle 内合同错误归类为 FAIL
//
// 边界：
//   - 不创建 campaign、active marker、clone、项目、服务或远端资源
//   - 不下载/安装依赖，不连接远端 Agent，也不读取任何凭据明文
package runtimevalidation

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const dedicatedRemoteHostTag = "superdev-validation-dedicated-resettable"

// RunReadOnlyPreflight 检查 strict campaign 在产生 active marker 前需要的全部外部条件。
//
// 参数：
//   - ctx: 允许取消七语言版本检查
//   - bundleRoot: 已通过 VerifyBundle 的当前 target 解压目录
//   - input: 已通过严格 schema 校验的非敏感输入
//   - target: 已与 native host 精确匹配的目标
//   - commands: 只执行 fixture 声明的版本命令；nil 时使用真实 OS executor
//
// 返回：
//   - 所有条件满足时 PASS
//   - 缺机器依赖或 foundation 准备不全时 BLOCKED
//   - bundle 内 fixture/scenario 合同无效时 FAIL
func RunReadOnlyPreflight(ctx context.Context, bundleRoot string, input RuntimeInput, target Target, commands CommandExecutor) CheckResult {
	log := logger.GetLogger().WithEntryName("RuntimeValidationPreflight").WithFields(map[string]any{
		"target": target.String(), "profile_id": input.ProfileID, "remote_host_id": input.RemoteHostID,
	})
	log.Info("开始 active marker 前的只读依赖预检")
	failed := func(status Status, code, message, source string) CheckResult {
		log.WithFields(map[string]any{"status": status, "cause_code": code, "source": source}).Error("runtime validation 只读依赖预检未通过")
		return CheckResult{ID: "read-only-preflight", Status: status, Cause: Cause{Code: code, Message: message, Source: source}}
	}

	assetRoot := filepath.Join(bundleRoot, "validation")
	fixtures, err := LoadFixtures(filepath.Join(assetRoot, "fixtures"))
	if err != nil {
		return failed(StatusFail, "fixture_assets_invalid", err.Error(), "bundle-fixtures")
	}
	if _, err := LoadScenarios(filepath.Join(assetRoot, "scenarios")); err != nil {
		return failed(StatusFail, "scenario_assets_invalid", err.Error(), "bundle-scenarios")
	}
	if commands == nil {
		commands = NewOSCommandExecutor(io.Discard)
	}
	for _, fixture := range fixtures {
		platform := fixture.Platforms[target.OS]
		if err := commands.Run(ctx, CommandRunRequest{
			Name: fixture.Provider + "-read-only-preflight", Command: platform.Preflight,
			Directory: filepath.Join(assetRoot, "fixtures", fixture.Provider),
		}); err != nil {
			return failed(StatusBlocked, "runtime_toolchain_unavailable", fmt.Sprintf("%s: %v", fixture.Provider, err), "toolchain-"+fixture.Provider)
		}
	}

	adapterPaths := cloneStringMap(input.Adapters)
	adapterPaths["resources/js-debug"] = filepath.Join(bundleRoot, "resources", "js-debug", "src", "dapDebugServer.js")
	for _, fixture := range fixtures {
		path := adapterPaths[fixture.Debug.AdapterResource]
		if err := validateNativeAdapter(path, target.OS, fixture.Debug.AdapterResource == "resources/js-debug"); err != nil {
			return failed(StatusBlocked, "debug_adapter_unavailable", fmt.Sprintf("%s: %v", fixture.Provider, err), "adapter-"+fixture.Provider)
		}
	}
	if err := validatePlaywrightDriver(filepath.Join(bundleRoot, "resources", "playwright-driver"), target); err != nil {
		return failed(StatusBlocked, "playwright_driver_unavailable", err.Error(), "playwright-driver")
	}
	if err := validateFoundationBrowserAndApproval(input.FoundationPath, target.OS); err != nil {
		return failed(StatusBlocked, "browser_or_debug_policy_unavailable", err.Error(), "foundation-settings")
	}
	if err := validateBorrowedTopology(input.FoundationPath, input.RemoteHostID); err != nil {
		return failed(StatusBlocked, "borrowed_remote_identity_unavailable", err.Error(), "foundation-topology")
	}
	if err := probeLoopbackPorts(len(fixtures) + 3); err != nil {
		return failed(StatusBlocked, "loopback_ports_unavailable", err.Error(), "loopback-ports")
	}
	log.WithFields(map[string]any{"provider_count": len(fixtures), "reserved_port_probe_count": len(fixtures) + 3}).Info("active marker 前的只读依赖预检通过")
	return CheckResult{ID: "read-only-preflight", Status: StatusPass}
}

func validateNativeAdapter(path, targetOS string, script bool) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("absolute adapter path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("adapter must be a regular non-symlink file")
	}
	// js-debug 由 Node 解释执行；其余 adapter/launcher 必须可直接启动。
	if targetOS != "windows" && !script && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("adapter is not executable")
	}
	return nil
}

func validatePlaywrightDriver(root string, target Target) error {
	nodeName := "node"
	if target.OS == "windows" {
		nodeName = "node.exe"
	}
	// playwright-go 直接执行 driver 根目录下的 Node，并把 package/cli.js 作为入口；
	// 把 node 误判为目录会让通过打包检查的 bundle 在真实启动时必然失败。
	for _, candidate := range []struct {
		name       string
		path       string
		executable bool
	}{
		{name: nodeName, path: filepath.Join(root, nodeName), executable: target.OS != "windows"},
		{name: "package/cli.js", path: filepath.Join(root, "package", "cli.js")},
	} {
		info, err := os.Lstat(candidate.path)
		if err != nil {
			return fmt.Errorf("inspect Playwright %s: %w", candidate.name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Playwright %s must be a regular non-symlink file", candidate.name)
		}
		if candidate.executable && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("Playwright %s is not executable", candidate.name)
		}
	}
	return nil
}

func validateFoundationBrowserAndApproval(foundationRoot, targetOS string) error {
	var settings FoundationRuntimeSettings
	if err := readJSONFile(filepath.Join(foundationRoot, "settings.json"), &settings); err != nil {
		return err
	}
	approval := settings.Approval
	if !approval.ConfigUpsert || !approval.PipelineUpsert || !approval.PipelineRun || !approval.TemplateImport ||
		!approval.BrowserDebugOpen || !approval.CodeDebugOpen || !approval.CodeDebugEvaluate {
		return fmt.Errorf("all runtime validation mutation approval policies must remain enabled")
	}
	// grace_minutes 是产品必填范围；validation actor 从不申请 grace，所有 mutation 仍逐次匹配真人批准 identity。
	if approval.GraceMinutes < 1 || approval.GraceMinutes > 120 {
		return fmt.Errorf("approval grace_minutes must remain within the product-valid range 1..120")
	}
	defaultID := strings.TrimSpace(settings.DebugBrowser.DefaultBrowserID)
	if defaultID == "" || settings.DebugBrowser.ProfileMode != "ephemeral" || !settings.DebugBrowser.AllowEvaluate {
		return fmt.Errorf("default ephemeral browser with allow_evaluate=true is required")
	}
	for _, browser := range settings.DebugBrowser.Browsers {
		if browser.ID != defaultID {
			continue
		}
		if err := validateNativeAdapter(browser.ExecutablePath, targetOS, false); err != nil {
			return fmt.Errorf("default browser %s: %w", defaultID, err)
		}
		return nil
	}
	return fmt.Errorf("default browser %s is not declared", defaultID)
}

func validateBorrowedTopology(foundationRoot, remoteHostID string) error {
	var hosts []map[string]any
	if err := readJSONFile(filepath.Join(foundationRoot, "hosts.json"), &hosts); err != nil {
		return err
	}
	foundHost := false
	for _, host := range hosts {
		if strings.TrimSpace(fmt.Sprint(host["id"])) != remoteHostID {
			continue
		}
		if isSelf, ok := host["is_self"].(bool); ok && isSelf {
			return fmt.Errorf("borrowed host is marked self")
		}
		if !stringListContains(host["tags"], dedicatedRemoteHostTag) {
			return fmt.Errorf("borrowed host is missing governance tag %s", dedicatedRemoteHostTag)
		}
		foundHost = true
		break
	}
	if !foundHost {
		return fmt.Errorf("borrowed host %s is absent", remoteHostID)
	}
	var agents []map[string]any
	if err := readJSONFile(filepath.Join(foundationRoot, "agents.json"), &agents); err != nil {
		return err
	}
	for _, agent := range agents {
		if strings.TrimSpace(fmt.Sprint(agent["host_id"])) != remoteHostID {
			continue
		}
		transport := RawMessageMap(agent["transport"])
		chain, _ := transport["chain"].([]any)
		if len(chain) == 0 {
			return fmt.Errorf("borrowed agent %s has no transport chain", remoteHostID)
		}
		return nil
	}
	return fmt.Errorf("borrowed agent for host %s is absent", remoteHostID)
}

func stringListContains(raw any, wanted string) bool {
	items, ok := raw.([]any)
	if !ok {
		if typed, typedOK := raw.([]string); typedOK {
			for _, item := range typed {
				if item == wanted {
					return true
				}
			}
		}
		return false
	}
	for _, item := range items {
		if fmt.Sprint(item) == wanted {
			return true
		}
	}
	return false
}

func probeLoopbackPorts(count int) error {
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for index := 0; index < count; index++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("allocate loopback port %d/%d: %w", index+1, count, err)
		}
		listeners = append(listeners, listener)
	}
	return nil
}
