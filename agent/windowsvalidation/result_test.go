// result_test.go 验证 Windows 真机结果只能由执行事实与证据义务派生。
//
// 职责：
//   - 锁定 NOT_RUN、BLOCKED、PASS 与 FAIL 的互斥语义
//   - 锁定安装包文件身份与安装器生命周期互不替代
//   - 锁定缺失证据、写证据失败和混合子状态的聚合行为
//
// 边界：
//   - 不执行 Windows 进程、安装器或 MCP 调用
//   - 不测试报告排版或具体 scenario 内容
package windowsvalidation

import "testing"

const resultTestTime = "2026-07-14T10:00:00Z"

func TestDeriveValidationResultUsesFactsAndEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input ResultInput
		want  PhaseStatus
	}{
		{
			name:  "selected lane did not reach action",
			input: ResultInput{Facts: ExecutionFacts{NotRunReason: "step was not reached"}},
			want:  PhaseStatusNotRun,
		},
		{
			name:  "named prerequisite blocked action",
			input: ResultInput{Facts: ExecutionFacts{BlockedBy: "remote_host_available", Failure: "dedicated Host is absent"}},
			want:  PhaseStatusBlocked,
		},
		{
			name:  "attempt and required evidence passed",
			input: successfulResultInput(),
			want:  PhaseStatusPass,
		},
		{
			name: "attempted target failed",
			input: ResultInput{Facts: ExecutionFacts{
				Attempted: true, Failure: "product returned conflict",
				StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime,
			}},
			want: PhaseStatusFail,
		},
		{
			name: "required evidence missing after success",
			input: ResultInput{
				Facts:    ExecutionFacts{Attempted: true, Succeeded: true, StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime},
				Evidence: []EvidenceRecord{{Name: "normalized_response", Required: true, Present: false, Ref: "evidence/response.json"}},
			},
			want: PhaseStatusFail,
		},
		{
			name: "evidence write failure after success",
			input: ResultInput{
				Facts:    ExecutionFacts{Attempted: true, Succeeded: true, StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime},
				Evidence: []EvidenceRecord{{Name: "normalized_response", Required: true, Present: false, Ref: "evidence/response.json", WriteError: "disk full"}},
			},
			want: PhaseStatusFail,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := DeriveValidationResult(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if result.PhaseStatus != test.want {
				t.Fatalf("phase_status=%s, want %s: %#v", result.PhaseStatus, test.want, result)
			}
			if result.Attempted != test.input.Facts.Attempted {
				t.Fatalf("attempted=%v, want fact %v", result.Attempted, test.input.Facts.Attempted)
			}
			if result.Succeeded != test.input.Facts.Succeeded {
				t.Fatalf("succeeded=%v, want raw behavior fact %v", result.Succeeded, test.input.Facts.Succeeded)
			}
		})
	}
}

func TestDeriveValidationResultRejectsContradictoryFacts(t *testing.T) {
	t.Parallel()
	for _, input := range []ResultInput{
		{Facts: ExecutionFacts{Succeeded: true}},
		{Facts: ExecutionFacts{Attempted: true, Succeeded: true, BlockedBy: "remote_host_available"}},
		{Facts: ExecutionFacts{Attempted: true, Succeeded: true, Failure: "cannot be both successful and failed"}},
		{Facts: ExecutionFacts{Attempted: true, NotRunReason: "cannot be attempted and not run"}},
		{Facts: ExecutionFacts{Failure: "unattempted failure has no blocker"}},
		{Facts: ExecutionFacts{Attempted: true}, Evidence: []EvidenceRecord{{Name: "response", Present: true}}},
	} {
		if _, err := DeriveValidationResult(input); err == nil {
			t.Fatalf("contradictory facts were accepted: %#v", input.Facts)
		}
	}
}

func TestDeriveInstallerExecutionSeparatesArtifactFromLifecycle(t *testing.T) {
	t.Parallel()
	notRun := ResultInput{Facts: ExecutionFacts{NotRunReason: "lifecycle was not selected"}}
	installer, err := DeriveInstallerExecution(InstallerExecutionFacts{
		Format:            "nsis",
		ArtifactVerified:  true,
		InstallerExecuted: false,
		Artifact:          successfulResultInput(),
		Install:           notRun,
		Start:             notRun,
		Stop:              notRun,
		Uninstall:         notRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installer.Artifact.PhaseStatus != PhaseStatusPass || !installer.ArtifactVerified {
		t.Fatalf("artifact facts were not retained: %#v", installer)
	}
	if installer.Result.PhaseStatus != PhaseStatusNotRun || installer.InstallerExecuted {
		t.Fatalf("artifact verification became installer success: %#v", installer)
	}
}

func TestDeriveInstallerExecutionRequiresCompleteLifecycle(t *testing.T) {
	t.Parallel()
	pass := successfulResultInput()
	installer, err := DeriveInstallerExecution(InstallerExecutionFacts{
		Format: "msi", ArtifactVerified: true, InstallerExecuted: true,
		Artifact: pass, Install: pass, Start: pass, Stop: pass, Uninstall: pass,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installer.Lifecycle.PhaseStatus != PhaseStatusPass || installer.Result.PhaseStatus != PhaseStatusPass {
		t.Fatalf("complete lifecycle did not pass: %#v", installer)
	}

	failedUninstall := ResultInput{Facts: ExecutionFacts{
		Attempted: true, Failure: "uninstaller left registry entry",
		StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime,
	}}
	installer, err = DeriveInstallerExecution(InstallerExecutionFacts{
		Format: "msi", ArtifactVerified: true, InstallerExecuted: true,
		Artifact: pass, Install: pass, Start: pass, Stop: pass, Uninstall: failedUninstall,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installer.Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("cleanup failure did not fail installer lifecycle: %#v", installer)
	}
}

func TestDeriveAggregateResultKeepsMixedChildStatesHonest(t *testing.T) {
	t.Parallel()
	pass, err := DeriveValidationResult(successfulResultInput())
	if err != nil {
		t.Fatal(err)
	}
	fail, err := DeriveValidationResult(ResultInput{Facts: ExecutionFacts{
		Attempted: true, Failure: "assertion mismatch", StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := DeriveValidationResult(ResultInput{Facts: ExecutionFacts{BlockedBy: "host", Failure: "host unavailable"}})
	if err != nil {
		t.Fatal(err)
	}
	notRun, err := DeriveValidationResult(ResultInput{Facts: ExecutionFacts{NotRunReason: "outside lane"}})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		children []ValidationResult
		want     PhaseStatus
	}{
		{name: "all pass", children: []ValidationResult{pass, pass}, want: PhaseStatusPass},
		{name: "failure wins", children: []ValidationResult{pass, fail, blocked}, want: PhaseStatusFail},
		{name: "named blocker remains blocked", children: []ValidationResult{pass, blocked}, want: PhaseStatusBlocked},
		{name: "unreached child remains not run", children: []ValidationResult{pass, notRun}, want: PhaseStatusNotRun},
	}
	for _, test := range tests {
		result, err := DeriveAggregateResult(test.name, len(test.children), test.children)
		if err != nil {
			t.Fatal(err)
		}
		if result.PhaseStatus != test.want {
			t.Fatalf("%s phase_status=%s, want %s", test.name, result.PhaseStatus, test.want)
		}
	}
}

func successfulResultInput() ResultInput {
	return ResultInput{
		Facts:    ExecutionFacts{Attempted: true, Succeeded: true, StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime},
		Evidence: []EvidenceRecord{{Name: "normalized_response", Required: true, Present: true, Ref: "evidence/response.json"}},
	}
}
