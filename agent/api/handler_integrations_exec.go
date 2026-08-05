// handler_integrations_exec.go 实现远端编程智能体接入的受限命令执行端点。
//
// 职责：
//   - 处理 POST /api/integrations/exec：在白名单约束下执行一条 CLI 命令，
//     把 exit_code / stdout / stderr / timed_out 回给调用方
//   - 专供桌面端 connector（Rust RemoteAgentCommandRunner）远端安装 OpenClaw /
//     Grok 的 MCP 配置使用——这两家刻意不解析回写自己的配置文件
//
// 边界：
//   - 白名单外一律 403，且**不启动任何进程**；判定全部委托
//     integrations_exec_allowlist.go 的纯函数
//   - 永不经 shell：一律 exec.CommandContext(ctx, absPath, args...)
//   - 不代理到远端机器（那是 proxyAgentIntegrations 的职责）；本端点执行的是
//     「运行本端点这台机器」上的命令
//   - CLI 返回非零**不是** HTTP 错误：调用方需要拿到 exit_code 与 stderr 自己
//     判断，把它翻译成 5xx 会让桌面端丢失连接器方言层要读的信息
//
// 日志纪律：
//   - 每条错误/拒绝分支各打一行带原因的日志
//   - **成功也打一行**：它改变了目标机状态，事后必须能复原「谁在什么时候
//     在这台机器上跑了哪个 CLI 的哪个子命令」
//   - 绝不记录 argv 内容、stdout、stderr：canonical JSON 里含 agent URL，与
//     桌面端 connectors/openclaw.rs 的边界约定保持同一条纪律
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// integrationsExecMaxOutputBytes 是每个输出流保留的最大字节数，与桌面端
// connectors/process.rs 的 MAX_CAPTURED_BYTES 一致。
const integrationsExecMaxOutputBytes = 64 << 10

// integrationsExecResponse 是 exec 端点的响应体。
type integrationsExecResponse struct {
	// ExitCode 是进程退出码；被信号杀死或超时时为 -1。
	ExitCode int `json:"exit_code"`
	// Stdout 是截断后的标准输出。
	Stdout string `json:"stdout"`
	// Stderr 是截断后的标准错误。
	Stderr string `json:"stderr"`
	// TimedOut 表示进程是否因超过时限被终止。
	TimedOut bool `json:"timed_out"`
}

// integrationsExec 处理 POST /api/integrations/exec。
//
// 注意：
//   - 子进程环境是**最小环境 + 白名单 env**，不继承 agent 自己的完整环境：
//     agent 进程里可能有与接入无关的凭据，没有理由传给被调用的 CLI
//   - CLI 返回非零不是 HTTP 错误：调用方要靠 exit_code/stderr 做方言层判断
func (a *App) integrationsExec(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	principal, _, _ := principalFromRequest(r)

	var req integrationsExecRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		log.Printf("[SuperDev] integrations: exec 请求体无法解析 by=%s：%v", principal, err)
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: exec 解析 home 失败 by=%s：%v", principal, err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}

	plan, err := integrationExecAllowed(home, req)
	if err != nil {
		var rejection integrationExecRejection
		if errors.As(err, &rejection) {
			log.Printf("[SuperDev] integrations: exec 被白名单拒绝 program=%s code=%s reason=%s by=%s",
				req.Program, rejection.Code, rejection.Reason, principal)
			jsonCodeError(w, http.StatusForbidden, rejection.Code, "command not allowed", nil)
			return
		}
		log.Printf("[SuperDev] integrations: exec 校验失败 program=%s by=%s：%v", req.Program, principal, err)
		jsonError(w, http.StatusForbidden, "command not allowed")
		return
	}

	// 与 detect 端点共用同一份解析判据，这里报不到就是真的没装——
	// 不能退回 PATH 查找，那会把 launchd 最小环境的差异带进来。
	absPath, ok := integrationCommandResolve(home, plan.Program)
	if !ok {
		log.Printf("[SuperDev] integrations: exec 未找到命令 program=%s by=%s", plan.Program, principal)
		jsonCodeError(w, http.StatusNotFound, "command_not_found", "command not found", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), plan.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, absPath, plan.Args...)
	cmd.Env = integrationsExecChildEnv(home, plan.Env)
	stdout, stderr, exitCode, runErr := runBoundedCommand(cmd)
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)

	if runErr != nil && !timedOut && exitCode < 0 {
		log.Printf("[SuperDev] integrations: exec 启动失败 program=%s args_count=%d by=%s：%v",
			plan.Program, len(plan.Args), principal, runErr)
		jsonError(w, http.StatusInternalServerError, "exec failed")
		return
	}

	// 成功路径也留痕：这条命令改变了本机状态。刻意不记 argv 内容与输出。
	log.Printf("[SuperDev] integrations: 已执行 program=%s subcommand=%s args_count=%d exit=%d timed_out=%t by=%s cost=%s",
		plan.Program, plan.Args[0], len(plan.Args), exitCode, timedOut, principal, time.Since(started))

	jsonOK(w, integrationsExecResponse{
		ExitCode: exitCode,
		Stdout:   truncateOutput(stdout),
		Stderr:   truncateOutput(stderr),
		TimedOut: timedOut,
	})
}

// integrationsExecChildEnv 构造子进程环境：最小必要变量 + 白名单 env。
//
// 参数：
//   - home: 目标机用户 home 绝对路径（integrationsHome 解析的那一个）
//   - allowed: 已过白名单且路径已收敛的环境变量
//
// 返回：
//   - "KEY=VALUE" 形式的环境列表
//
// 注意：
//   - 只传 HOME 与 PATH，其余一律不传：agent 进程里可能有与接入无关的凭据
//   - HOME 取**解析出来的那个 home**，不是 os.Getenv("HOME")。路径白名单、
//     命令解析、子进程 HOME 必须是同一个值——三者取不同来源的话，CLI 会按
//     一个 home 写配置、白名单按另一个 home 判定，是那种平时看不出来、
//     一旦不一致就写到白名单外的错
func integrationsExecChildEnv(home string, allowed map[string]string) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + integrationsExecChildPath(home),
	}
	for key, value := range allowed {
		env = append(env, key+"="+value)
	}
	return env
}

// integrationsExecChildPath 构造子进程的 PATH：agent 自身 PATH + 命令兜底目录。
//
// 参数：
//   - home: 目标机用户 home 绝对路径
//
// 返回：
//   - 用平台分隔符连接、已去重的目录列表
//
// 注意：
//   - 只补不删：agent 自身 PATH 里的目录全部保留且优先级在前，兜底目录追加在后
//   - 为什么必须补：agent 由 launchd / systemd 拉起时 PATH 是最小集（某台
//     macOS 目标机实测就是 `/usr/bin:/bin:/usr/sbin:/sbin`）。
//     integrationCommandResolve 已经靠兜底目录解决了「找不到程序」，但**光解析出
//     绝对路径不够**——被调用的 CLI 自己还要在 PATH 上找解释器与子进程：npm 系
//     CLI 的入口是 `#!/usr/bin/env node` 脚本，node 装在 /opt/homebrew/bin 或
//     ~/.local/bin 这类用户级目录里，最小 PATH 下一律 exit 127。那是与
//     「装了却报没装」同一类的不对称，只是发生在执行期而不是探测期。
//   - 补的正是 integrationCommandSearchDirs 那一份清单：解析程序与解析它的
//     解释器用同一份目录，两者不可能给出不一致的答案
func integrationsExecChildPath(home string) string {
	seen := make(map[string]struct{})
	dirs := make([]string, 0, 16)
	appendDir := func(dir string) {
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		appendDir(dir)
	}
	for _, dir := range integrationCommandSearchDirs(home) {
		appendDir(dir)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// truncateOutput 把输出截断到 integrationsExecMaxOutputBytes。
func truncateOutput(raw string) string {
	if len(raw) <= integrationsExecMaxOutputBytes {
		return raw
	}
	return raw[:integrationsExecMaxOutputBytes]
}

// runBoundedCommand 执行命令并分别捕获两个输出流。
//
// 参数：
//   - cmd: 已配置好 Env 的命令；调用方负责保证它来自白名单
//
// 返回：
//   - stdout、stderr、退出码（异常终止时为 -1）、启动或等待过程中的错误
//
// 注意：
//   - 用标准库 bytes.Buffer 收集，不自造缓冲类型；截断由调用方的
//     truncateOutput 负责，这里保留完整输出以免截断逻辑散落两处
func runBoundedCommand(cmd *exec.Cmd) (string, string, int, error) {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exitCode, err
}
