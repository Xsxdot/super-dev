// environment_persistence_test.go 验证环境清单归档、篡改检测和写盘失败回退。
//
// 职责：
//   - 锁定 JSON/Markdown 固定输出以及加载时的重新派生校验
//   - 覆盖 observed/resolved/verdict 篡改与不可写目录下的内存事实保留
//
// 边界：
//   - 仅写测试临时目录，不执行环境 probe 或真实 Windows campaign
package windowsvalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistEnvironmentManifestWritesSafeReloadableArtifacts(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	secret := "environment-persistence-secret"
	manifest.Prerequisites[0].Remediation += " " + secret
	sealEnvironmentCollectionProvenance(&manifest)
	redactor := NewRedactor()
	redactor.RegisterSecret("token", secret)

	result, err := PersistEnvironmentManifest(t.TempDir(), manifest, smallEnvironmentPlan(), redactor)
	require.NoError(t, err)
	assert.Equal(t, PhaseStatusPass, result.Result.PhaseStatus)
	assert.FileExists(t, result.PlanPath)
	assert.FileExists(t, result.JSONPath)
	assert.FileExists(t, result.MarkdownPath)
	loaded, err := LoadEnvironmentManifest(result.JSONPath)
	require.NoError(t, err)
	assert.Equal(t, manifest.CampaignID, loaded.CampaignID)
	_, err = AdmitEnvironmentManifest(loaded, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: loaded.PlanDigest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trusted in-memory collector")

	jsonBytes, err := os.ReadFile(result.JSONPath)
	require.NoError(t, err)
	markdownBytes, err := os.ReadFile(result.MarkdownPath)
	require.NoError(t, err)
	assert.NotContains(t, string(jsonBytes), secret)
	assert.NotContains(t, string(markdownBytes), secret)
	assert.Contains(t, string(markdownBytes), EnvironmentKeyToolchainNode)
}

func TestLoadEnvironmentManifestRejectsPersistedObservationAndVerdictTampering(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	root := t.TempDir()
	persisted, err := PersistEnvironmentManifest(root, manifest, smallEnvironmentPlan(), NewRedactor())
	require.NoError(t, err)
	baseline, err := os.ReadFile(persisted.JSONPath)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*EnvironmentManifest)
	}{
		{name: "observed version", mutate: func(value *EnvironmentManifest) {
			for index := range value.Prerequisites {
				if value.Prerequisites[index].Key == EnvironmentKeyToolchainNode {
					value.Prerequisites[index].Observed.Version = "23.0.0"
				}
			}
		}},
		{name: "resolved source", mutate: func(value *EnvironmentManifest) {
			for index := range value.Prerequisites {
				if value.Prerequisites[index].Key == EnvironmentKeyAdapterNative {
					value.Prerequisites[index].Resolved.Source = "path_fallback"
				}
			}
		}},
		{name: "resolved path", mutate: func(value *EnvironmentManifest) {
			for index := range value.Prerequisites {
				if value.Prerequisites[index].Key == EnvironmentKeyAdapterNative {
					value.Prerequisites[index].Expected.Path = value.Prerequisites[index].Resolved.Path
					value.Prerequisites[index].Resolved.Path = `D:\forged\lldb-dap.exe`
				}
			}
		}},
		{name: "phase status", mutate: func(value *EnvironmentManifest) {
			for index := range value.Prerequisites {
				if value.Prerequisites[index].Key == EnvironmentKeyToolchainNode {
					value.Prerequisites[index].Result.PhaseStatus = PhaseStatusFail
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var tampered EnvironmentManifest
			require.NoError(t, json.Unmarshal(baseline, &tampered))
			test.mutate(&tampered)
			directory := t.TempDir()
			path := filepath.Join(directory, EnvironmentManifestJSONFilename)
			require.NoError(t, writeJSON(path, tampered))
			require.NoError(t, writeJSON(filepath.Join(directory, EnvironmentPlanJSONFilename), smallEnvironmentPlan()))
			_, err := LoadEnvironmentManifest(path)
			require.Error(t, err)
		})
	}
}

func TestLoadEnvironmentManifestRejectsCoordinatedExpectedAndObservedTampering(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	directory := t.TempDir()
	persisted, err := PersistEnvironmentManifest(directory, manifest, smallEnvironmentPlan(), NewRedactor())
	require.NoError(t, err)
	var tampered EnvironmentManifest
	require.NoError(t, readJSONFile(persisted.JSONPath, &tampered))
	index := environmentPrerequisiteIndex(t, tampered, EnvironmentKeyToolchainNode)
	tampered.Prerequisites[index].Expected.Version = "23.0.0"
	tampered.Prerequisites[index].Observed.Version = "23.0.0"
	tampered.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(tampered.Prerequisites[index])
	require.NoError(t, VerifyEnvironmentManifest(tampered))
	require.NoError(t, writeJSON(persisted.JSONPath, tampered))

	_, err = LoadEnvironmentManifest(persisted.JSONPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frozen plan binding")
}

func TestPersistEnvironmentManifestWriteFailureKeepsSafeInMemoryFacts(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	blockedRoot := filepath.Join(t.TempDir(), `C:\Users\alice\secret-path`)
	require.NoError(t, os.WriteFile(blockedRoot, []byte("not a directory"), 0o600))

	result, err := PersistEnvironmentManifest(blockedRoot, manifest, smallEnvironmentPlan(), NewRedactor())
	require.Error(t, err)
	assert.Equal(t, PhaseStatusFail, result.Result.PhaseStatus)
	assert.Equal(t, manifest.CampaignID, result.Manifest.CampaignID)
	assert.Len(t, result.Manifest.Prerequisites, len(manifest.Prerequisites))
	assert.Equal(t, EnvironmentKeyAdapterNative, environmentPrerequisiteByKey(t, result.Manifest, EnvironmentKeyAdapterNative).Key)
	assert.NotContains(t, err.Error(), blockedRoot)
	assert.NotContains(t, err.Error(), "alice")
	assert.Equal(t, "environment manifest persistence failed: write_plan", err.Error())
}

func TestPersistEnvironmentManifestValidationFailureNeverReturnsRawSecret(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	secret := "registered-observed-secret"
	redactor := NewRedactor()
	redactor.RegisterSecret("token", secret)
	for index := range manifest.Prerequisites {
		if manifest.Prerequisites[index].Key == EnvironmentKeyToolchainNode {
			manifest.Prerequisites[index].Observed.Version = secret
		}
	}
	sealEnvironmentCollectionProvenance(&manifest)

	result, err := PersistEnvironmentManifest(t.TempDir(), manifest, smallEnvironmentPlan(), redactor)
	require.Error(t, err)
	assert.NotContains(t, CanonicalJSON(result.Manifest), secret)
	assert.Contains(t, CanonicalJSON(result.Manifest), "[REDACTED:TOKEN:T01]")
}
