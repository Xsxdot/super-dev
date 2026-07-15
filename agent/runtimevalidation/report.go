// report.go 持久化脱敏 evidence、manifest、summary.md 和最后的 authoritative summary.json。
//
// 职责：
//   - 为每份 evidence 记录 size/SHA-256，并在写盘前扫描已知 secret
//   - 先写 evidence 与人读 summary.md，最后原子写 complete=true 的 summary.json
//   - 只从当前 campaign 的 authoritative JSON 加载结论
//
// 边界：
//   - summary.md、旧 campaign 和半写 JSON 都不能声明 PASS
//   - 不持久化 raw MCP stdout、credential、approval token 或 cookie
package runtimevalidation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	summarySchemaVersion = 1
	summaryKind          = "superdev.runtime-validation.summary"
)

// BorrowedAttestation 保存 foundation 与 borrowed non-self topology 的运行前后身份。
type BorrowedAttestation struct {
	RemoteHostID               string `json:"remote_host_id"`
	ExpectedRemoteIdentity     string `json:"expected_remote_identity"`
	FoundationDigestBefore     string `json:"foundation_digest_before"`
	FoundationDigestAfter      string `json:"foundation_digest_after"`
	BorrowedTopologyDigest     string `json:"borrowed_topology_digest"`
	RemoteNodeConfirmedNonSelf bool   `json:"remote_node_confirmed_non_self"`
}

// EvidenceFileIdentity 记录一份脱敏 evidence 的路径、size 和 SHA-256。
type EvidenceFileIdentity struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// EvidenceManifest 是 authoritative summary 所引用的全部 evidence 清单。
type EvidenceManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	Kind          string                 `json:"kind"`
	Files         []EvidenceFileIdentity `json:"files"`
}

// Summary 是 target-native strict run 唯一 authoritative 报告。
type Summary struct {
	SchemaVersion          int                 `json:"schema_version"`
	Kind                   string              `json:"kind"`
	Complete               bool                `json:"complete"`
	CampaignID             string              `json:"campaign_id"`
	StartedAtUTC           string              `json:"started_at_utc,omitempty"`
	FinishedAtUTC          string              `json:"finished_at_utc,omitempty"`
	Target                 Target              `json:"target"`
	Host                   HostIdentity        `json:"native_host"`
	Bundle                 BundleReceipt       `json:"bundle"`
	LiveTools              []string            `json:"live_tools"`
	Coverage               CoverageReport      `json:"coverage"`
	PrimaryEvidence        []ToolEvidence      `json:"primary_evidence"`
	Languages              []LanguageResult    `json:"languages"`
	Borrowed               BorrowedAttestation `json:"borrowed_attestation"`
	Journal                JournalSnapshot     `json:"journal"`
	Cleanup                CleanupResult       `json:"cleanup"`
	Checks                 []CheckResult       `json:"checks"`
	Verdict                Verdict             `json:"verdict"`
	EvidenceManifestSHA256 string              `json:"evidence_manifest_sha256"`
}

// ReportInput 提交 summary 事实、具名 evidence 和只用于写前扫描的 secret。
type ReportInput struct {
	Summary          Summary
	Evidence         map[string]any
	ForbiddenSecrets []string
}

// WriteReport 按 evidence→manifest→summary.md→summary.json 顺序写入新 campaign 目录。
func WriteReport(root string, input ReportInput) (Summary, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationReport").WithFields(map[string]any{"campaign_id": input.Summary.CampaignID, "results_root": root})
	if strings.TrimSpace(root) == "" || strings.TrimSpace(input.Summary.CampaignID) == "" {
		return Summary{}, fmt.Errorf("report root and campaign_id are required")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return Summary{}, fmt.Errorf("campaign report root already exists: %s", root)
	}
	if err := os.MkdirAll(filepath.Join(root, "evidence"), 0o700); err != nil {
		return Summary{}, err
	}
	log.WithField("evidence_count", len(input.Evidence)).Info("开始写入 runtime validation 脱敏 evidence")
	names := make([]string, 0, len(input.Evidence))
	for name := range input.Evidence {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest := EvidenceManifest{SchemaVersion: 1, Kind: "superdev.runtime-validation.evidence-manifest", Files: []EvidenceFileIdentity{}}
	for _, name := range names {
		if filepath.Base(name) != name || name == "." || name == "" || filepath.Ext(name) != ".json" {
			return Summary{}, fmt.Errorf("evidence name %q is not a safe JSON filename", name)
		}
		raw, err := json.MarshalIndent(input.Evidence[name], "", "  ")
		if err != nil {
			return Summary{}, fmt.Errorf("marshal evidence %s: %w", name, err)
		}
		raw = append(raw, '\n')
		if secret := firstSecretInBytes(raw, input.ForbiddenSecrets); secret != "" {
			return Summary{}, fmt.Errorf("evidence %s contains a registered secret", name)
		}
		path := filepath.Join(root, "evidence", name)
		if err := atomicWriteBytes(path, raw, 0o600); err != nil {
			return Summary{}, err
		}
		digest := sha256.Sum256(raw)
		manifest.Files = append(manifest.Files, EvidenceFileIdentity{Path: filepath.ToSlash(filepath.Join("evidence", name)), Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])})
	}
	manifestPath := filepath.Join(root, "evidence-manifest.json")
	if err := atomicWriteJSON(manifestPath, manifest, 0o600); err != nil {
		return Summary{}, err
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Summary{}, err
	}
	manifestDigest := sha256.Sum256(manifestRaw)
	input.Summary.SchemaVersion = summarySchemaVersion
	input.Summary.Kind = summaryKind
	input.Summary.Complete = true
	input.Summary.EvidenceManifestSHA256 = hex.EncodeToString(manifestDigest[:])
	if input.Summary.FinishedAtUTC == "" {
		input.Summary.FinishedAtUTC = time.Now().UTC().Format(time.RFC3339Nano)
	}
	markdown := renderSummaryMarkdown(input.Summary)
	if secret := firstSecretInBytes([]byte(markdown), input.ForbiddenSecrets); secret != "" {
		return Summary{}, fmt.Errorf("summary markdown contains a registered secret")
	}
	if err := atomicWriteBytes(filepath.Join(root, "summary.md"), []byte(markdown), 0o600); err != nil {
		return Summary{}, err
	}
	summaryRaw, err := json.MarshalIndent(input.Summary, "", "  ")
	if err != nil {
		return Summary{}, err
	}
	if secret := firstSecretInBytes(summaryRaw, input.ForbiddenSecrets); secret != "" {
		return Summary{}, fmt.Errorf("authoritative summary contains a registered secret")
	}
	// summary.json 是最后一次原子 rename；在此之前的任何错误都只能留下非 authoritative 调查材料。
	if err := atomicWriteJSON(filepath.Join(root, "summary.json"), input.Summary, 0o600); err != nil {
		return Summary{}, err
	}
	log.WithFields(map[string]any{"status": input.Summary.Verdict.Status, "evidence_manifest_sha256": input.Summary.EvidenceManifestSHA256}).Info("runtime validation authoritative summary.json 已最后写入")
	return input.Summary, nil
}

// LoadAuthoritativeSummary 只接受 schema/kind 匹配且 complete=true 的 summary.json。
func LoadAuthoritativeSummary(path string) (Summary, error) {
	var summary Summary
	raw, err := os.ReadFile(path)
	if err != nil {
		return summary, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return summary, fmt.Errorf("decode authoritative summary: %w", err)
	}
	if summary.SchemaVersion != summarySchemaVersion || summary.Kind != summaryKind || !summary.Complete {
		return summary, fmt.Errorf("authoritative summary is not complete or has an unsupported contract")
	}
	if summary.Verdict.Status == StatusPass && !summary.Verdict.Complete {
		return summary, fmt.Errorf("authoritative PASS summary has incomplete verdict")
	}
	return summary, nil
}

// ExitCodeForStatus 映射 strict CLI 稳定退出码：0=PASS、1=FAIL、2=BLOCKED。
func ExitCodeForStatus(status Status) int {
	switch status {
	case StatusPass:
		return 0
	case StatusBlocked:
		return 2
	default:
		return 1
	}
}

func renderSummaryMarkdown(summary Summary) string {
	return fmt.Sprintf("# Runtime validation summary\n\n- Campaign: `%s`\n- Target: `%s/%s`\n- Native host: `%s/%s`\n- Bundle manifest: `%s`\n- Live tools: `%d`\n- Primary evidence: `%d`\n- Language results: `%d`\n- Verdict: **%s**\n- Root cause: `%s`\n",
		summary.CampaignID, summary.Target.OS, summary.Target.Architecture, summary.Host.OS, summary.Host.Architecture,
		summary.Bundle.ManifestSHA256, len(summary.LiveTools), len(summary.PrimaryEvidence), len(summary.Languages), summary.Verdict.Status, summary.Verdict.RootCause.Code)
}

func firstSecretInBytes(content []byte, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(string(content), secret) {
			return secret
		}
	}
	return ""
}
