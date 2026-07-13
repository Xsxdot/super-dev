// Package main 构建可从 macOS 复制到 Windows 的确定性验证归档。
//
// 职责：
//   - 解析仓库、包源、Agent 与输出目录
//   - 执行固定资产门禁、Windows x64 交叉编译和确定性 ZIP 构建
//
// 边界：
//   - 只输出 package_verified，不产生任何 Windows 工具 verdict
//   - 不下载、嵌入或修改安装器
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
	options := parseBuildOptions()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	log := logger.GetLogger().WithEntryName("WindowsValidationBuildCLI")
	log.WithFields(map[string]any{"source_root": options.SourceRoot, "output_dir": options.OutputDir}).Info("开始构建 Windows 可复制验证包")
	verification, err := windowsvalidation.BuildPortableArchive(ctx, options)
	if err != nil {
		log.WithErr(err).Error("Windows 可复制验证包构建失败")
		os.Exit(1)
	}
	log.WithFields(map[string]any{"status": verification.Status, "archive": verification.Archive.Path, "sha256": verification.Archive.SHA256}).Info("Windows 可复制验证包构建完成")
}

func parseBuildOptions() windowsvalidation.BuildOptions {
	workingDirectory, _ := os.Getwd()
	defaultAgent := workingDirectory
	defaultRepository := filepath.Dir(defaultAgent)
	var options windowsvalidation.BuildOptions
	flag.StringVar(&options.RepositoryRoot, "repository-root", defaultRepository, "super-debug repository root")
	flag.StringVar(&options.SourceRoot, "source-root", filepath.Join(defaultRepository, "validation", "windows-real"), "portable validation source root")
	flag.StringVar(&options.AgentRoot, "agent-root", defaultAgent, "Go Agent module root")
	flag.StringVar(&options.OutputDir, "output-dir", filepath.Join(defaultRepository, "dist", "windows-validation"), "package output directory")
	flag.Parse()
	return options
}
