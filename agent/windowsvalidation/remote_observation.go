// remote_observation.go 定义远端机器身份的阶段比较与安全证据归档。
//
// 职责：
//   - 比较 baseline、远端写入前与 cleanup 后的 Agent node/machine digest
//   - 把缺失事实派生为 BLOCKED、身份漂移派生为 FAIL、完全一致派生为 PASS
//   - 只归档安全身份摘要与统一 ValidationResult
//
// 边界：
//   - 不读取 raw machine-id、hostname、IP、网络错误或任何凭据
//   - 不执行远端写入、cleanup 或网络探测
package windowsvalidation

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	// RemoteMachineIdentityCheckpointSchemaVersion 是阶段身份归档合同版本。
	RemoteMachineIdentityCheckpointSchemaVersion = "superdev.windows-remote-machine-identity-checkpoint/v1"
	// RemoteMachineIdentityCheckpointKind 是阶段身份归档的稳定类型。
	RemoteMachineIdentityCheckpointKind = "windows_remote_machine_identity_checkpoint"
)

// RemoteObservationStage 表示远端机器身份比较发生的固定阶段。
type RemoteObservationStage string

const (
	// RemoteObservationStageBeforeRemoteWrite 是任何远端写动作之前的身份门禁。
	RemoteObservationStageBeforeRemoteWrite RemoteObservationStage = "before-remote-write"
	// RemoteObservationStageAfterCleanup 是 remote scenario cleanup 完成后的身份门禁。
	RemoteObservationStageAfterCleanup RemoteObservationStage = "after-cleanup"
)

// RemoteMachineIdentity 是可归档的远端机器安全身份摘要。
type RemoteMachineIdentity struct {
	HostID          string `json:"host_id"`
	AgentNodeID     string `json:"agent_node_id"`
	MachineIDSHA256 string `json:"machine_id_sha256"`
}

// RemoteMachineIdentityCheckpoint 保存一个阶段与 baseline 的身份比较结果。
type RemoteMachineIdentityCheckpoint struct {
	SchemaVersion string                 `json:"schema_version"`
	Kind          string                 `json:"kind"`
	CampaignID    string                 `json:"campaign_id"`
	Stage         RemoteObservationStage `json:"stage"`
	ObservedAtUTC string                 `json:"observed_at_utc"`
	Baseline      RemoteMachineIdentity  `json:"baseline"`
	Observed      RemoteMachineIdentity  `json:"observed"`
	Result        ValidationResult       `json:"result"`
}

// DeriveRemoteMachineIdentityCheckpoint 从安全身份摘要派生阶段结论。
//
// 参数：
//   - campaignID: 当前 Windows campaign ID
//   - stage: before-remote-write 或 after-cleanup
//   - baseline: environment preflight 已准入的机器身份
//   - observed: 同一 adapter 在当前阶段重新读取的机器身份
//   - observedAtUTC: 只读观察完成时间
//
// 返回：
//   - 缺观察事实为 BLOCKED、身份漂移为 FAIL、完全一致为 PASS 的 checkpoint
//   - baseline 或固定身份参数非法时的合同错误
func DeriveRemoteMachineIdentityCheckpoint(
	campaignID string,
	stage RemoteObservationStage,
	baseline EnvironmentRemoteMachineObservation,
	observed EnvironmentRemoteMachineObservation,
	observedAtUTC string,
) (RemoteMachineIdentityCheckpoint, error) {
	if !campaignIDPattern.MatchString(strings.TrimSpace(campaignID)) {
		return RemoteMachineIdentityCheckpoint{}, fmt.Errorf("remote machine checkpoint campaign_id is invalid")
	}
	if stage != RemoteObservationStageBeforeRemoteWrite && stage != RemoteObservationStageAfterCleanup {
		return RemoteMachineIdentityCheckpoint{}, fmt.Errorf("remote machine checkpoint stage %q is unsupported", stage)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(observedAtUTC)); err != nil {
		return RemoteMachineIdentityCheckpoint{}, fmt.Errorf("remote machine checkpoint observed_at_utc is invalid")
	}
	baselineIdentity := safeRemoteMachineIdentity(baseline)
	if !completeRemoteMachineIdentity(baselineIdentity) {
		return RemoteMachineIdentityCheckpoint{}, fmt.Errorf("remote machine checkpoint baseline identity is incomplete")
	}
	observedIdentity := safeRemoteMachineIdentity(observed)
	checkpoint := RemoteMachineIdentityCheckpoint{
		SchemaVersion: RemoteMachineIdentityCheckpointSchemaVersion, Kind: RemoteMachineIdentityCheckpointKind,
		CampaignID: strings.TrimSpace(campaignID), Stage: stage, ObservedAtUTC: strings.TrimSpace(observedAtUTC),
		Baseline: baselineIdentity, Observed: observedIdentity,
	}
	key := "remote.machine-identity." + string(stage)
	evidence := EvidenceRecord{Name: key, Required: true, Present: true, Ref: "inline:remote-machine-identity-checkpoint#identity"}
	switch {
	case !completeRemoteMachineIdentity(observedIdentity):
		checkpoint.Result = withEvidence(blockedResult(key, "remote machine identity observation is incomplete"), evidence)
	case observedIdentity != baselineIdentity:
		checkpoint.Result = attemptedResult(false, "remote machine identity drifted from the admitted baseline", observedAtUTC, observedAtUTC, []EvidenceRecord{evidence})
	default:
		checkpoint.Result = attemptedResult(true, "", observedAtUTC, observedAtUTC, []EvidenceRecord{evidence})
	}
	return checkpoint, nil
}

// PersistRemoteMachineIdentityCheckpoint 把阶段身份比较写入固定安全证据树。
//
// 参数：
//   - resultsDir: 当前 campaign 结果目录
//   - checkpoint: 已由统一结果模型派生的阶段比较
//   - redactor: 最终写盘前的防御性脱敏器
//
// 返回：
//   - before-remote-write.json 或 after-cleanup.json 的完整路径
//   - 目录、阶段或写盘失败时的错误
func PersistRemoteMachineIdentityCheckpoint(resultsDir string, checkpoint RemoteMachineIdentityCheckpoint, redactor *Redactor) (string, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationRemoteObservation").WithFields(map[string]any{
		"campaign_id": checkpoint.CampaignID, "stage": checkpoint.Stage,
	})
	log.Info("开始写入 Windows 远端机器身份阶段证据")
	if strings.TrimSpace(resultsDir) == "" || redactor == nil {
		log.WithField("cause_code", "invalid_options").Error("Windows 远端机器身份阶段证据参数无效")
		return "", fmt.Errorf("remote machine checkpoint persistence requires results directory and redactor")
	}
	if checkpoint.Stage != RemoteObservationStageBeforeRemoteWrite && checkpoint.Stage != RemoteObservationStageAfterCleanup {
		log.WithField("cause_code", "invalid_stage").Error("Windows 远端机器身份阶段证据阶段无效")
		return "", fmt.Errorf("remote machine checkpoint stage %q is unsupported", checkpoint.Stage)
	}
	path := filepath.Join(resultsDir, "evidence", "remote-observation", string(checkpoint.Stage)+".json")
	redacted := redactor.Redact(RawMessageMap(checkpoint))
	if err := writeJSON(path, redacted); err != nil {
		log.WithField("cause_code", "write_failed").Error("Windows 远端机器身份阶段证据写入失败")
		return "", err
	}
	log.WithField("phase_status", checkpoint.Result.PhaseStatus).Info("Windows 远端机器身份阶段证据写入完成")
	return path, nil
}

func safeRemoteMachineIdentity(observation EnvironmentRemoteMachineObservation) RemoteMachineIdentity {
	return RemoteMachineIdentity{
		HostID: strings.TrimSpace(observation.HostID), AgentNodeID: strings.TrimSpace(observation.AgentNodeID),
		MachineIDSHA256: strings.ToLower(strings.TrimSpace(observation.MachineIDSHA256)),
	}
}

func completeRemoteMachineIdentity(identity RemoteMachineIdentity) bool {
	return strings.TrimSpace(identity.HostID) != "" && strings.TrimSpace(identity.AgentNodeID) != "" && validEnvironmentSHA256(identity.MachineIDSHA256)
}

func remoteMachineObservationFromManifest(manifest EnvironmentManifest) (EnvironmentRemoteMachineObservation, error) {
	for _, prerequisite := range manifest.Prerequisites {
		if prerequisite.Key != EnvironmentKeyRemoteLinuxMachine {
			continue
		}
		if prerequisite.Result.PhaseStatus != PhaseStatusPass {
			return EnvironmentRemoteMachineObservation{}, fmt.Errorf("remote machine baseline prerequisite is not PASS")
		}
		observation := EnvironmentRemoteMachineObservation{
			HostID: prerequisite.Observed.Attributes["host_id"], OS: prerequisite.Observed.Attributes["os"],
			KernelArch: prerequisite.Observed.Attributes["kernel_arch"], AgentArch: prerequisite.Observed.Attributes["agent_arch"],
			AgentNodeID: prerequisite.Observed.Attributes["agent_node_id"], MachineIDSHA256: prerequisite.Observed.Attributes["machine_id_sha256"],
		}
		if !completeRemoteMachineIdentity(safeRemoteMachineIdentity(observation)) {
			return EnvironmentRemoteMachineObservation{}, fmt.Errorf("remote machine baseline identity is incomplete")
		}
		return observation, nil
	}
	return EnvironmentRemoteMachineObservation{}, fmt.Errorf("remote machine baseline prerequisite is missing")
}

func observeRemoteMachineIdentityCheckpoint(
	ctx context.Context,
	reader EnvironmentAgentAPIReader,
	baseline EnvironmentRemoteMachineObservation,
	campaignID string,
	stage RemoteObservationStage,
	resultsDir string,
	redactor *Redactor,
) (RemoteMachineIdentityCheckpoint, string, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationRemoteObservation").WithFields(map[string]any{
		"campaign_id": campaignID, "stage": stage, "host_id": baseline.HostID,
	})
	log.Info("开始读取 Windows 远端机器身份阶段观察")
	observed, readErr := reader.ReadEnvironmentRemoteMachine(ctx, baseline.HostID)
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if readErr != nil {
		// API 错误不归档 raw error；缺少当前真实身份由统一模型稳定派生为 BLOCKED。
		observed = EnvironmentRemoteMachineObservation{HostID: baseline.HostID}
		log.WithField("cause_code", "remote_machine_observation_unavailable").Error("Windows 远端机器身份阶段观察不可用")
	}
	checkpoint, err := DeriveRemoteMachineIdentityCheckpoint(campaignID, stage, baseline, observed, observedAt)
	if err != nil {
		return RemoteMachineIdentityCheckpoint{}, "", err
	}
	path, err := PersistRemoteMachineIdentityCheckpoint(resultsDir, checkpoint, redactor)
	if err != nil {
		return checkpoint, "", err
	}
	log.WithField("phase_status", checkpoint.Result.PhaseStatus).Info("Windows 远端机器身份阶段观察完成")
	return checkpoint, path, nil
}

func remoteObservationCheckpointStep(checkpoint RemoteMachineIdentityCheckpoint, evidencePath, resultsDir string) StepExecution {
	reference := filepath.ToSlash(evidencePath)
	if relative, err := filepath.Rel(resultsDir, evidencePath); err == nil {
		reference = filepath.ToSlash(relative)
	}
	return StepExecution{
		StepID: "remote-machine-identity-" + string(checkpoint.Stage), Coverage: CoverageSupporting,
		Result: checkpoint.Result, InlineEvidence: map[string]any{
			"stage": checkpoint.Stage, "evidence_ref": reference,
		},
	}
}

func requireRemoteWriteCheckpoint(checkpoint RemoteMachineIdentityCheckpoint) error {
	if checkpoint.Stage != RemoteObservationStageBeforeRemoteWrite {
		return fmt.Errorf("remote writes require the before-remote-write identity checkpoint")
	}
	if checkpoint.Result.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("remote writes require a PASS machine identity checkpoint")
	}
	return nil
}

func evaluateEnvironmentDirectExposure(observation EnvironmentDirectExposureObservation) (PhaseStatus, string) {
	// 可达是确定的安全违例，优先于其它计数不完整；任何真实可达都必须 FAIL。
	if observation.CountsObserved && observation.ReachableCount > 0 {
		return PhaseStatusFail, "selected Host Agent port 57017 is directly reachable"
	}
	if !observation.CountsObserved || observation.CandidateCount <= 0 || observation.AttemptedCount <= 0 {
		return PhaseStatusBlocked, "direct exposure probe has no complete real dial attempts"
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(observation.CheckedAtUTC)); err != nil {
		return PhaseStatusBlocked, "direct exposure probe has no valid checked_at_utc fact"
	}
	if observation.InconclusiveCount > 0 || observation.AttemptedCount != observation.CandidateCount {
		return PhaseStatusBlocked, "direct exposure probe is inconclusive or only partially attempted"
	}
	if observation.ReachableCount != 0 {
		return PhaseStatusFail, "direct exposure probe returned an invalid reachable count"
	}
	return PhaseStatusPass, ""
}
