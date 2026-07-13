// windows_validation_scenarios_test.go 把便携验证场景与真实 MCP 注册表做静态契约校验。
//
// 职责：
//   - 保证冻结 75 工具目录与当前 packaged MCP 工具注册表双向一致
//   - 保证所有 primary/supporting/cleanup 调用只使用真实 schema 允许的参数
//
// 边界：
//   - 不启动 Agent、Windows 服务、浏览器、调试器或 pipeline
//   - 不把静态 schema 通过当作 Windows 功能 PASS
package mcp

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/xsxdot/super-dev/agent/windowsvalidation"
)

func TestWindowsValidationScenariosMatchPackagedMCPRegistry(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "validation", "windows-real"))
	source, err := windowsvalidation.LoadPackageSource(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(&fakeAgentClient{})
	registered := make([]string, 0, len(server.tools))
	for name := range server.tools {
		registered = append(registered, name)
	}
	sort.Strings(registered)
	frozen := append([]string{}, source.Frozen.SourceSurface.MCPTools.Names...)
	sort.Strings(frozen)
	if fmt.Sprint(registered) != fmt.Sprint(frozen) {
		t.Fatalf("registered MCP tools differ from frozen catalog\nregistered=%v\nfrozen=%v", registered, frozen)
	}
	for _, scenario := range source.Scenarios {
		steps := append(append([]windowsvalidation.ScenarioStep{}, scenario.Steps...), scenario.Cleanup...)
		for _, step := range steps {
			registration, exists := server.tools[step.Tool]
			if !exists {
				t.Errorf("scenario %s step %s references unregistered tool %s", scenario.ID, step.ID, step.Tool)
				continue
			}
			assertScenarioArgumentsMatchSchema(t, scenario.ID, step, registration.Tool.InputSchema)
		}
	}
}

func assertScenarioArgumentsMatchSchema(t *testing.T, scenarioID string, step windowsvalidation.ScenarioStep, schema map[string]any) {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	if additional, exists := schema["additionalProperties"].(bool); exists && !additional {
		for key := range step.Arguments {
			if _, allowed := properties[key]; !allowed {
				t.Errorf("scenario %s step %s uses unknown %s argument %q", scenarioID, step.ID, step.Tool, key)
			}
		}
	}
	for _, required := range schemaStrings(schema["required"]) {
		if _, exists := step.Arguments[required]; !exists {
			t.Errorf("scenario %s step %s omits required %s argument %q", scenarioID, step.ID, step.Tool, required)
		}
	}
}

func schemaStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
