// response.go 定义 MCP tool 的统一结构化响应。
//
// 职责：
//   - 为工具成功和业务错误生成 MCP CallToolResult
//   - 保证业务错误进入 isError=true，而不是 JSON-RPC protocol error
//
// 边界：
//   - 不做具体业务判断
package mcp

type toolPayload struct {
	OK          bool     `json:"ok"`
	Summary     string   `json:"summary,omitempty"`
	Data        any      `json:"data,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	NextActions []string `json:"next_actions,omitempty"`
}

type toolErrorPayload struct {
	OK         bool   `json:"ok"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Candidates any    `json:"candidates,omitempty"`
	Data       any    `json:"data,omitempty"`
}

func toolSuccess(summary string, data any, warnings []string, nextActions []string) CallToolResult {
	payload := toolPayload{OK: true, Summary: summary, Data: data, Warnings: warnings, NextActions: nextActions}
	return CallToolResult{
		Content:           []map[string]string{{"type": "text", "text": summary}},
		StructuredContent: payload,
	}
}

func toolError(code, message string, data any) CallToolResult {
	payload := toolErrorPayload{OK: false, Code: code, Message: message, Data: data}
	return CallToolResult{
		Content:           []map[string]string{{"type": "text", "text": message}},
		StructuredContent: payload,
		IsError:           true,
	}
}
