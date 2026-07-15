// build-runtime-validation 为 targets.txt 中的五 target 生成 package_verified 便携包。
//
// 职责：
//   - 从 repo root 定位 Agent、validation assets 与 js-debug
//   - 要求调用方显式提供 target-native Playwright driver 根
//   - 调用共享 builder 并输出 manifest/archive digest
//
// 边界：
//   - 不下载资源，不启动 target bundle，不声明真机 PASS
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/runtimevalidation"
)

type bundleBuilder func(context.Context, runtimevalidation.BundleBuildOptions) ([]runtimevalidation.BundleBuildReceipt, error)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, runtimevalidation.BuildRuntimeValidationBundles))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, build bundleBuilder) int {
	log := logger.GetLogger().WithEntryName("BuildRuntimeValidationCLI")
	log.Info("开始解析 runtime validation bundle builder 参数")
	flags := flag.NewFlagSet("build-runtime-validation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repoRoot := flags.String("repo-root", "", "Absolute SuperDev repository root")
	outputRoot := flags.String("output", "", "Absolute bundle output root")
	playwrightRoot := flags.String("playwright-drivers", "", "Absolute root containing one target-native driver directory per target")
	goBinary := flags.String("go", "go", "Go compiler executable")
	if err := flags.Parse(args); err != nil {
		log.WithErr(err).Error("runtime validation bundle builder 参数解析失败")
		return 1
	}
	if !filepath.IsAbs(*repoRoot) || !filepath.IsAbs(*outputRoot) || !filepath.IsAbs(*playwrightRoot) {
		log.Error("runtime validation bundle builder 路径参数必须是绝对路径")
		return 1
	}
	targets, err := runtimevalidation.LoadTargetsFile(filepath.Join(*repoRoot, "validation", "runtime", "targets.txt"))
	if err != nil {
		log.WithErr(err).Error("加载 runtime validation target contract 失败")
		return 1
	}
	log.WithFields(map[string]any{"target_count": len(targets), "output_root": *outputRoot}).Info("开始构建 runtime validation package_verified bundles")
	receipts, err := build(ctx, runtimevalidation.BundleBuildOptions{
		AgentRoot: filepath.Join(*repoRoot, "agent"), RuntimeAssetsRoot: filepath.Join(*repoRoot, "validation", "runtime"),
		JSDebugRoot:           filepath.Join(*repoRoot, "desktop", "src-tauri", "resources", "js-debug"),
		PlaywrightDriversRoot: *playwrightRoot, OutputRoot: *outputRoot, GoBinary: *goBinary, Targets: targets,
	})
	if err != nil {
		log.WithErr(err).Error("runtime validation bundles 构建失败")
		return 1
	}
	for _, receipt := range receipts {
		_, _ = fmt.Fprintf(stdout, "package_verified target=%s manifest=%s archive=%s\n", receipt.Target.String(), receipt.Bundle.ManifestSHA256, receipt.ArchiveSHA256)
	}
	log.WithField("bundle_count", len(receipts)).Info("runtime validation package_verified bundles 构建完成")
	return 0
}
