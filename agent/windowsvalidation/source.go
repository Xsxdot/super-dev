// source.go 加载并校验便携包的受控源资产。
//
// 职责：
//   - 读取冻结构建、场景、七语言 fixture 和 pipeline 配置
//   - 在交叉编译前机械拒绝工具/provider 漂移与断点行漂移

// 边界：
//   - 不读取被忽略的 .scratch 设计资产
//   - 不执行 Windows 功能场景
package windowsvalidation

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const fixtureKind = "superdev.windows-validation.fixture"

// FixtureManifest 描述一个可复制语言夹具的运行与调试合同。
type FixtureManifest struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	ID            string         `json:"id"`
	Provider      string         `json:"provider"`
	CWD           string         `json:"cwd"`
	Runtime       FixtureRuntime `json:"runtime"`
	Build         struct {
		WindowsCommand string   `json:"windows_command"`
		Prerequisites  []string `json:"prerequisites"`
	} `json:"build"`
	Run struct {
		WindowsCommand string `json:"windows_command"`
		Port           int    `json:"port"`
	} `json:"run"`
	Readiness struct {
		URL    string `json:"url"`
		Status int    `json:"status"`
	} `json:"readiness"`
	Debug struct {
		Source        string         `json:"source"`
		Line          int            `json:"line"`
		Marker        string         `json:"marker"`
		Variables     []string       `json:"variables"`
		Prerequisites []string       `json:"prerequisites"`
		Preflight     []string       `json:"preflight,omitempty"`
		Expected      map[string]any `json:"expected_non_secret_variables,omitempty"`
	} `json:"debug"`
	Contract struct {
		NormalPath            string `json:"normal_path"`
		ErrorPath             string `json:"error_path"`
		AuthHeader            string `json:"auth_header"`
		AuthValueEnv          string `json:"auth_value_env"`
		AuthScheme            string `json:"auth_scheme"`
		AuthorizationTemplate string `json:"authorization_template"`
	} `json:"contract"`
}

// FixtureRuntime 是可直接传给 language runtime MCP 的 config/env 片段。
type FixtureRuntime struct {
	Config map[string]any    `json:"config"`
	Env    map[string]string `json:"env"`
}

// PackageSource 是打包前已加载的全部受控资产。
type PackageSource struct {
	Root                 string
	Frozen               FrozenBuild
	Scenarios            []Scenario
	Fixtures             []FixtureManifest
	RemotePipelineConfig map[string]any
	Coverage             []CoverageAssignment
}

// LoadPackageSource 从 validation/windows-real 根加载并验证固定资产。
func LoadPackageSource(root string) (PackageSource, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return PackageSource{}, fmt.Errorf("resolve package source: %w", err)
	}
	if err := rejectSymlinks(root); err != nil {
		return PackageSource{}, err
	}
	var source PackageSource
	source.Root = root
	if err := readJSONFile(filepath.Join(root, "manifest", "frozen-build.json"), &source.Frozen); err != nil {
		return PackageSource{}, err
	}
	scenarioFiles, err := filepath.Glob(filepath.Join(root, "scenarios", "*.json"))
	if err != nil {
		return PackageSource{}, fmt.Errorf("glob scenarios: %w", err)
	}
	sort.Strings(scenarioFiles)
	for _, path := range scenarioFiles {
		var scenario Scenario
		if err := readJSONFile(path, &scenario); err != nil {
			return PackageSource{}, err
		}
		if err := ValidateScenario(scenario); err != nil {
			return PackageSource{}, fmt.Errorf("validate %s: %w", filepath.Base(path), err)
		}
		source.Scenarios = append(source.Scenarios, scenario)
	}
	fixtureFiles, err := filepath.Glob(filepath.Join(root, "fixtures", "*", "fixture.json"))
	if err != nil {
		return PackageSource{}, fmt.Errorf("glob fixtures: %w", err)
	}
	sort.Strings(fixtureFiles)
	for _, path := range fixtureFiles {
		var fixture FixtureManifest
		if err := readJSONFile(path, &fixture); err != nil {
			return PackageSource{}, err
		}
		if err := validateFixture(root, path, fixture); err != nil {
			return PackageSource{}, err
		}
		source.Fixtures = append(source.Fixtures, fixture)
	}
	pipelinePath := filepath.Join(root, "pipeline", "project-pipeline.json")
	if err := readJSONFile(pipelinePath, &source.RemotePipelineConfig); err != nil {
		return PackageSource{}, err
	}
	if err := validateFrozen(source.Frozen); err != nil {
		return PackageSource{}, err
	}
	source.Coverage, err = ValidateCoverage(source.Frozen.SourceSurface.MCPTools.Names, source.Scenarios)
	if err != nil {
		return PackageSource{}, err
	}
	if err := validateProviders(source.Frozen.SourceSurface.LanguageRuntimeProviders.Names, source.Fixtures); err != nil {
		return PackageSource{}, err
	}
	if err := validateRemoteArtifacts(root, source.Scenarios); err != nil {
		return PackageSource{}, err
	}
	return source, nil
}

func validateFrozen(frozen FrozenBuild) error {
	if frozen.SchemaVersion != 1 || frozen.Build.GitCommit == "" || frozen.Build.ProductVersion == "" {
		return fmt.Errorf("frozen build identity is incomplete")
	}
	if frozen.SourceSurface.MCPTools.Count != len(frozen.SourceSurface.MCPTools.Names) {
		return fmt.Errorf("frozen MCP count=%d names=%d", frozen.SourceSurface.MCPTools.Count, len(frozen.SourceSurface.MCPTools.Names))
	}
	if frozen.SourceSurface.LanguageRuntimeProviders.Count != len(frozen.SourceSurface.LanguageRuntimeProviders.Names) {
		return fmt.Errorf("frozen provider count=%d names=%d", frozen.SourceSurface.LanguageRuntimeProviders.Count, len(frozen.SourceSurface.LanguageRuntimeProviders.Names))
	}
	if len(frozen.Installers) != 2 {
		return fmt.Errorf("frozen installer count=%d, want 2", len(frozen.Installers))
	}
	for label, set := range map[string]FrozenNameSet{
		"MCP tools":          frozen.SourceSurface.MCPTools,
		"language providers": frozen.SourceSurface.LanguageRuntimeProviders,
	} {
		seen := map[string]bool{}
		for _, name := range set.Names {
			if name == "" || seen[name] {
				return fmt.Errorf("frozen %s contains blank or duplicate name %q", label, name)
			}
			seen[name] = true
		}
		raw, _ := json.Marshal(set.Names)
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		if !strings.EqualFold(digest, set.CanonicalJSONSHA256) {
			return fmt.Errorf("frozen %s canonical digest mismatch", label)
		}
	}
	return nil
}

func validateFixture(root, manifestPath string, fixture FixtureManifest) error {
	if fixture.SchemaVersion != 1 || fixture.Kind != fixtureKind || fixture.ID == "" || fixture.Provider == "" {
		return fmt.Errorf("fixture %s identity is invalid", manifestPath)
	}
	if fixture.CWD == "" || fixture.Build.WindowsCommand == "" || fixture.Run.WindowsCommand == "" || fixture.Readiness.URL == "" {
		return fmt.Errorf("fixture %s execution contract is incomplete", fixture.ID)
	}
	fixtureDir := filepath.Dir(manifestPath)
	for _, relative := range []string{fixture.Build.WindowsCommand, fixture.Run.WindowsCommand, fixture.Debug.Source} {
		path := filepath.Join(fixtureDir, filepath.FromSlash(relative))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return fmt.Errorf("fixture %s required file %s is unavailable", fixture.ID, relative)
		}
	}
	if len(fixture.Debug.Preflight) > 0 {
		preflightPath := filepath.Join(fixtureDir, filepath.FromSlash(fixture.Debug.Preflight[0]))
		if info, err := os.Stat(preflightPath); err != nil || info.IsDir() {
			return fmt.Errorf("fixture %s debug preflight %s is unavailable", fixture.ID, fixture.Debug.Preflight[0])
		}
	}
	if fixture.Debug.Line <= 0 || fixture.Debug.Marker == "" || len(fixture.Debug.Variables) == 0 {
		return fmt.Errorf("fixture %s debug contract is incomplete", fixture.ID)
	}
	// 夹具配置只能保存公开 campaign ID；若 manifest 回退成完整 Authorization 环境变量，打包阶段立即拒绝。
	if fixture.Contract.AuthHeader != "Authorization" || fixture.Contract.AuthValueEnv != "FIXTURE_CAMPAIGN_ID" ||
		fixture.Contract.AuthScheme != "bearer_campaign_id" || fixture.Contract.AuthorizationTemplate != fixtureAuthorizationPrefix+"{campaign_id}" {
		return fmt.Errorf("fixture %s authorization contract may persist a credential or has drifted", fixture.ID)
	}
	sourcePath := filepath.Join(fixtureDir, filepath.FromSlash(fixture.Debug.Source))
	line, err := readLine(sourcePath, fixture.Debug.Line)
	if err != nil {
		return fmt.Errorf("fixture %s breakpoint: %w", fixture.ID, err)
	}
	if !strings.Contains(line, fixture.Debug.Marker) {
		return fmt.Errorf("fixture %s breakpoint line %d no longer contains marker %q", fixture.ID, fixture.Debug.Line, fixture.Debug.Marker)
	}
	if relative, err := filepath.Rel(root, fixtureDir); err != nil || filepath.ToSlash(relative) != strings.TrimSuffix(fixture.CWD, "/") {
		return fmt.Errorf("fixture %s cwd %q does not match package location", fixture.ID, fixture.CWD)
	}
	return nil
}

func validateProviders(frozen []string, fixtures []FixtureManifest) error {
	want := append([]string{}, frozen...)
	got := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		got = append(got, fixture.Provider)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return fmt.Errorf("fixture providers=%v do not equal frozen providers=%v", got, want)
	}
	return nil
}

func validateRemoteArtifacts(root string, scenarios []Scenario) error {
	remote, found := scenarioByID(scenarios, "remote-pipeline")
	if !found {
		return fmt.Errorf("remote-pipeline scenario is missing")
	}
	for _, suffix := range []string{"a", "b"} {
		metadata, ok := remote.Variables["artifact_version_"+suffix].(map[string]any)
		if !ok {
			return fmt.Errorf("remote artifact %s version metadata is missing", suffix)
		}
		version := fmt.Sprint(metadata["default"])
		directory := filepath.Join(root, "pipeline", "artifacts", "version-"+suffix)
		versionBytes, err := os.ReadFile(filepath.Join(directory, "version.txt"))
		if err != nil {
			return fmt.Errorf("read remote artifact %s version: %w", suffix, err)
		}
		if strings.TrimSpace(string(versionBytes)) != version {
			return fmt.Errorf("remote artifact %s version.txt does not match scenario version %q", suffix, version)
		}
		var manifest struct {
			Version string `json:"version"`
			Payload struct {
				Path   string `json:"path"`
				Size   int    `json:"size"`
				SHA256 string `json:"sha256"`
			} `json:"payload"`
		}
		if err := readJSONFile(filepath.Join(directory, "manifest.json"), &manifest); err != nil {
			return err
		}
		if manifest.Version != version || manifest.Payload.Path != "payload.txt" {
			return fmt.Errorf("remote artifact %s manifest identity is invalid", suffix)
		}
		payload, err := os.ReadFile(filepath.Join(directory, manifest.Payload.Path))
		if err != nil {
			return fmt.Errorf("read remote artifact %s payload: %w", suffix, err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(payload))
		if len(payload) != manifest.Payload.Size || !strings.EqualFold(digest, manifest.Payload.SHA256) {
			return fmt.Errorf("remote artifact %s payload identity mismatch", suffix)
		}
	}
	return nil
}

func readJSONFile(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readLine(path string, number int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for current := 1; scanner.Scan(); current++ {
		if current == number {
			return scanner.Text(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("line %d does not exist", number)
}

func rejectSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("portable package source contains symlink: %s", path)
		}
		return nil
	})
}
