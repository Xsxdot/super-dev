// tools_langruntime.go 暴露 Language Runtime Provider 的 MCP 只读工具。
//
// 职责：
//   - 让 AI 先读取 schema，再建议、校验并预览语言运行配置
//   - 将 MCP 调用转发给本机 agent HTTP 契约
//
// 边界：
//   - 不读写 .superdev/config.yaml
//   - 不启动、停止或重启进程
//   - 不直接调用具体语言 provider
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

type languageRuntimeArgs struct {
	Language    string            `json:"language"`
	ProjectRoot string            `json:"project_root"`
	CWD         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	Config      map[string]any    `json:"config"`
	Intent      string            `json:"intent"`
	ArtifactDir string            `json:"artifact_dir"`
}

func (s *Server) listLanguageRuntimeProvidersTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	languages, err := s.client.ListLanguageRuntimeProviders(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("language runtime providers loaded", map[string]any{"languages": languages, "count": len(languages)}, nil, nil), nil
}

func (s *Server) describeLanguageRuntimeSchemaTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeLanguageRuntimeArgs(args)
	if !ok {
		return result, nil
	}
	schema, err := s.client.DescribeLanguageRuntimeSchema(ctx, req.Language)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("language runtime schema loaded", map[string]any{"schema": schema}, nil, nil), nil
}

func (s *Server) suggestServiceRuntimeTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeLanguageRuntimeArgs(args)
	if !ok {
		return result, nil
	}
	response, err := s.client.SuggestServiceRuntime(ctx, req.Language, languageRuntimeBody(req, false))
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("service runtime suggestions loaded", response, nil, nil), nil
}

func (s *Server) validateServiceRuntimeTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeLanguageRuntimeArgs(args)
	if !ok {
		return result, nil
	}
	response, err := s.client.ValidateServiceRuntime(ctx, req.Language, languageRuntimeBody(req, true))
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("service runtime validation completed", response, nil, nil), nil
}

func (s *Server) previewServiceExecutionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeLanguageRuntimeArgs(args)
	if !ok {
		return result, nil
	}
	response, err := s.client.PreviewServiceExecution(ctx, req.Language, languageRuntimeBody(req, true))
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("service execution preview completed", response, nil, nil), nil
}

func decodeLanguageRuntimeArgs(args json.RawMessage) (languageRuntimeArgs, CallToolResult, bool) {
	var req languageRuntimeArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return req, toolError("invalid_arguments", err.Error(), nil), false
	}
	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		return req, toolError("invalid_arguments", "language is required", nil), false
	}
	return req, CallToolResult{}, true
}

func languageRuntimeBody(req languageRuntimeArgs, includeConfig bool) map[string]any {
	body := map[string]any{
		"project_root": req.ProjectRoot,
		"cwd":          req.CWD,
	}
	if includeConfig {
		body["env"] = req.Env
		body["config"] = req.Config
	}
	if strings.TrimSpace(req.Intent) != "" {
		body["intent"] = req.Intent
	}
	if strings.TrimSpace(req.ArtifactDir) != "" {
		body["artifact_dir"] = req.ArtifactDir
	}
	return body
}
