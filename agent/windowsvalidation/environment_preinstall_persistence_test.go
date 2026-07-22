// environment_preinstall_persistence_test.go 验证 prepared backup 的安装前环境绑定记录。
//
// 职责：
//   - 锁定 plan/manifest/decision/baseline/build/installer 的重派生绑定
//   - 证明记录或安装前 manifest 被改写后 lifecycle verifier 会 fail closed
//
// 边界：
//   - 只写测试临时目录，不执行安装器、MCP、Agent 或真实 Windows 命令
package windowsvalidation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPreparingBackupIdentityRejectsReadyUntilCollectionCompletes(t *testing.T) {
	backup := t.TempDir()
	categoryDigests := map[string]string{}
	for _, category := range cleanupBaselineCategories {
		categoryDigests[category] = strings.Repeat("a", 64)
	}
	manifest := preparedBackupManifest{
		SchemaVersion: 1, Kind: "superdev.windows-validation.prepared-backup", Status: "preparing",
		CreatedAtUTC: "2026-07-15T02:00:00Z", Lane: "nsis_core", CampaignID: testRemoteCampaignID,
		BaselineSHA256: strings.Repeat("b", 64), BaselineCategorySHA256: categoryDigests,
	}
	require.NoError(t, writeJSON(filepath.Join(backup, "backup-manifest.json"), manifest))

	resolved, loaded, err := loadPreparingBackupIdentity(backup, testRemoteCampaignID, "nsis_core")
	require.NoError(t, err)
	assert.Equal(t, backup, resolved)
	assert.Equal(t, "preparing", loaded.Status)

	manifest.Status = "ready"
	require.NoError(t, writeJSON(filepath.Join(backup, "backup-manifest.json"), manifest))
	_, _, err = loadPreparingBackupIdentity(backup, testRemoteCampaignID, "nsis_core")
	require.ErrorContains(t, err, "requires preparing")
	_, err = os.Stat(filepath.Join(backup, PreparedEnvironmentPreinstallDirectory))
	assert.True(t, os.IsNotExist(err), "identity loading must not create or mutate user-state artifacts")
}

func TestVerifyPreparedEnvironmentPreinstallRecordRederivesEveryBinding(t *testing.T) {
	frozen, plan, runner := passingPreInstallEnvironmentFixtures()
	frozen.Installers = []InstallerIdentity{
		{Filename: "SuperDev-0.2.1-setup.exe", Format: "nsis", SizeBytes: 42, SHA256: strings.Repeat("e", 64)},
		{Filename: "SuperDev-0.2.1.msi", Format: "msi", SizeBytes: 84, SHA256: strings.Repeat("f", 64)},
	}
	manifest, err := CollectEnvironmentPreInstallManifest(context.Background(), EnvironmentPreInstallCollectorOptions{
		CampaignID: testRemoteCampaignID, Plan: plan, PackageBuild: frozen, CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev\jvm-wrapper.exe`: {
				ResolvedPath: `C:\Program Files\SuperDev\jvm-wrapper.exe`,
				SHA256:       strings.Repeat("b", 64),
			},
		}},
		BrowserInventory: fixedEnvironmentBrowserInventory{paths: map[string]string{
			EnvironmentKeyBrowserChrome: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			EnvironmentKeyBrowserEdge:   `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}},
		Now: func() time.Time { return time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	request := EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionPreInstall, CollectionStage: EnvironmentCollectionStagePreInstall,
		ExpectedPlanDigest: CanonicalEnvironmentPlanDigest(plan),
	}
	decision, err := AdmitEnvironmentManifest(manifest, request)
	require.NoError(t, err)
	require.True(t, decision.Admitted)

	directory := t.TempDir()
	persisted, err := PersistEnvironmentManifest(directory, manifest, plan, NewRedactor())
	require.NoError(t, err)
	loaded, err := LoadEnvironmentManifest(persisted.JSONPath)
	require.NoError(t, err)
	planFileDigest, err := fileSHA256(persisted.PlanPath)
	require.NoError(t, err)
	manifestFileDigest, err := fileSHA256(persisted.JSONPath)
	require.NoError(t, err)
	pass := attemptedResult(true, "", "2026-07-15T02:00:00Z", "2026-07-15T02:00:01Z", []EvidenceRecord{{
		Name: "prepared_fact", Required: true, Present: true, Ref: "inline:prepared-environment-preinstall",
	}})
	overall, err := DeriveAggregateResult("prepared environment pre-install", 4, []ValidationResult{pass, pass, pass, decision.Result})
	require.NoError(t, err)
	prepared := preparedBackupManifest{BaselineSHA256: strings.Repeat("a", 64)}
	record := PreparedEnvironmentPreinstall{
		SchemaVersion: PreparedEnvironmentPreinstallSchemaVersion, Kind: PreparedEnvironmentPreinstallKind,
		CampaignID: testRemoteCampaignID, Lane: "nsis_core", PreparedBaselineSHA256: prepared.BaselineSHA256,
		BuildCommit: frozen.Build.GitCommit, ProductVersion: frozen.Build.ProductVersion,
		StableRuntimeInputSHA256: strings.Repeat("1", 64),
		StablePlanSHA256:         CanonicalPreInstallEnvironmentPlanDigest(plan),
		PlanFileSHA256:           planFileDigest, ManifestFileSHA256: manifestFileDigest,
		ManifestDigest:  CanonicalEnvironmentManifestDigest(loaded),
		InstallerChecks: []PackageFileIdentity{{Path: frozen.Installers[0].Filename, SizeBytes: 42, SHA256: strings.Repeat("e", 64)}},
		Request:         request, Decision: decision, PackageIntegrity: pass, InputSafety: pass, InstallerArtifact: pass,
		Result: overall, CollectedAtUTC: "2026-07-15T02:00:01Z",
	}

	err = verifyPreparedEnvironmentPreinstallRecord(
		record, prepared, frozen, loaded, plan, persisted.PlanPath, persisted.JSONPath,
		testRemoteCampaignID, "nsis_core",
	)
	require.NoError(t, err)

	tampered := record
	tampered.Decision.Admitted = false
	err = verifyPreparedEnvironmentPreinstallRecord(
		tampered, prepared, frozen, loaded, plan, persisted.PlanPath, persisted.JSONPath,
		testRemoteCampaignID, "nsis_core",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decision differs")

	tampered = record
	tampered.PreparedBaselineSHA256 = strings.Repeat("3", 64)
	err = verifyPreparedEnvironmentPreinstallRecord(
		tampered, prepared, frozen, loaded, plan, persisted.PlanPath, persisted.JSONPath,
		testRemoteCampaignID, "nsis_core",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseline binding differs")

	assert.Equal(t, filepath.Base(persisted.JSONPath), EnvironmentManifestJSONFilename)
}

func TestCoreOnlyPreparedEnvironmentAllowsOnlyNamedBlockedPrerequisitesWithoutInstaller(t *testing.T) {
	frozen, plan, runner := passingPreInstallEnvironmentFixtures()
	runner.errors[EnvironmentKeyToolchainNode] = errors.New("node executable not found")
	manifest, err := CollectEnvironmentPreInstallManifest(context.Background(), EnvironmentPreInstallCollectorOptions{
		CampaignID: testRemoteCampaignID, Plan: plan, PackageBuild: frozen, CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev\jvm-wrapper.exe`: {
				ResolvedPath: `C:\Program Files\SuperDev\jvm-wrapper.exe`,
				SHA256:       strings.Repeat("b", 64),
			},
		}},
		BrowserInventory: fixedEnvironmentBrowserInventory{paths: map[string]string{
			EnvironmentKeyBrowserChrome: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			EnvironmentKeyBrowserEdge:   `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}},
		Now: func() time.Time { return time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	request := EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionDiagnostic, CollectionStage: EnvironmentCollectionStagePreInstall,
		ExpectedPlanDigest: CanonicalEnvironmentPlanDigest(plan), AllowedBlockedKeys: []string{EnvironmentKeyToolchainNode},
	}
	decision, err := AdmitEnvironmentManifest(manifest, request)
	require.NoError(t, err)
	require.True(t, decision.Admitted)
	assert.Equal(t, PhaseStatusBlocked, decision.Result.PhaseStatus)

	withoutAllowance := request
	withoutAllowance.AllowedBlockedKeys = nil
	rejected, err := AdmitEnvironmentManifest(manifest, withoutAllowance)
	require.NoError(t, err)
	assert.False(t, rejected.Admitted)

	directory := t.TempDir()
	persisted, err := PersistEnvironmentManifest(directory, manifest, plan, NewRedactor())
	require.NoError(t, err)
	loaded, err := LoadEnvironmentManifest(persisted.JSONPath)
	require.NoError(t, err)
	planFileDigest, err := fileSHA256(persisted.PlanPath)
	require.NoError(t, err)
	manifestFileDigest, err := fileSHA256(persisted.JSONPath)
	require.NoError(t, err)
	pass := attemptedResult(true, "", "2026-07-15T02:00:00Z", "2026-07-15T02:00:01Z", []EvidenceRecord{{
		Name: "prepared_fact", Required: true, Present: true, Ref: "inline:prepared-environment-preinstall",
	}})
	installerNotRun := notRunResult("core_only excludes installer artifact")
	overall, err := DeriveAggregateResult("prepared environment pre-install", 3, []ValidationResult{pass, pass, decision.Result})
	require.NoError(t, err)
	prepared := preparedBackupManifest{BaselineSHA256: strings.Repeat("a", 64)}
	record := PreparedEnvironmentPreinstall{
		SchemaVersion: PreparedEnvironmentPreinstallSchemaVersion, Kind: PreparedEnvironmentPreinstallKind,
		CampaignID: testRemoteCampaignID, Lane: "core_only", PreparedBaselineSHA256: prepared.BaselineSHA256,
		BuildCommit: frozen.Build.GitCommit, ProductVersion: frozen.Build.ProductVersion,
		StableRuntimeInputSHA256: strings.Repeat("1", 64),
		StablePlanSHA256:         CanonicalPreInstallEnvironmentPlanDigest(plan),
		PlanFileSHA256:           planFileDigest, ManifestFileSHA256: manifestFileDigest,
		ManifestDigest: CanonicalEnvironmentManifestDigest(loaded), Request: request, Decision: decision,
		PackageIntegrity: pass, InputSafety: pass, InstallerArtifact: installerNotRun,
		Result: overall, CollectedAtUTC: "2026-07-15T02:00:01Z",
	}
	require.Empty(t, record.InstallerChecks)
	require.NoError(t, verifyPreparedEnvironmentPreinstallRecord(
		record, prepared, frozen, loaded, plan, persisted.PlanPath, persisted.JSONPath,
		testRemoteCampaignID, "core_only",
	))
}

func TestPreparedRuntimeInputDigestUsesBoundSemanticInput(t *testing.T) {
	directory := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260715T020000Z-aabbcc"
	values := map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.runtime-input",
		"mcp_path":            filepath.Join(directory, "superdev-mcp.exe"),
		"installer_directory": filepath.Join(directory, "installers"),
		"campaign_root":       filepath.Join(directory, "campaigns"), "results_root": filepath.Join(directory, "results"),
		"approval_wait_seconds": 120,
	}
	prettyPath := filepath.Join(directory, "pretty.json")
	pretty, err := json.MarshalIndent(values, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(prettyPath, append(pretty, '\n'), 0o600))
	orderedPath := filepath.Join(directory, "ordered.json")
	ordered := `{"results_root":"` + filepath.ToSlash(filepath.Join(directory, "results")) + `","campaign_root":"` + filepath.ToSlash(filepath.Join(directory, "campaigns")) + `","approval_wait_seconds":120,"installer_directory":"` + filepath.ToSlash(filepath.Join(directory, "installers")) + `","mcp_path":"` + filepath.ToSlash(filepath.Join(directory, "superdev-mcp.exe")) + `","kind":"superdev.windows-validation.runtime-input","schema_version":1}`
	require.NoError(t, os.WriteFile(orderedPath, []byte(ordered), 0o600))

	prettyInput, prettyDigest, err := loadPreparedEnvironmentRuntimeInput(prettyPath, campaignID, "msi_smoke")
	require.NoError(t, err)
	orderedInput, orderedDigest, err := loadPreparedEnvironmentRuntimeInput(orderedPath, campaignID, "msi_smoke")
	require.NoError(t, err)
	assert.Equal(t, prettyInput, orderedInput)
	assert.Equal(t, prettyDigest, orderedDigest)
	require.NoError(t, verifyPreparedEnvironmentRuntimeInput(PreparedEnvironmentPreinstallEvidence{Record: PreparedEnvironmentPreinstall{
		StableRuntimeInputSHA256: prettyDigest, CampaignID: campaignID, Lane: "msi_smoke",
	}}, orderedInput))

	changed := orderedInput
	changed.ApprovalWaitSeconds++
	assert.NotEqual(t, prettyDigest, canonicalPreInstallRuntimeInputDigest(changed))
	require.ErrorContains(t, verifyPreparedEnvironmentRuntimeInput(PreparedEnvironmentPreinstallEvidence{Record: PreparedEnvironmentPreinstall{
		StableRuntimeInputSHA256: prettyDigest, CampaignID: campaignID, Lane: "msi_smoke",
	}}, changed), "stable fields differ")
}

func TestPreparedRuntimeInputAllowsPostInstallPlaceholdersAndBindsOnlyStableFields(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260715T020000Z-aabbcc"
	input := RuntimeInput{
		SchemaVersion: 1, Kind: "superdev.windows-validation.runtime-input",
		MCPPath: filepath.Join(directory, "installed", "superdev-mcp.exe"), InstallerDirectory: "",
		CampaignRoot: filepath.Join(directory, "campaigns"), ResultsRoot: filepath.Join(directory, "results"),
		LinuxHostID: "REPLACE_AFTER_FRESH_PROFILE", LinuxRoot: windowsValidationLinuxRootTemplate,
		RemoteGovernanceAttestationPath: "REPLACE_AFTER_FRESH_PROFILE",
		AgentDataDirectory:              filepath.Join(directory, ".superdev"),
		AllowedEnvironmentBlockers:      []string{EnvironmentKeyToolchainNode},
		Lane:                            "core_only", CampaignID: campaignID,
	}
	require.NoError(t, validatePreInstallRuntimeInput(input))
	preparedDigest := canonicalPreInstallRuntimeInputDigest(input)

	postInstall := input
	postInstall.InstallerDirectory = filepath.Join(directory, "irrelevant-installer-path")
	postInstall.LinuxHostID = "fresh-linux-host"
	postInstall.RemoteGovernanceAttestationPath = filepath.Join(directory, "remote-governance.json")
	require.Equal(t, preparedDigest, canonicalPreInstallRuntimeInputDigest(postInstall))
	require.NoError(t, verifyPreparedEnvironmentRuntimeInput(PreparedEnvironmentPreinstallEvidence{Record: PreparedEnvironmentPreinstall{
		StableRuntimeInputSHA256: preparedDigest, CampaignID: campaignID, Lane: "core_only",
	}}, postInstall))

	drifted := postInstall
	drifted.ChromeVersion = "127.0.0.0"
	require.NotEqual(t, preparedDigest, canonicalPreInstallRuntimeInputDigest(drifted))
	require.ErrorContains(t, verifyPreparedEnvironmentRuntimeInput(PreparedEnvironmentPreinstallEvidence{Record: PreparedEnvironmentPreinstall{
		StableRuntimeInputSHA256: preparedDigest, CampaignID: campaignID, Lane: "core_only",
	}}, drifted), "stable fields differ")
}
