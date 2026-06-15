// migration.go 提供 command runtime → language runtime 的迁移预览。
//
// 职责：解析简单命令给出可转换的 language runtime 配置；复杂命令/env_file 给 diagnostic 并保留原样。
// 边界：只预览不落盘；命令解析仅用于辅助迁移，不是长期运行模型（spec 风险条目）。
package langruntime

import (
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// MigrationPreview 是一次迁移预览的结果。
type MigrationPreview struct {
	Convertible bool                `json:"convertible"`
	Runtime     model.RuntimeConfig `json:"runtime"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
}

// PreviewCommandMigration 预览把 command runtime 迁移为 language runtime。
func PreviewCommandMigration(language model.ServiceLanguage, rt model.RuntimeConfig) MigrationPreview {
	if rt.Type != "" && rt.Type != model.RuntimeTypeCommand {
		return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
			Severity: SeverityInfo, Code: "runtime_not_command", Message: "runtime is not command",
		}}}
	}
	if strings.TrimSpace(rt.EnvFile) != "" {
		return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
			Severity: SeverityWarning, Field: "env_file", Code: "env_file_requires_apply_flow",
			Message: "env_file must be expanded explicitly before migration",
		}}}
	}
	if containsShellFeature(rt.Command) {
		return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
			Severity: SeverityWarning, Field: "command", Code: "command_shell_features_unsupported",
			Message: "complex shell command stays on command runtime",
		}}}
	}
	switch language {
	case model.LanguageGo:
		return previewGoCommandMigration(rt)
	case model.LanguageNode:
		return previewNodeCommandMigration(rt)
	case model.LanguagePython:
		return previewPythonCommandMigration(rt)
	default:
		return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
			Severity: SeverityInfo, Field: "language", Code: "language_migration_unsupported",
			Message: "command migration for this language arrives with its provider",
		}}}
	}
}

func previewGoCommandMigration(rt model.RuntimeConfig) MigrationPreview {
	env, fields := splitInlineEnvFields(strings.Fields(strings.TrimSpace(rt.Command)))
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "run" {
		return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
			Severity: SeverityWarning, Field: "command", Code: "go_command_unsupported",
			Message: "only simple `go run <package>` commands can be converted",
		}}}
	}
	config := map[string]any{"program": fields[2]}
	if len(fields) > 3 {
		config["program_args"] = append([]string{}, fields[3:]...)
	}
	return MigrationPreview{
		Convertible: true,
		Runtime: model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    rt.WorkingDir,
			Env:    mergeStringMaps(rt.EnvVars, env),
			Config: config,
		},
	}
}

func previewNodeCommandMigration(rt model.RuntimeConfig) MigrationPreview {
	env, fields := splitInlineEnvFields(strings.Fields(strings.TrimSpace(rt.Command)))
	if len(fields) == 0 {
		return unconvertible(rt, "command", "node_command_empty", "empty command")
	}
	base := commandBaseName(fields[0])
	config := map[string]any{}
	if base == "node" && len(fields) >= 2 {
		config["program"] = fields[1]
		if len(fields) > 2 {
			config["program_args"] = append([]string{}, fields[2:]...)
		}
	} else {
		// Node 生态常见 pnpm/npm/yarn 包裹启动；保留 runner 语义进入第二层逃生口。
		config[ConfigKeyRuntimeExecutable] = base
		if len(fields) > 1 {
			config[ConfigKeyRuntimeArgs] = append([]string{}, fields[1:]...)
		}
	}
	return MigrationPreview{
		Convertible: true,
		Runtime: model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    rt.WorkingDir,
			Env:    mergeStringMaps(rt.EnvVars, env),
			Config: config,
		},
	}
}

func previewPythonCommandMigration(rt model.RuntimeConfig) MigrationPreview {
	env, fields := splitInlineEnvFields(strings.Fields(strings.TrimSpace(rt.Command)))
	if len(fields) < 2 || !isPythonExecutable(fields[0]) {
		return unconvertible(rt, "command", "python_command_unsupported", "only python <file> or python -m <module> can be converted")
	}
	config := map[string]any{}
	if fields[1] == "-m" {
		if len(fields) < 3 {
			return unconvertible(rt, "command", "python_module_missing", "python -m requires a module name")
		}
		config["module"] = fields[2]
		if len(fields) > 3 {
			config["program_args"] = append([]string{}, fields[3:]...)
		}
	} else {
		config["program"] = fields[1]
		if len(fields) > 2 {
			config["program_args"] = append([]string{}, fields[2:]...)
		}
	}
	return MigrationPreview{
		Convertible: true,
		Runtime: model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    rt.WorkingDir,
			Env:    mergeStringMaps(rt.EnvVars, env),
			Config: config,
		},
	}
}

// splitInlineEnvFields 剥离命令头部的 KEY=VALUE 环境变量前缀（sh 语义）。
func splitInlineEnvFields(fields []string) (map[string]string, []string) {
	env := map[string]string{}
	i := 0
	for ; i < len(fields); i++ {
		key, value, ok := strings.Cut(fields[i], "=")
		if !ok || key == "" || strings.ContainsAny(key, "-/.") {
			break
		}
		env[key] = value
	}
	if len(env) == 0 {
		env = nil
	}
	return env, fields[i:]
}

func containsShellFeature(command string) bool {
	for _, token := range []string{"|", ">", "<", "&&", "||", ";", "$(", "`", "&"} {
		if strings.Contains(command, token) {
			return true
		}
	}
	return false
}

func commandBaseName(command string) string {
	if idx := strings.LastIndex(command, "/"); idx >= 0 {
		return command[idx+1:]
	}
	return command
}

func isPythonExecutable(command string) bool {
	base := commandBaseName(command)
	return base == "python" || base == "python3" || strings.HasPrefix(base, "python3.")
}

func unconvertible(rt model.RuntimeConfig, field, code, message string) MigrationPreview {
	return MigrationPreview{Runtime: rt, Diagnostics: []Diagnostic{{
		Severity: SeverityWarning, Field: field, Code: code, Message: message,
	}}}
}

func mergeStringMaps(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range a {
		out[key] = value
	}
	for key, value := range b {
		out[key] = value
	}
	return out
}
