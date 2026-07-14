// Package main 提供 Windows 10 x64 一次性真实验证驱动入口。
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

func main() {
	options := parseOptions()
	log := logger.GetLogger().WithEntryName("WindowsValidationCLI")
	if options.FinalizeCleanup {
		if _, err := windowsvalidation.FinalizeCampaignCleanup(options.Run.ResultsRoot, options.CampaignID, options.CleanupReport, options.PreparedBackup); err != nil {
			log.WithErr(err).WithField("campaign_id", options.CampaignID).Error("Windows cleanup 最终报告合并失败")
			os.Exit(1)
		}
		return
	}
	if options.MaterializePreDriverFailure {
		if _, err := windowsvalidation.MaterializePreDriverFailure(options.Run.PackageRoot, options.Run.ResultsRoot, options.PreparedBackup, options.CampaignID); err != nil {
			log.WithErr(err).WithField("campaign_id", options.CampaignID).Error("Windows pre-driver 失败报告生成失败")
			os.Exit(1)
		}
		return
	}
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

type cliOptions struct {
	Run                         windowsvalidation.RunOptions
	FinalizeCleanup             bool
	MaterializePreDriverFailure bool
	CampaignID                  string
	CleanupReport               string
	PreparedBackup              string
}

func parseOptions() cliOptions {
	executable, _ := os.Executable()
	defaultRoot := filepath.Clean(filepath.Join(filepath.Dir(executable), ".."))
	var options cliOptions
	flag.StringVar(&options.Run.PackageRoot, "package-root", defaultRoot, "extracted portable package root")
	flag.StringVar(&options.Run.InputPath, "input", filepath.Join(filepath.Dir(defaultRoot), "runtime-input.json"), "Windows machine runtime input JSON outside the immutable package")
	flag.StringVar(&options.Run.MCPPath, "mcp-path", "", "explicit installed superdev-mcp.exe override")
	flag.StringVar(&options.Run.AgentURL, "agent-url", "http://127.0.0.1:57017", "installed Desktop Agent URL")
	flag.StringVar(&options.Run.ResultsRoot, "results-root", "", "explicit results root override")
	flag.StringVar(&options.Run.InstallerDir, "installer-dir", "", "explicit frozen installer input directory")
	flag.BoolVar(&options.FinalizeCleanup, "finalize-cleanup", false, "merge the fixed cleanup report into campaign and aggregate reports")
	flag.BoolVar(&options.MaterializePreDriverFailure, "materialize-pre-driver-failure", false, "derive a fixed campaign report when the normal driver was not reached")
	flag.StringVar(&options.CampaignID, "campaign-id", "", "campaign identity used by cleanup finalization or pre-driver failure materialization")
	flag.StringVar(&options.CleanupReport, "cleanup-report", "", "campaign cleanup-report.json used only by --finalize-cleanup")
	flag.StringVar(&options.PreparedBackup, "prepared-backup", "", "prepared backup used by cleanup finalization or pre-driver failure materialization")
	flag.Parse()
	return options
}
