// fixture.go 定义并加载七语言跨平台 runtime/debug fixture 合同。
//
// 职责：
//   - 固定 Go、Node.js、Python、Java、Kotlin、Rust、C++ 的平台命令与探针
//   - 强制动态端口、正常/受控错误 probe 和真实断点变量合同
//   - 为 provider matrix 提供与 OS 分支无关的参数化输入
//
// 边界：
//   - 不执行工具链或 DAP，不分配端口，也不修改旧 Windows fixture
//   - 不允许 fixture 自行注册项目或拥有 MCP primary verdict
package runtimevalidation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const (
	// FixtureSchemaVersion 是 runtime validation fixture 当前接受的 schema 版本。
	FixtureSchemaVersion = 1
	// FixtureKind 标识跨平台 runtime validation fixture。
	FixtureKind = "superdev.runtime-validation.fixture"
)

var requiredFixtureProviders = []string{"cpp", "go", "java", "kotlin", "node", "python", "rust"}
var requiredFixturePlatforms = []string{"darwin", "linux", "windows"}

// Fixture 描述一个语言 provider 在三类原生 OS 上的运行与调试合同。
type Fixture struct {
	SchemaVersion        int                        `json:"schema_version"`
	Kind                 string                     `json:"kind"`
	ID                   string                     `json:"id"`
	Provider             string                     `json:"provider"`
	WorkingDir           string                     `json:"working_dir"`
	Runtime              FixtureRuntime             `json:"runtime"`
	Platforms            map[string]FixturePlatform `json:"platforms"`
	Readiness            HTTPProbe                  `json:"readiness"`
	NormalProbe          HTTPProbe                  `json:"normal_probe"`
	ControlledErrorProbe HTTPProbe                  `json:"controlled_error_probe"`
	Debug                DebugContract              `json:"debug"`
}

// FixtureRuntime 保存 language runtime provider 的 config 和非秘密环境模板。
type FixtureRuntime struct {
	Config map[string]any    `json:"config"`
	Env    map[string]string `json:"env"`
}

// FixturePlatform 保存单个平台的 preflight、build、run 命令和产物探针。
type FixturePlatform struct {
	Preflight  CommandSpec     `json:"preflight"`
	Build      CommandSpec     `json:"build"`
	Run        CommandSpec     `json:"run"`
	Executable string          `json:"executable"`
	Probes     []ArtifactProbe `json:"probes"`
}

// CommandSpec 描述无需 shell 解析的 executable/arguments 命令。
type CommandSpec struct {
	Executable string            `json:"executable"`
	Arguments  []string          `json:"arguments,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// ArtifactProbe 描述 build/run 后必须成立的 executable、module、file 或 tcp 事实。
type ArtifactProbe struct {
	Type         string `json:"type"`
	Path         string `json:"path,omitempty"`
	Module       string `json:"module,omitempty"`
	PortVariable string `json:"port_variable,omitempty"`
}

// HTTPProbe 描述 loopback fixture 的 readiness、正常或受控错误请求。
type HTTPProbe struct {
	Type           string         `json:"type"`
	Method         string         `json:"method"`
	Path           string         `json:"path"`
	ExpectedStatus int            `json:"expected_status"`
	ExpectedFields map[string]any `json:"expected_fields,omitempty"`
}

// DebugContract 描述 fixture 的真实 DAP provider、源码断点和非秘密变量期望。
type DebugContract struct {
	Provider          string         `json:"provider"`
	AdapterResource   string         `json:"adapter_resource,omitempty"`
	Source            string         `json:"source"`
	Line              int            `json:"line"`
	Marker            string         `json:"marker"`
	ExpectedVariables map[string]any `json:"expected_variables"`
}

// LoadFixtures 从每个 provider 子目录加载 fixture.json 并校验七语言全集。
//
// 参数：
//   - root: 包含 provider 子目录的 fixture 根目录
//
// 返回：
//   - 按 provider 名排序的七个已校验 fixture
//   - 文件、JSON、schema、重复或缺失 provider 错误
//
// 注意：旧 validation/windows-real 资产保持冻结，本函数只读取传入的新 runtime 目录。
func LoadFixtures(root string) ([]Fixture, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationFixture").WithField("root", root)
	log.Info("开始加载七语言 runtime validation fixtures")
	entries, err := os.ReadDir(root)
	if err != nil {
		log.WithErr(err).Error("读取 fixture 根目录失败")
		return nil, fmt.Errorf("read fixtures root: %w", err)
	}
	fixtures := make([]Fixture, 0, len(requiredFixtureProviders))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "fixture.json")
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			log.WithErr(err).WithField("provider_dir", entry.Name()).Error("读取 fixture 失败")
			return nil, fmt.Errorf("read fixture %s: %w", entry.Name(), err)
		}
		var fixture Fixture
		if err := json.Unmarshal(raw, &fixture); err != nil {
			log.WithErr(err).WithField("provider_dir", entry.Name()).Error("解析 fixture 失败")
			return nil, fmt.Errorf("decode fixture %s: %w", entry.Name(), err)
		}
		if err := ValidateFixture(fixture); err != nil {
			log.WithErr(err).WithField("provider_dir", entry.Name()).Error("fixture 合同无效")
			return nil, err
		}
		if _, ok := seen[fixture.Provider]; ok {
			return nil, fmt.Errorf("fixture provider %s is duplicated", fixture.Provider)
		}
		seen[fixture.Provider] = struct{}{}
		fixtures = append(fixtures, fixture)
	}
	missing := make([]string, 0)
	for _, provider := range requiredFixtureProviders {
		if _, ok := seen[provider]; !ok {
			missing = append(missing, provider)
		}
	}
	if len(missing) > 0 || len(fixtures) != len(requiredFixtureProviders) {
		return nil, fmt.Errorf("fixture provider set mismatch: missing=%v count=%d", missing, len(fixtures))
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Provider < fixtures[j].Provider })
	log.WithFields(map[string]any{"fixture_count": len(fixtures), "providers": requiredFixtureProviders}).Info("七语言 runtime validation fixtures 加载完成")
	return fixtures, nil
}

// ValidateFixture 校验单个 fixture 的平台命令、动态端口、探针和断点合同。
//
// 参数：
//   - fixture: 待校验的语言 fixture
//
// 返回：
//   - schema、provider、平台、端口、probe 或 debug 合同不完整时的错误
//
// 注意：FIXTURE_PORT 必须由 runner 注入 ${PORT}，fixture 不得占用固定端口。
func ValidateFixture(fixture Fixture) error {
	if fixture.SchemaVersion != FixtureSchemaVersion || fixture.Kind != FixtureKind {
		return fmt.Errorf("fixture %s has unsupported schema or kind", fixture.ID)
	}
	if strings.TrimSpace(fixture.ID) == "" || !containsString(requiredFixtureProviders, fixture.Provider) {
		return fmt.Errorf("fixture %s has unsupported provider %q", fixture.ID, fixture.Provider)
	}
	if strings.TrimSpace(fixture.WorkingDir) == "" || len(fixture.Runtime.Config) == 0 {
		return fmt.Errorf("fixture %s needs working_dir and runtime config", fixture.ID)
	}
	// 固定端口会让七语言并行或残留进程互相污染；端口只能由 campaign 分配。
	if fixture.Runtime.Env["FIXTURE_PORT"] != "${PORT}" {
		return fmt.Errorf("fixture %s must use dynamic ${PORT}", fixture.ID)
	}
	if fixture.Runtime.Env["FIXTURE_CAMPAIGN_ID"] != "${CAMPAIGN_ID}" {
		return fmt.Errorf("fixture %s must correlate runtime with ${CAMPAIGN_ID}", fixture.ID)
	}
	for _, platform := range requiredFixturePlatforms {
		contract, ok := fixture.Platforms[platform]
		if !ok {
			return fmt.Errorf("fixture %s is missing %s platform contract", fixture.ID, platform)
		}
		if err := validateFixturePlatform(fixture.ID, platform, contract); err != nil {
			return err
		}
	}
	if len(fixture.Platforms) != len(requiredFixturePlatforms) {
		return fmt.Errorf("fixture %s contains unsupported platform contracts", fixture.ID)
	}
	for name, probe := range map[string]HTTPProbe{
		"readiness": fixture.Readiness, "normal": fixture.NormalProbe, "controlled_error": fixture.ControlledErrorProbe,
	} {
		if probe.Type != "http" || strings.TrimSpace(probe.Method) == "" || !strings.HasPrefix(probe.Path, "/") || probe.ExpectedStatus <= 0 {
			return fmt.Errorf("fixture %s has invalid %s probe", fixture.ID, name)
		}
	}
	if fixture.NormalProbe.ExpectedStatus >= 400 || fixture.ControlledErrorProbe.ExpectedStatus < 400 {
		return fmt.Errorf("fixture %s normal/error probes do not prove both paths", fixture.ID)
	}
	if strings.TrimSpace(fixture.Debug.Provider) == "" || strings.TrimSpace(fixture.Debug.Source) == "" ||
		fixture.Debug.Line <= 0 || strings.TrimSpace(fixture.Debug.Marker) == "" || len(fixture.Debug.ExpectedVariables) == 0 {
		return fmt.Errorf("fixture %s has incomplete debug breakpoint contract", fixture.ID)
	}
	return nil
}

func validateFixturePlatform(fixtureID, platform string, contract FixturePlatform) error {
	for name, command := range map[string]CommandSpec{"preflight": contract.Preflight, "build": contract.Build, "run": contract.Run} {
		if strings.TrimSpace(command.Executable) == "" {
			return fmt.Errorf("fixture %s %s %s command is missing", fixtureID, platform, name)
		}
	}
	if strings.TrimSpace(contract.Executable) == "" || len(contract.Probes) == 0 {
		return fmt.Errorf("fixture %s %s needs executable and probes", fixtureID, platform)
	}
	seenTCP := false
	seenArtifact := false
	for _, probe := range contract.Probes {
		switch probe.Type {
		case "tcp":
			seenTCP = probe.PortVariable == "FIXTURE_PORT"
		case "executable", "file":
			seenArtifact = strings.TrimSpace(probe.Path) != ""
		case "module":
			seenArtifact = strings.TrimSpace(probe.Module) != ""
		default:
			return fmt.Errorf("fixture %s %s has unsupported probe %q", fixtureID, platform, probe.Type)
		}
	}
	if !seenTCP || !seenArtifact {
		return fmt.Errorf("fixture %s %s must prove an artifact and dynamic tcp readiness", fixtureID, platform)
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
