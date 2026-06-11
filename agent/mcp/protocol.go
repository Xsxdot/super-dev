// protocol.go 实现 SuperDev MCP 的最小 JSON-RPC stdio 协议层。
//
// 职责：
//   - 处理 initialize、notifications/initialized、ping、tools/list、tools/call
//   - 将 MCP tool call 分发到注册表
//   - 保证 stdout 只写 newline-delimited JSON-RPC 消息
//
// 边界：
//   - 不实现 resources、prompts、sampling、elicitation
//   - 不在协议层访问 agent HTTP API
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/xsxdot/super-dev/agent/internal/buildinfo"
)

const protocolVersion = "2025-11-25"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  map[string]any  `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CallToolResult 是 MCP tools/call 的返回体。
type CallToolResult struct {
	Content           []map[string]string `json:"content"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError,omitempty"`
}

// Server 持有 MCP 工具注册表和 agent client。
type Server struct {
	client AgentClient
	tools  map[string]registeredTool
}

// NewServer 创建 MCP Server。
//
// 参数：
//   - client: 访问本机 SuperDev agent 的抽象，协议壳测试中可为 nil
//
// 返回：
//   - 已注册默认工具的 MCP Server
func NewServer(client AgentClient) *Server {
	s := &Server{client: client, tools: map[string]registeredTool{}}
	for _, tool := range defaultTools(s) {
		s.tools[tool.Tool.Name] = tool
	}
	return s
}

// Handle 处理单个 JSON-RPC 请求。
//
// 参数：
//   - ctx: 调用上下文
//   - req: 已解析的 JSON-RPC 请求
//
// 返回：
//   - JSON-RPC 响应；通知请求可返回空响应
func (s *Server) Handle(ctx context.Context, req rpcRequest) rpcResponse {
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, -32600, "invalid jsonrpc version")
	}
	switch req.Method {
	case "initialize":
		return successResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "superdev-mcp",
				"title":   "SuperDev MCP",
				"version": buildinfo.Version,
			},
			"instructions": "Use SuperDev MCP tools to inspect runtime state, control deployments, and query logs through the local SuperDev agent.",
		})
	case "notifications/initialized":
		return rpcResponse{}
	case "ping":
		return successResponse(req.ID, map[string]any{})
	case "tools/list":
		tools := make([]Tool, 0, len(s.tools))
		for _, tool := range s.tools {
			tools = append(tools, tool.Tool)
		}
		return successResponse(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, -32602, "invalid tools/call params")
		}
		tool, ok := s.tools[params.Name]
		if !ok {
			return errorResponse(req.ID, -32601, "unknown tool: "+params.Name)
		}
		result, err := tool.Handler(ctx, params.Arguments)
		if err != nil {
			return errorResponse(req.ID, -32603, err.Error())
		}
		return successResponse(req.ID, mustObject(result))
	default:
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

// CallToolForSmoke 调用已注册 MCP 工具，供本机 smoke/e2e 命令复用真实工具链。
//
// 参数：
//   - ctx: 调用上下文
//   - name: MCP 工具名
//   - args: 工具参数 JSON
//
// 返回：
//   - 工具业务结果，业务错误会以 IsError=true 返回
//   - 工具不存在或 handler 异常时返回 error
//
// 注意：
//   - 仅用于本机 smoke 和未来 e2e，不作为外部协议入口
func (s *Server) CallToolForSmoke(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	tool, ok := s.tools[name]
	if !ok {
		return CallToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.Handler(ctx, args)
}

// RunStdio reads newline-delimited JSON-RPC requests and writes responses.
//
// 参数：
//   - ctx: 调用上下文
//   - in: stdin 或测试输入
//   - out: stdout 或测试输出
//
// 返回：
//   - 输入扫描或响应写入错误
//
// 注意：
//   - 该方法只向 out 写 JSON-RPC 响应，日志必须走 stderr
func (s *Server) RunStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			resp := errorResponse(nil, -32700, "parse error")
			if err := writeResponse(out, resp); err != nil {
				return err
			}
			continue
		}
		resp := s.Handle(ctx, req)
		if len(resp.ID) == 0 && resp.Error == nil && resp.Result == nil {
			continue
		}
		if err := writeResponse(out, resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) toolNotImplemented(context.Context, json.RawMessage) (CallToolResult, error) {
	return toolError("not_implemented", "tool implementation is not wired yet", nil), nil
}

func successResponse(id json.RawMessage, result map[string]any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func writeResponse(out io.Writer, resp rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func mustObject(v any) map[string]any {
	data, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}
