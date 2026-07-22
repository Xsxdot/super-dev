// runtime-validation 是 target-native strict validation bundle 的唯一执行入口。
//
// 职责：
//   - 解析 bundle/input/target 和仅来自 stdin 的一次性 credential
//   - 调用 runtimevalidation.RunStrictCampaign
//   - 稳定透传 0=PASS、1=FAIL、2=BLOCKED 退出码
//
// 边界：
//   - 不接受 credential 命令行参数或配置字段
//   - 不从 summary.md、旧 summary 或 package checksum 推断 PASS
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/runtimevalidation"
)

type campaignRunner func(context.Context, runtimevalidation.StrictCampaignOptions) (runtimevalidation.StrictCampaignResult, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, runtimevalidation.RunStrictCampaign))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, execute campaignRunner) int {
	log := logger.GetLogger().WithEntryName("RuntimeValidationCLI")
	log.Info("开始解析 runtime validation strict campaign 参数")
	flags := flag.NewFlagSet("runtime-validation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	bundleRoot := flags.String("bundle-root", "", "Absolute extracted bundle root")
	inputPath := flags.String("input", "", "Absolute runtime-input.json path")
	targetText := flags.String("target", "", "Bundle target as goos/goarch")
	credentialStdin := flags.Bool("credential-stdin", false, "Read one credential line from stdin")
	if err := flags.Parse(args); err != nil {
		log.WithErr(err).Error("runtime validation 参数解析失败")
		return 1
	}
	target, err := parseTarget(*targetText)
	if err != nil || *bundleRoot == "" || *inputPath == "" || !*credentialStdin {
		log.WithErr(err).Error("runtime validation 必填参数或 stdin credential mode 无效")
		return 1
	}
	credential, err := readCredentialLine(stdin)
	if err != nil {
		log.WithErr(err).Error("runtime validation 一次性 credential stdin 不可用")
		return 2
	}
	log.WithFields(map[string]any{"target": target.String(), "bundle_root": *bundleRoot, "input_path": *inputPath}).Info("开始执行 runtime validation strict campaign")
	result, err := execute(ctx, runtimevalidation.StrictCampaignOptions{
		BundleRoot: *bundleRoot, InputPath: *inputPath, Target: target, CredentialValue: credential,
	})
	if err != nil {
		log.WithErr(err).Error("runtime validation campaign infrastructure failure")
		return 1
	}
	log.WithFields(map[string]any{"status": result.Summary.Verdict.Status, "report_root": result.ReportRoot}).Info("runtime validation strict campaign 完成")
	_, _ = fmt.Fprintf(stdout, "runtime-validation: status=%s report=%s\n", result.Summary.Verdict.Status, result.ReportRoot)
	return runtimevalidation.ExitCodeForStatus(result.Summary.Verdict.Status)
}

func parseTarget(value string) (runtimevalidation.Target, error) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return runtimevalidation.Target{}, fmt.Errorf("target must use goos/goarch")
	}
	for _, target := range runtimevalidation.SupportedTargets() {
		if target.OS == parts[0] && target.Architecture == parts[1] {
			return target, nil
		}
	}
	return runtimevalidation.Target{}, fmt.Errorf("unsupported target")
}

func readCredentialLine(reader io.Reader) (string, error) {
	buffered := bufio.NewReader(io.LimitReader(reader, 8193))
	value, err := buffered.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	if value == "" || len(value) > 8192 {
		return "", fmt.Errorf("credential is empty or too long")
	}
	return value, nil
}
