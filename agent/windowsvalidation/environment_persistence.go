// environment_persistence.go 持久化、加载并比较 Windows 环境清单。
//
// 职责：
//   - 将已重新验证且脱敏的 manifest 写为固定 JSON 与人工可读 Markdown
//   - 加载时重新派生 verdict 并拒绝序列化结果或 observation 篡改
//   - 按 prerequisite key 输出跨运行的 expected/observed/resolved/result 漂移
//
// 边界：
//   - 不执行 probe、不改变环境，也不把写盘成功当作 prerequisite PASS
//   - 写盘失败时仍通过返回值保留内存中的安全 manifest，不静默丢失事实
package windowsvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	// EnvironmentManifestJSONFilename 是 campaign 结果目录中的稳定机器可读文件名。
	EnvironmentManifestJSONFilename = "environment-manifest.json"
	// EnvironmentManifestMarkdownFilename 是 campaign 结果目录中的稳定人工复查文件名。
	EnvironmentManifestMarkdownFilename = "environment-manifest.md"
	// EnvironmentPlanJSONFilename 是与 manifest digest 绑定的本次冻结只读 plan。
	EnvironmentPlanJSONFilename = "environment-plan.json"
	// EnvironmentManifestComparisonFilename 是 A→B 漂移报告的稳定文件名。
	EnvironmentManifestComparisonFilename = "environment-manifest-comparison.json"
	// EnvironmentManifestComparisonSchemaVersion 是 A→B 漂移报告合同版本。
	EnvironmentManifestComparisonSchemaVersion = "superdev.windows-environment-manifest-comparison/v1"
	maxEnvironmentManifestBytes                = 8 * 1024 * 1024
)

// EnvironmentManifestPersistence 保存写盘结果以及写盘失败时仍可用的内存事实。
type EnvironmentManifestPersistence struct {
	Manifest     EnvironmentManifest       `json:"manifest"`
	Plan         EnvironmentCollectionPlan `json:"plan"`
	PlanPath     string                    `json:"plan_path,omitempty"`
	JSONPath     string                    `json:"json_path,omitempty"`
	MarkdownPath string                    `json:"markdown_path,omitempty"`
	Result       ValidationResult          `json:"result"`
}

// environmentPersistenceError 保留底层错误供 errors.Is/As 诊断，但对外只暴露稳定操作码。
//
// Windows 结果目录可能包含用户名或其他机器路径；该错误会进入 gate report，
// 因此 Error 不得代理 os.PathError 的原始路径。
type environmentPersistenceError struct {
	operation string
	cause     error
}

func (e *environmentPersistenceError) Error() string {
	return "environment manifest persistence failed: " + e.operation
}

func (e *environmentPersistenceError) Unwrap() error {
	return e.cause
}

// EnvironmentManifestDrift 描述同一 prerequisite 的一个稳定字段变化。
type EnvironmentManifestDrift struct {
	Key      string `json:"key"`
	Field    string `json:"field"`
	Previous string `json:"previous,omitempty"`
	Current  string `json:"current,omitempty"`
}

// EnvironmentManifestComparison 是 prepared A 与 post-install B 的可重派生差异表面。
type EnvironmentManifestComparison struct {
	SchemaVersion          string                     `json:"schema_version"`
	Kind                   string                     `json:"kind"`
	PreviousManifestSHA256 string                     `json:"previous_manifest_sha256"`
	CurrentManifestSHA256  string                     `json:"current_manifest_sha256"`
	Drifts                 []EnvironmentManifestDrift `json:"drifts"`
}

// PersistEnvironmentManifest 写入固定 JSON/Markdown，并在任何失败返回安全内存事实。
//
// 参数：
//   - resultsDir: 单次 campaign 的结果目录
//   - manifest: collector 返回的环境清单
//   - redactor: 当前 campaign 脱敏器；为空时创建无已知秘密的脱敏器
//
// 返回：
//   - 安全 manifest、目标路径与统一写盘结果
//   - 合同校验、脱敏后语义校验或任一文件写入失败时的错误
func PersistEnvironmentManifest(resultsDir string, manifest EnvironmentManifest, plan EnvironmentCollectionPlan, redactor *Redactor) (EnvironmentManifestPersistence, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentPersistence").WithField("campaign_id", manifest.CampaignID)
	log.Info("开始持久化 Windows 环境清单")
	if redactor == nil {
		redactor = NewRedactor()
	}
	safeManifest, err := sanitizeEnvironmentManifest(manifest, redactor)
	result := EnvironmentManifestPersistence{Manifest: safeManifest}
	if redactor.containsKnownSecret(plan) {
		err = fmt.Errorf("environment plan contains a registered secret")
	} else if bindingErr := VerifyEnvironmentManifestPlanBinding(safeManifest, plan); bindingErr != nil {
		err = bindingErr
	} else {
		result.Plan = plan
	}
	if err != nil {
		result.Result = environmentFailResult("environment-manifest-persistence", err.Error(), time.Now().UTC().Format(time.RFC3339Nano))
		log.WithField("cause_code", "contract_invalid").Error("Windows 环境清单持久化前校验失败")
		return result, err
	}
	result.JSONPath = filepath.Join(resultsDir, EnvironmentManifestJSONFilename)
	result.MarkdownPath = filepath.Join(resultsDir, EnvironmentManifestMarkdownFilename)
	result.PlanPath = filepath.Join(resultsDir, EnvironmentPlanJSONFilename)
	if err := writeJSON(result.PlanPath, plan); err != nil {
		result.Result = environmentFailResult("environment-manifest-persistence", "write environment plan JSON failed", time.Now().UTC().Format(time.RFC3339Nano))
		log.WithFields(map[string]any{"file": EnvironmentPlanJSONFilename, "write_status": "failed"}).Error("Windows 环境冻结 plan 写入失败，保留内存事实")
		return result, &environmentPersistenceError{operation: "write_plan", cause: err}
	}
	if err := writeJSON(result.JSONPath, safeManifest); err != nil {
		result.Result = environmentFailResult("environment-manifest-persistence", "write environment manifest JSON failed", time.Now().UTC().Format(time.RFC3339Nano))
		log.WithFields(map[string]any{"file": EnvironmentManifestJSONFilename, "write_status": "failed"}).Error("Windows 环境清单 JSON 写入失败，保留内存事实")
		return result, &environmentPersistenceError{operation: "write_manifest_json", cause: err}
	}
	if err := writeEnvironmentManifestMarkdown(result.MarkdownPath, safeManifest); err != nil {
		result.Result = environmentFailResult("environment-manifest-persistence", "write environment manifest Markdown failed", time.Now().UTC().Format(time.RFC3339Nano))
		log.WithFields(map[string]any{"file": EnvironmentManifestMarkdownFilename, "write_status": "failed"}).Error("Windows 环境清单 Markdown 写入失败，保留内存事实")
		return result, &environmentPersistenceError{operation: "write_manifest_markdown", cause: err}
	}
	result.Result = environmentPassResult("environment-manifest-persistence", time.Now().UTC().Format(time.RFC3339Nano))
	log.WithFields(map[string]any{"plan_file": EnvironmentPlanJSONFilename, "json_file": EnvironmentManifestJSONFilename, "markdown_file": EnvironmentManifestMarkdownFilename, "write_status": "complete"}).Info("Windows 环境清单持久化完成")
	return result, nil
}

// LoadEnvironmentManifest 从 JSON 加载清单并重新校验全部派生结果和 observation 语义。
func LoadEnvironmentManifest(path string) (EnvironmentManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return EnvironmentManifest{}, fmt.Errorf("open environment manifest: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxEnvironmentManifestBytes+1))
	if err != nil {
		return EnvironmentManifest{}, fmt.Errorf("read environment manifest: %w", err)
	}
	if len(payload) > maxEnvironmentManifestBytes {
		return EnvironmentManifest{}, fmt.Errorf("environment manifest exceeds %d bytes", maxEnvironmentManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest EnvironmentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("decode environment manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return EnvironmentManifest{}, err
	}
	if err := VerifyEnvironmentManifest(manifest); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("verify environment manifest: %w", err)
	}
	var plan EnvironmentCollectionPlan
	if err := readJSONFile(filepath.Join(filepath.Dir(path), EnvironmentPlanJSONFilename), &plan); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("read bound environment plan: %w", err)
	}
	if plan.SchemaVersion != EnvironmentPlanSchemaVersion || plan.Kind != EnvironmentPlanKind {
		return EnvironmentManifest{}, fmt.Errorf("bound environment plan identity is invalid")
	}
	if err := VerifyEnvironmentManifestPlanBinding(manifest, plan); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("verify environment manifest frozen plan binding: %w", err)
	}
	return manifest, nil
}

// CompareEnvironmentManifests 按稳定 key 返回两次已验证清单的字段漂移。
func CompareEnvironmentManifests(previous, current EnvironmentManifest) ([]EnvironmentManifestDrift, error) {
	if err := VerifyEnvironmentManifest(previous); err != nil {
		return nil, fmt.Errorf("verify previous environment manifest: %w", err)
	}
	if err := VerifyEnvironmentManifest(current); err != nil {
		return nil, fmt.Errorf("verify current environment manifest: %w", err)
	}
	previousByKey := environmentPrerequisiteMap(previous.Prerequisites)
	currentByKey := environmentPrerequisiteMap(current.Prerequisites)
	keys := make([]string, 0, len(previousByKey)+len(currentByKey))
	seen := map[string]struct{}{}
	for key := range previousByKey {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range currentByKey {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	drifts := make([]EnvironmentManifestDrift, 0)
	for _, key := range keys {
		before, beforeExists := previousByKey[key]
		after, afterExists := currentByKey[key]
		if !beforeExists || !afterExists {
			drifts = append(drifts, EnvironmentManifestDrift{Key: key, Field: "presence", Previous: fmt.Sprint(beforeExists), Current: fmt.Sprint(afterExists)})
			continue
		}
		fields := []struct {
			name          string
			previousValue string
			currentValue  string
		}{
			{"required", fmt.Sprint(before.Required), fmt.Sprint(after.Required)},
			{"collection_stage", string(before.CollectionStage), string(after.CollectionStage)},
			{"expected", CanonicalJSON(before.Expected), CanonicalJSON(after.Expected)},
			{"observed", CanonicalJSON(before.Observed), CanonicalJSON(after.Observed)},
			{"resolved", CanonicalJSON(before.Resolved), CanonicalJSON(after.Resolved)},
			{"result", string(before.Result.PhaseStatus), string(after.Result.PhaseStatus)},
		}
		for _, field := range fields {
			if field.previousValue != field.currentValue {
				drifts = append(drifts, EnvironmentManifestDrift{Key: key, Field: field.name, Previous: field.previousValue, Current: field.currentValue})
			}
		}
	}
	return drifts, nil
}

// BuildEnvironmentManifestComparison 从同一对 A/B manifest 构造确定性的报告对象。
func BuildEnvironmentManifestComparison(previous, current EnvironmentManifest) (EnvironmentManifestComparison, error) {
	drifts, err := CompareEnvironmentManifests(previous, current)
	if err != nil {
		return EnvironmentManifestComparison{}, err
	}
	return EnvironmentManifestComparison{
		SchemaVersion:          EnvironmentManifestComparisonSchemaVersion,
		Kind:                   "windows_environment_manifest_comparison",
		PreviousManifestSHA256: CanonicalEnvironmentManifestDigest(previous),
		CurrentManifestSHA256:  CanonicalEnvironmentManifestDigest(current),
		Drifts:                 drifts,
	}, nil
}

// PersistEnvironmentManifestComparison 将真实 A→B compare 写入 campaign 结果目录。
func PersistEnvironmentManifestComparison(resultsDir string, previous, current EnvironmentManifest) (EnvironmentManifestComparison, string, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentComparison").WithField("campaign_id", current.CampaignID)
	log.Info("开始持久化 Windows 环境 A→B 对比")
	comparison, err := BuildEnvironmentManifestComparison(previous, current)
	if err != nil {
		log.WithField("cause_code", "comparison_invalid").Error("Windows 环境 A→B 对比构造失败")
		return EnvironmentManifestComparison{}, "", err
	}
	path := filepath.Join(resultsDir, EnvironmentManifestComparisonFilename)
	if err := writeJSON(path, comparison); err != nil {
		log.WithField("cause_code", "comparison_write_failed").Error("Windows 环境 A→B 对比持久化失败")
		return comparison, path, &environmentPersistenceError{operation: "write_comparison", cause: err}
	}
	log.WithFields(map[string]any{"drift_count": len(comparison.Drifts), "file": EnvironmentManifestComparisonFilename}).Info("Windows 环境 A→B 对比持久化完成")
	return comparison, path, nil
}

func sanitizeEnvironmentManifest(manifest EnvironmentManifest, redactor *Redactor) (EnvironmentManifest, error) {
	provenanceErr := verifyEnvironmentCollectionProvenance(manifest)
	redacted := redactor.Redact(RawMessageMap(manifest))
	if redactor.containsKnownSecret(redacted) {
		return EnvironmentManifest{}, fmt.Errorf("environment manifest still contains a registered secret after redaction")
	}
	raw, err := json.Marshal(redacted)
	if err != nil {
		return EnvironmentManifest{}, fmt.Errorf("encode sanitized environment manifest: %w", err)
	}
	var safe EnvironmentManifest
	if err := json.Unmarshal(raw, &safe); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("decode sanitized environment manifest: %w", err)
	}
	for index := range safe.Prerequisites {
		safe.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(safe.Prerequisites[index])
	}
	if err := VerifyEnvironmentManifest(safe); err != nil {
		return safe, fmt.Errorf("verify sanitized environment manifest: %w", err)
	}
	if provenanceErr != nil {
		return safe, provenanceErr
	}
	sealEnvironmentCollectionProvenance(&safe)
	return safe, nil
}

func writeEnvironmentManifestMarkdown(path string, manifest EnvironmentManifest) error {
	var output strings.Builder
	fmt.Fprintf(&output, "# Windows environment manifest\n\n- Campaign: `%s`\n- Catalog: `%s`\n- Collected: `%s`\n- Result: **%s**\n\n", markdownCell(manifest.CampaignID), markdownCell(manifest.CatalogVersion), markdownCell(manifest.CollectedAtUTC), manifest.Result.PhaseStatus)
	output.WriteString("| Prerequisite | Expected | Observed | Resolved | Result | Remediation |\n|---|---|---|---|---|---|\n")
	for _, prerequisite := range manifest.Prerequisites {
		fmt.Fprintf(&output, "| `%s` | `%s` | `%s` | `%s` | %s | %s |\n",
			markdownCell(prerequisite.Key), markdownCell(CanonicalJSON(prerequisite.Expected)), markdownCell(CanonicalJSON(prerequisite.Observed)),
			markdownCell(CanonicalJSON(prerequisite.Resolved)), prerequisite.Result.PhaseStatus, markdownCell(prerequisite.Remediation))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output.String()), 0o644)
}

func environmentPrerequisiteMap(items []EnvironmentPrerequisite) map[string]EnvironmentPrerequisite {
	out := make(map[string]EnvironmentPrerequisite, len(items))
	for _, item := range items {
		out[item.Key] = item
	}
	return out
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains trailing value")
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}
