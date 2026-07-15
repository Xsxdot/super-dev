// Package main 提供 Windows 10 22H2 x64 (build 19045) 一次性真实验证驱动入口。
//
// 职责：
//   - 解析便携包与机器输入路径
//   - 调用固定 campaign 驱动并以进程退出码暴露最终状态
//
// 边界：
//   - 不接受任意步骤、脚本或工具名
//   - 不在非 Windows x64 主机执行功能验证
package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/windowsvalidation"
)

const debugCredentialEnvName = "SUPERDEV_WINDOWS_VALIDATION_DEBUG_CREDENTIAL"

func main() {
	options := parseOptions()
	log := logger.GetLogger().WithEntryName("WindowsValidationCLI")
	if options.CollectEnvironmentPreinstall {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_, err := windowsvalidation.CollectPreparedEnvironmentPreinstall(ctx, windowsvalidation.PreparedEnvironmentPreinstallOptions{
			PackageRoot: options.Run.PackageRoot, RuntimeInputPath: options.Run.InputPath,
			PreparedBackup: options.PreparedBackup, CampaignID: options.CampaignID, Lane: options.PreinstallLane,
		})
		if err != nil {
			log.WithFields(map[string]any{"campaign_id": safeCampaignID(options.CampaignID), "lane": options.PreinstallLane}).Error("Windows 安装前环境门禁失败")
			os.Exit(1)
		}
		return
	}
	if options.ExecuteInstallerLifecycle {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_, err := windowsvalidation.ExecuteInstallerLifecycleAction(ctx, windowsvalidation.InstallerLifecycleExecuteOptions{
			PackageRoot: options.Run.PackageRoot, PreparedBackup: options.PreparedBackup,
			InstallerPath: options.InstallerPath, InstallDirectory: options.InstallDirectory,
			ResultsRoot: options.Run.ResultsRoot, Action: windowsvalidation.InstallerLifecycleAction(options.LifecycleAction),
		})
		if err != nil {
			log.WithField("action", safeLifecycleAction(options.LifecycleAction)).Error("Windows installer lifecycle 固定动作失败")
			os.Exit(1)
		}
		return
	}
	if options.FinalizeInstallerLifecycle {
		if _, err := windowsvalidation.FinalizeCampaignInstallerLifecycle(options.Run.PackageRoot, options.Run.ResultsRoot, options.CampaignID, options.PreparedBackup); err != nil {
			log.WithField("campaign_id", safeCampaignID(options.CampaignID)).Error("Windows installer lifecycle 最终报告合并失败")
			os.Exit(1)
		}
		return
	}
	if options.FinalizeCleanup {
		if _, err := windowsvalidation.FinalizeCampaignCleanup(options.Run.ResultsRoot, options.CampaignID, options.CleanupReport, options.PreparedBackup); err != nil {
			log.WithErr(err).WithField("campaign_id", safeCampaignID(options.CampaignID)).Error("Windows cleanup 最终报告合并失败")
			os.Exit(1)
		}
		return
	}
	if options.MaterializePreDriverFailure {
		if _, err := windowsvalidation.MaterializePreDriverFailure(options.Run.PackageRoot, options.Run.ResultsRoot, options.PreparedBackup, options.CampaignID); err != nil {
			log.WithErr(err).WithField("campaign_id", safeCampaignID(options.CampaignID)).Error("Windows pre-driver 失败报告生成失败")
			os.Exit(1)
		}
		return
	}
	options.Run.DebugCredentialValue = consumeDebugCredentialEnvironment()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	log.WithFields(map[string]any{"package_root": options.Run.PackageRoot, "input": options.Run.InputPath}).Info("Windows 真实验证入口开始")
	report, err := windowsvalidation.RunCampaign(ctx, options.Run)
	if err != nil {
		log.WithErr(err).Error("Windows 真实验证入口失败")
		os.Exit(1)
	}
	log.WithFields(map[string]any{"campaign_id": report.CampaignID, "lane": report.Lane, "phase_status": report.Result.PhaseStatus, "attempted": report.Result.Attempted, "tool_rows": len(report.ToolRows)}).Info("Windows 真实验证入口完成")
	// cleanup 在外层 PowerShell 卸载/恢复之后才能完成；进程退出码只表达本 lane 的功能状态。
	if report.Functional.PhaseStatus != windowsvalidation.PhaseStatusPass {
		os.Exit(2)
	}
}

func safeLifecycleAction(action string) string {
	switch action {
	case "install", "start", "stop", "uninstall":
		return action
	default:
		return ""
	}
}

func safeCampaignID(campaignID string) string {
	if len(campaignID) == 0 || len(campaignID) > 128 {
		return ""
	}
	for _, character := range campaignID {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' {
			return ""
		}
	}
	return campaignID
}

type cliOptions struct {
	Run                          windowsvalidation.RunOptions
	CollectEnvironmentPreinstall bool
	ExecuteInstallerLifecycle    bool
	FinalizeInstallerLifecycle   bool
	FinalizeCleanup              bool
	MaterializePreDriverFailure  bool
	CampaignID                   string
	LifecycleAction              string
	InstallerPath                string
	InstallDirectory             string
	CleanupReport                string
	PreparedBackup               string
	PreinstallLane               string
}

func parseOptions() cliOptions {
	executable, _ := os.Executable()
	defaultRoot := filepath.Clean(filepath.Join(filepath.Dir(executable), ".."))
	var options cliOptions
	flag.StringVar(&options.Run.PackageRoot, "package-root", defaultRoot, "extracted portable package root")
	flag.StringVar(&options.Run.InputPath, "input", filepath.Join(filepath.Dir(defaultRoot), "runtime-input.json"), "Windows machine runtime input JSON outside the immutable package")
	flag.StringVar(&options.Run.MCPPath, "mcp-path", "", "explicit installed superdev-mcp.exe override")
	flag.StringVar(&options.Run.ResultsRoot, "results-root", "", "explicit results root override")
	flag.StringVar(&options.Run.InstallerDir, "installer-dir", "", "explicit frozen installer input directory")
	flag.BoolVar(&options.CollectEnvironmentPreinstall, "collect-environment-preinstall", false, "collect and persist the read-only pre-install environment gate")
	flag.BoolVar(&options.ExecuteInstallerLifecycle, "execute-installer-lifecycle", false, "execute and record one fixed installer lifecycle action")
	flag.BoolVar(&options.FinalizeInstallerLifecycle, "finalize-installer-lifecycle", false, "merge prepared installer lifecycle facts into campaign and aggregate reports")
	flag.BoolVar(&options.FinalizeCleanup, "finalize-cleanup", false, "merge the fixed cleanup report into campaign and aggregate reports")
	flag.BoolVar(&options.MaterializePreDriverFailure, "materialize-pre-driver-failure", false, "derive a fixed campaign report when the normal driver was not reached")
	flag.StringVar(&options.CampaignID, "campaign-id", "", "campaign identity used by cleanup finalization or pre-driver failure materialization")
	flag.StringVar(&options.LifecycleAction, "lifecycle-action", "", "fixed install, start, stop, or uninstall action")
	flag.StringVar(&options.InstallerPath, "installer-path", "", "frozen installer selected for the prepared lane")
	flag.StringVar(&options.InstallDirectory, "install-directory", "", "canonical product install root bound across lifecycle actions")
	flag.StringVar(&options.CleanupReport, "cleanup-report", "", "campaign cleanup-report.json used only by --finalize-cleanup")
	flag.StringVar(&options.PreparedBackup, "prepared-backup", "", "prepared backup used by cleanup finalization or pre-driver failure materialization")
	flag.StringVar(&options.PreinstallLane, "preinstall-lane", "", "prepared msi_smoke, nsis_core, or core_only lane used only by pre-install collection")
	flag.Parse()
	options.Run.PreparedBackup = options.PreparedBackup
	return options
}

func consumeDebugCredentialEnvironment() string {
	value := os.Getenv(debugCredentialEnvName)
	// 环境只用作 PowerShell 父进程到 driver 的一次性内存交接；读取后立即删除，避免后续子进程继承。
	_ = os.Unsetenv(debugCredentialEnvName)
	return value
}
