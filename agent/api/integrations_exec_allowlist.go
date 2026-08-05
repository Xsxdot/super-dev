// integrations_exec_allowlist.go 提供受限命令执行端点的白名单校验纯函数。
//
// 职责：
//   - 静态持有「哪个 program 的哪个子命令可以被远端调用」的白名单
//   - 静态持有可透传的环境变量 key 白名单，并把值交给 integrationPathAllowed 收敛
//   - 校验 args 条数、单参长度、timeout 区间等边界，形成可直接执行的 plan
//
// 边界：
//   - 纯函数（除 integrationPathAllowed 内部的 Lstat/EvalSymlinks 外无 I/O），
//     不启动任何进程——执行是 handler_integrations_exec.go 的职责
//   - 白名单是**服务端静态常量**，绝不接受调用方下发或运行时扩展
//   - 只校验「形状」：program 与首个子命令。完整 argv 长什么样是桌面端 Rust
//     连接器的方言知识，本文件刻意不知道，也不应该知道——把方言复制到 Go 会
//     让同一件事有两个真相源，改一次 argv 要动两栈
package api

import (
	"fmt"
	"time"
)

// integrationsExecMaxArgs 是单次调用允许的参数条数上限。
const integrationsExecMaxArgs = 16

// integrationsExecMaxArgBytes 是单个参数的字节上限。
// 取 64 KiB 是因为 openclaw mcp set 的 canonical JSON 要整个作为一个参数传入。
const integrationsExecMaxArgBytes = 64 << 10

// integrationsExecDefaultTimeout 是调用方不指定时的等待时限，与桌面端
// CommandSpec::new 的默认值一致。
const integrationsExecDefaultTimeout = 30 * time.Second

// integrationsExecMaxTimeout 是允许的最长等待时限。
const integrationsExecMaxTimeout = 60 * time.Second

// integrationsExecAllowlist 是「program → 允许的首个子命令集合」白名单。
//
// 为什么只钉到首个子命令：与受限文件端点同构——fs 端点管「能写哪些路径」、
// 不管写什么内容；exec 端点管「能跑哪个程序的哪个子命令」、不管具体参数。
// 再往细钉（完整 argv 模板）就等于把连接器方言下沉到 Go，与「方言 Rust 单源」
// 相冲突。
//
// 新增一家需要 CLI 写配置的连接器时，在此加一行数据；同时必须更新跨栈清单
// testdata/desktop-connector-commands.txt 与
// TestIntegrationsExecAllowlistMatchesDesktopFixture。
var integrationsExecAllowlist = map[string]map[string]struct{}{
	"openclaw": {"mcp": {}},
	"grok":     {"mcp": {}},
}

// integrationsExecEnvAllowlist 是允许透传给子进程的环境变量 key。
//
// 与桌面端 ConnectorEnvironment 的三个字段一一对应：这三个变量决定对应 CLI
// 去读写哪个配置文件，不传的话 agent 进程（launchd 最小环境）会让 CLI 落到
// 默认路径上——而且复核会通过，用户在自己 shell 里却用不上，是典型的静默说谎。
var integrationsExecEnvAllowlist = map[string]struct{}{
	"OPENCLAW_CONFIG_PATH": {},
	"OPENCODE_CONFIG":      {},
	"KIMI_CODE_HOME":       {},
}

// integrationsExecRequest 是 exec 端点的请求体。
type integrationsExecRequest struct {
	// Program 是命令名而非路径：解析目标机上的绝对路径是 agent 的职责。
	Program string `json:"program"`
	// Args 是完整参数列表；Args[0] 必须落在该 program 的子命令白名单里。
	Args []string `json:"args"`
	// Env 是要额外注入子进程的环境变量，key 与值都受白名单约束。
	Env map[string]string `json:"env"`
	// TimeoutMs 是等待时限；0 表示用默认值。
	TimeoutMs int `json:"timeout_ms"`
}

// integrationExecPlan 是校验通过后可直接执行的调用规格。
type integrationExecPlan struct {
	// Program 仍是命令名，绝对路径由 handler 用 integrationCommandResolve 解析。
	Program string
	// Args 是原样透传的参数列表。
	Args []string
	// Env 的值已被 integrationPathAllowed 收敛，不再是调用方声称的原始串。
	Env map[string]string
	// Timeout 是已经过区间校验的等待时限。
	Timeout time.Duration
}

// integrationExecRejection 是带稳定错误码的拒绝原因。
type integrationExecRejection struct {
	// Code 是稳定、可分类的拒绝代码，用于 HTTP 响应体与日志。
	Code string
	// Reason 是给日志的可读说明；不含调用方传入的原始参数内容。
	Reason string
}

// Error 让 integrationExecRejection 满足 error 接口。
func (r integrationExecRejection) Error() string {
	return fmt.Sprintf("%s: %s", r.Code, r.Reason)
}

// integrationExecAllowed 校验一次 exec 请求，返回可执行的调用规格。
//
// 参数：
//   - home: 目标机用户 home 绝对路径
//   - req: 调用方请求体
//
// 返回：
//   - 通过时返回 plan，Env 的值已被路径白名单收敛
//   - 不通过时返回 integrationExecRejection，Code 为稳定拒绝码
//
// 注意：
//   - 校验顺序刻意是「program → 子命令 → 边界 → env」：先否决整类不该出现的
//     调用，再花代价做 env 的路径解析（那一步会碰磁盘）
//   - 本函数不加日志：拒绝原因经 Code 返回给 handler，由 handler 统一落日志；
//     在这里也打一遍会造成同一次拒绝两行日志
func integrationExecAllowed(home string, req integrationsExecRequest) (integrationExecPlan, error) {
	subcommands, ok := integrationsExecAllowlist[req.Program]
	if !ok {
		return integrationExecPlan{}, integrationExecRejection{
			Code:   "program_not_allowed",
			Reason: "program 不在白名单内",
		}
	}
	if len(req.Args) == 0 {
		return integrationExecPlan{}, integrationExecRejection{
			Code:   "subcommand_not_allowed",
			Reason: "缺少子命令",
		}
	}
	if _, ok := subcommands[req.Args[0]]; !ok {
		return integrationExecPlan{}, integrationExecRejection{
			Code:   "subcommand_not_allowed",
			Reason: "子命令不在该 program 的白名单内",
		}
	}
	if len(req.Args) > integrationsExecMaxArgs {
		return integrationExecPlan{}, integrationExecRejection{
			Code:   "args_too_many",
			Reason: "参数条数超过上限",
		}
	}
	for _, arg := range req.Args {
		if len(arg) > integrationsExecMaxArgBytes {
			return integrationExecPlan{}, integrationExecRejection{
				Code:   "arg_too_long",
				Reason: "单个参数超过长度上限",
			}
		}
	}

	timeout := integrationsExecDefaultTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout > integrationsExecMaxTimeout {
			return integrationExecPlan{}, integrationExecRejection{
				Code:   "timeout_out_of_range",
				Reason: "timeout 超过上限",
			}
		}
	}

	env := make(map[string]string, len(req.Env))
	for key, value := range req.Env {
		if _, ok := integrationsExecEnvAllowlist[key]; !ok {
			return integrationExecPlan{}, integrationExecRejection{
				Code:   "env_key_not_allowed",
				Reason: "环境变量 key 不在白名单内",
			}
		}
		// 值必须过与文件端点同一份路径白名单：这三个变量指向的都是智能体配置
		// 文件，让它们指到白名单外等于绕开文件端点的全部约束。
		resolved, err := integrationPathAllowed(home, value)
		if err != nil {
			return integrationExecPlan{}, integrationExecRejection{
				Code:   "env_value_not_allowed",
				Reason: "环境变量值不在路径白名单内",
			}
		}
		env[key] = resolved
	}

	return integrationExecPlan{
		Program: req.Program,
		Args:    req.Args,
		Env:     env,
		Timeout: timeout,
	}, nil
}
