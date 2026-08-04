// handler_integrations_detect.go 实现远端接入流程的第一步：detect 端点。
//
// 职责：
//   - 处理 POST /api/integrations/detect，返回桌面端做接入决策所需的三样事实：
//     请求命令列表在本机 PATH 中的存在性、home 目录绝对路径、agent 自身的
//     stdio MCP launch spec（供桌面端写入 claude/codex/... 的 MCP 配置）
//   - agentSelfLaunchSpec 提炼为独立方法，供本文件与测试复用
//
// 边界：
//   - 只读端点，不写任何文件、不做路径白名单校验（那是 Task 4 受限文件端点的
//     职责，integrations_paths.go 提供的白名单纯函数本端点用不到）
//   - 不代理到远端机器（那是 Task 5 的职责）；本端点回答的是「运行本端点的
//     这台机器上有什么」
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// integrationsDetectRequest 是 detect 端点的请求体：待探测的命令名列表。
type integrationsDetectRequest struct {
	Commands []string `json:"commands"`
}

// integrationCommandPattern 是命令名白名单正则：小写字母/数字开头，其后允许
// 小写字母/数字/连字符，长度上限 64。exec.LookPath 本身不会执行任意字符串，
// 但仍需要白名单化拒绝形如 "a b"、混入大写字母等意料之外的输入，防止调用方
// 把本端点当成任意字符串探测通道使用。
var integrationCommandPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// agentLaunchSpec 描述如何在这台机器上把 superdev-agent 自身作为 stdio MCP
// server 启动——远端机器只有 superdev-agent 一个二进制，编程智能体的 MCP
// 配置需要写成「command + args + env(SUPERDEV_AGENT_URL)」，url 由调用方
// （agent_install_command.go 生成的配置或桌面端写入逻辑）负责拼进目标
// connector 的配置文件，本结构体只负责给出这三样原始事实。
type agentLaunchSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

// integrationsDetect 处理 POST /api/integrations/detect。
// 返回远端接入所需的三样事实：CLI 存在性、home 目录、agent 自身 launch spec。
// 只读端点；命令名白名单化校验防注入（虽然 LookPath 不执行，但仍拒绝任意串）。
func (a *App) integrationsDetect(w http.ResponseWriter, r *http.Request) {
	var req integrationsDetectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Commands) > 32 {
		jsonError(w, http.StatusBadRequest, "too many commands")
		return
	}
	presence := make(map[string]bool, len(req.Commands))
	for _, name := range req.Commands {
		if !integrationCommandPattern.MatchString(name) {
			jsonError(w, http.StatusBadRequest, "invalid command name")
			return
		}
		_, err := exec.LookPath(name)
		presence[name] = err == nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	spec, err := a.agentSelfLaunchSpec()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 agent launch spec 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve launch spec failed")
		return
	}
	// 审计日志只放 Principal 的展示名，绝不放 token 值——principalFromRequest
	// 从 withSecurity 注入的 ctx 中推导，本端点必然经过该中间件（不在 bypass
	// 白名单内），故此处必有 Principal。
	name, _, _ := principalFromRequest(r)
	log.Printf("[SuperDev] integrations: detect 完成 commands=%d home 已解析 by=%s", len(presence), name)
	jsonOK(w, map[string]any{"home": home, "commands": presence, "agent": spec})
}

// agentSelfLaunchSpec 解析当前 agent 进程自身作为 stdio MCP server 的
// launch spec。
//
// 返回：
//   - Command: 当前运行的 agent 二进制路径，经 EvalSymlinks 归一（避免安装
//     脚本创建的软链或相对路径污染桌面端后续写入的 MCP 配置）
//   - Args: 固定为 ["mcp"]——对应 Task 1 在 agent/main.go 加的 mcp 子命令
//     分派，`superdev-agent mcp` 即一个完整的 stdio MCP server
//   - URL: 固定 "http://127.0.0.1:" + 当前监听端口。恒为 http 是有意的：
//     本机 loopback 明文豁免监听器（见 server.go Serve 头注释）保证本机
//     明文可用，TLS 只服务跨机链路，不应该按 TLS 配置切换 scheme——否则
//     远端机器上跑的编程智能体 CLI（本身就是运行在这台机器上的本机客户端）
//     反而要为一条 loopback 连接处理自签证书信任问题。
//
// 返回错误：os.Executable 或 EvalSymlinks 失败（理论上罕见，仅在权限异常等
// 极端环境下发生）。
func (a *App) agentSelfLaunchSpec() (agentLaunchSpec, error) {
	exe, err := os.Executable()
	if err != nil {
		return agentLaunchSpec{}, fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return agentLaunchSpec{}, fmt.Errorf("resolve executable symlink: %w", err)
	}
	return agentLaunchSpec{
		Command: resolved,
		Args:    []string{"mcp"},
		URL:     "http://127.0.0.1:" + a.listenPort(),
	}, nil
}

// listenPort 从当前监听地址（Serve 写入的 a.listenAddr）中提取端口号。
//
// 注意：
//   - 解析失败（尚未调用 Serve、或地址格式异常）时回退到 agent 默认端口
//     57017 并打 Warn 日志——detect 端点仍应给出一个可用的 launch spec，
//     不能因为端口解析失败整体 500；57017 与 agent/mcp.ResolveStdioAgentURL
//     的默认值保持一致。
func (a *App) listenPort() string {
	addr := a.currentListenAddr()
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		log.Printf("[SuperDev] integrations: 解析监听端口失败，回退默认端口 57017 addr=%q err=%v", addr, err)
		return "57017"
	}
	return port
}
