// main_test.go 验证 runtime-validation CLI 参数、stdin secret 和稳定退出码。
//
// 职责：锁定 PASS=0、FAIL=1、BLOCKED=2，且 secret 不从 argv 进入。
// 边界：不启动真实 Agent/MCP，进程边界由 runtimevalidation 集成验收。
package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/runtimevalidation"
)

func TestCLIMapsStrictStatusesAndReadsCredentialFromStdin(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		status runtimevalidation.Status
		code   int
	}{{runtimevalidation.StatusPass, 0}, {runtimevalidation.StatusFail, 1}, {runtimevalidation.StatusBlocked, 2}} {
		t.Run(string(test.status), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{
				"--bundle-root", "/tmp/bundle with spaces", "--input", "/tmp/input.json", "--target", "darwin/arm64", "--credential-stdin",
			}, bytes.NewBufferString("one-time-secret\n"), &stdout, &stderr, func(_ context.Context, options runtimevalidation.StrictCampaignOptions) (runtimevalidation.StrictCampaignResult, error) {
				require.Equal(t, "one-time-secret", options.CredentialValue)
				return runtimevalidation.StrictCampaignResult{Summary: runtimevalidation.Summary{Verdict: runtimevalidation.Verdict{Status: test.status}}, ReportRoot: "/tmp/report"}, nil
			})
			require.Equal(t, test.code, code)
			require.NotContains(t, stdout.String(), "one-time-secret")
			require.NotContains(t, stderr.String(), "one-time-secret")
		})
	}
}

func TestCLIRejectsCredentialArgumentAndMissingStdinMode(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	code := run(context.Background(), []string{"--bundle-root", "/tmp/b", "--input", "/tmp/i", "--target", "darwin/arm64", "--credential", "secret"}, bytes.NewBuffer(nil), &output, &output, nil)
	require.Equal(t, 1, code)
	require.NotContains(t, output.String(), "secret")
}
