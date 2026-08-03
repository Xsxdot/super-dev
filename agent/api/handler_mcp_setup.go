// handler_mcp_setup.go 提供归属节点自举 claude-code MCP 最小配置的端点。
//
// 职责：
//   - 仅处理 claude-code 一种客户端、仅本机（agent 自身所在机器）HOME 目录下
//     的 ~/.claude.json、仅合并写入其中的 mcpServers.superdev 一个条目
//   - JSON 读取 → 合并（保留其他既有键）→ 临时文件 + rename 原子写回
//   - 已存在同名 superdev 条目时整体覆盖，保证多次调用幂等收敛到同一结果
//
// 边界：
//   - 不安装 skill 目录、不写 SessionStart hook、不配置除 superdev 以外的任何
//     MCP connector——这些能力挂账在计划偏离 1，本任务只做最小可用配置
//   - 不写入任何 token/密钥：env 只含指向本机 loopback 地址的 SUPERDEV_AGENT_URL，
//     鉴权由 superdev-mcp 进程自身按本机自举流程完成，配置文件里永不出现凭据
//   - 文件内容非法 JSON 时拒绝写入并返回 400，绝不用空骨架覆盖用户已有但暂时
//     解析失败的配置——静默毁坏用户配置比拒绝服务更危险
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/xsxdot/super-dev/agent/model"
)

// mcpSetupHomeDir 是本机 HOME 目录解析的测试替身。
//
// 生产环境为空字符串时，用 os.UserHomeDir() 解析真实路径；测试把它赋值为
// t.TempDir()，从而在不触碰真实用户目录的前提下验证真实文件读写。
var mcpSetupHomeDir string

// mcpSetupServerName 是写入 mcpServers 的固定条目键名。
const mcpSetupServerName = "superdev"

// mcpSetupResponse 是 POST /api/mcp-setup/claude-code 的成功响应体。
type mcpSetupResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// setupClaudeCodeMCP 处理 POST /api/mcp-setup/claude-code。
//
// 这是「归属转移」链路里目标机自举的一环：项目转移到本机归属后，转移引擎
// （Task 5）会调用本机自己的这个端点，让本机 agent 把自己写进本机 HOME 的
// claude-code MCP 配置，使 claude-code 之后能通过 superdev-mcp 连接到本机
// agent。调用方实际会带 {project_id,root_path} body（供其自身留痕），这两个
// 字段与「写哪台机器的 HOME」「写哪个端口」无关——本端点恒写本机、恒用本机
// 监听端口，因此完整读取但忽略 body 内容，绝不因为带了 body 就报错。
func (a *App) setupClaudeCodeMCP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
		var ignored map[string]any
		_ = json.NewDecoder(r.Body).Decode(&ignored)
	}

	path, err := mcpSetupConfigPath()
	if err != nil {
		log.Printf("[SuperDev] mcp-setup: 解析本机 HOME 目录失败 err=%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home dir: "+err.Error())
		return
	}

	doc, err := loadMCPSetupDocument(path)
	if err != nil {
		log.Printf("[SuperDev] mcp-setup: 拒绝写入，%s 内容非法 JSON，文件保持不变 err=%v", path, err)
		jsonError(w, http.StatusBadRequest, "existing claude.json is not valid JSON, refusing to overwrite: "+err.Error())
		return
	}

	mergeMCPSetupEntry(doc)

	if err := writeMCPSetupDocumentAtomic(path, doc); err != nil {
		log.Printf("[SuperDev] mcp-setup: 写入 %s 失败 err=%v", path, err)
		jsonError(w, http.StatusInternalServerError, "write claude.json: "+err.Error())
		return
	}

	log.Printf("[SuperDev] mcp-setup: 已写入 claude-code MCP 配置 path=%s", path)
	jsonWrite(w, http.StatusOK, mcpSetupResponse{Status: "installed", Path: path})
}

// mcpSetupConfigPath 返回本机 ~/.claude.json 的完整路径。
func mcpSetupConfigPath() (string, error) {
	home := mcpSetupHomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, ".claude.json"), nil
}

// loadMCPSetupDocument 读取并解析 path 处的 JSON 文档。
//
// 参数：
//   - path: .claude.json 完整路径
//
// 返回：
//   - 解析后的顶层 JSON 对象；文件不存在或为空时返回空骨架 {}，不报错
//   - 文件存在但内容不是合法 JSON 时返回错误——调用方据此判定 400，绝不能
//     吞掉错误后用空骨架顶替，那等于静默清空用户原有配置
func loadMCPSetupDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// mergeMCPSetupEntry 在 doc 中合并写入 mcpServers.superdev 条目，保留其余既有键。
//
// 若 doc["mcpServers"] 已存在但不是对象（用户配置损坏或类型不符预期），直接
// 整体替换为新对象——这种情况下沿用旧值没有意义，覆盖是唯一合理选择。已存在
// 同名 superdev 条目时整体覆盖而非逐字段合并，保证多次调用幂等收敛到同一个
// 结果，而不是新旧字段交织出不可预期的中间态。
//
// env 只含 SUPERDEV_AGENT_URL 一个指向本机 loopback 地址的键——这是红线：
// 该配置文件会被 claude-code 明文读取，绝不能承载 token/密钥。
func mergeMCPSetupEntry(doc map[string]any) {
	serversRaw, exists := doc["mcpServers"]
	servers, isMap := serversRaw.(map[string]any)
	if !exists || !isMap {
		servers = map[string]any{}
	}
	servers[mcpSetupServerName] = map[string]any{
		"command": "superdev-mcp",
		"env": map[string]string{
			// 注意：此处用 DefaultAgentListenPort 是刻意为之——运行中的 agent 未把自身 --addr
			// 端口存入 AppConfig（全代码库共享 57017 假设：superdev-mcp 自身兜底、agent_install_command 亦然）。
			// 若目标机以自定义 --addr 运行，这里写入的 SUPERDEV_AGENT_URL 端口会不对（200 但内容错），
			// 用户需手工修正 ~/.claude.json。根因修复=给 AppConfig 增 ListenPort 并从 main.go 透传，属本切面范围外。
			"SUPERDEV_AGENT_URL": fmt.Sprintf("http://127.0.0.1:%d", model.DefaultAgentListenPort),
		},
	}
	doc["mcpServers"] = servers
}

// writeMCPSetupDocumentAtomic 把 doc 写回 path，临时文件 + rename 原子替换
// （惯例对齐 agent/projecthome/store.go），避免进程崩溃或并发写入截断出半份 JSON。
func writeMCPSetupDocumentAtomic(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
