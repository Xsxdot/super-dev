// remote.go 提供目标机（远端 host）的 git 探测与检出能力。
//
// 职责：
//   - InspectRemote：通过注入的 Runner 在目标机上探测 path 的 git 状态，
//     复用 Snapshot 的字段语义，结果作为项目归属转移预检的远端事实来源
//   - EnsureCheckout：让目标机 path 成为 repoURL 在 branch 分支上的最新检出
//     （目录不存在则 clone，已是同源仓库则 fetch+checkout+pull）
//
// 边界：
//   - 不搬运 git 凭据——clone/fetch/pull 失败即目标机自己没权限访问该仓库，
//     错误原样透出给调用方，交给使用者自行在目标机配置凭据，本包不做任何兜底
//   - 不比对 URL 是否同源：EnsureCheckout 假定调用方已在预检阶段确认 path 处
//     的仓库与 repoURL 同源（含人工复核），这里只管把它开到目标分支的最新提交
//   - 不解析 host 侧输出语义（如 fetch 的 diff 统计），只逐行透传给 onLine，
//     本包自身只在日志里记录步骤边界
package gitinfo

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// CommandResult 是一次远端命令执行的完整结果。
//
// 为什么要有 Stderr：git 把失败原因（Host key verification failed、
// Permission denied、branch not found）全部写在 stderr 上。旧签名只返回
// stdout，导致 checkout 失败时诊断信息里只剩一个 exitCode，排障必须登上
// 目标机手动复现——这正是 2026-08-20 真机验收踩到的坑。
//
// 为什么 Stdout / Stderr 分开而不是合并成一个 Output：探测类命令
// （test -d ... && echo yes || echo no、cd && pwd）靠精确比对 stdout 判断结果，
// shell 往 stderr 写任何一行都会把判断带偏。诊断要合并、判定要分开，
// 所以分开存、由调用方决定怎么用。
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner 在目标机上执行一条 shell 命令。
//
// 契约：
//   - 命令跑完但非零退出不算 error：结果写进 CommandResult.ExitCode，
//     error 返回 nil。这是「探测到的确凿事实」（目录不存在、不是仓库）。
//   - error 非 nil 只表示传输层故障（隧道断、SSH 连不上、超时）。
type Runner func(ctx context.Context, cmd, workDir string) (CommandResult, error)

// RemoteProbe 是目标机 checkout 的探测结果。
type RemoteProbe struct {
	DirExists bool // path 是否存在；false 时 Snapshot 全零值，没有意义
	Snapshot       // 复用 Task 1 的字段语义（IsRepo/Branch/RemoteURL/Dirty/Ahead）
}

// InspectRemote 探测目标机 path 的 git 状态。
//
// 参数：
//   - ctx: 控制每条远端命令的超时/取消
//   - run: 命令执行器，见 Runner 类型注释
//   - path: 目标机上待探测的目录绝对路径
//
// 返回：
//   - RemoteProbe: path 不存在或不是 git 仓库都是「确实如此」的正常结果，
//     分别体现为 DirExists=false 或 IsRepo=false，不是错误
//   - error: 仅当 Runner 报告传输层故障（err 非 nil）时非空，
//     这类基础设施故障不能被误判成「目标机上没这个目录/不是仓库」
//
// 注意：
//   - 字段级降级与 Inspect（本机版本）保持一致：分支/远端/脏状态/领先数
//     任一子命令因 git 语义失败（无上游/无 origin/detached HEAD）单独降级，
//     不让整个探测失败
func InspectRemote(ctx context.Context, run Runner, path string) (RemoteProbe, error) {
	exists, err := probeDirExists(ctx, run, path)
	if err != nil {
		return RemoteProbe{}, err
	}
	if !exists {
		// 目录都不存在，后面的 git 探测无意义，直接返回全零 Snapshot。
		return RemoteProbe{DirExists: false}, nil
	}

	probe := RemoteProbe{DirExists: true}

	isWorkTree, err := runRemoteGit(ctx, run, path, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return RemoteProbe{}, err
	}
	if isWorkTree.ExitCode != 0 || strings.TrimSpace(isWorkTree.Stdout) != "true" {
		// exitCode != 0：目录存在但不是 git 仓库；输出不是 "true"：bare 仓库
		// （同 local.go 的 Inspect 契约——bare 仓库该命令 exit 0 但打印 "false"）。
		// 两种情况都判定 IsRepo=false，其余字段无意义，保持零值。
		return probe, nil
	}
	probe.IsRepo = true

	if branch, err := runRemoteGit(ctx, run, path, "symbolic-ref", "--short", "HEAD"); err != nil {
		return RemoteProbe{}, err
	} else if branch.ExitCode == 0 {
		probe.Branch = strings.TrimSpace(branch.Stdout)
	}
	// detached HEAD 时上面的命令 exitCode != 0，按字段级降级为空分支名，不算错误。

	if remoteURL, err := runRemoteGit(ctx, run, path, "remote", "get-url", "origin"); err != nil {
		return RemoteProbe{}, err
	} else if remoteURL.ExitCode == 0 {
		probe.RemoteURL = strings.TrimSpace(remoteURL.Stdout)
	}
	// 无 origin 时上面的命令 exitCode != 0，按字段级降级为空 RemoteURL。

	if status, err := runRemoteGit(ctx, run, path, "status", "--porcelain"); err != nil {
		return RemoteProbe{}, err
	} else if status.ExitCode == 0 {
		probe.Dirty = strings.TrimSpace(status.Stdout) != ""
	}

	if aheadOut, err := runRemoteGit(ctx, run, path, "rev-list", "--count", "@{upstream}..HEAD"); err != nil {
		return RemoteProbe{}, err
	} else if aheadOut.ExitCode == 0 {
		if ahead, parseErr := strconv.Atoi(strings.TrimSpace(aheadOut.Stdout)); parseErr == nil {
			probe.Ahead = ahead
		} else {
			probe.Ahead = -1
		}
	} else {
		// 无上游配置时该命令 exitCode != 0，降级为 -1（区分「已同步」与「没配上游」，
		// 语义与 local.go 的 Snapshot.Ahead 完全一致）。
		probe.Ahead = -1
	}

	return probe, nil
}

// EnsureCheckout 让目标机 path 成为 repoURL 在 branch 分支上的最新检出：
//   - 目录不存在 → git clone --branch
//   - 已是同源仓库 → fetch + checkout branch + pull --ff-only
//
// 前置（调用方已确认）：同源判定与人工复核在预检阶段完成，本函数不再比对 URL，
// 只管把目标机上的仓库开到目标分支的最新提交。
//
// 参数：
//   - onLine: 每条命令的 host 侧输出会按行回调，供上层写日志/审计；
//     本函数自身只记录步骤边界（开始/结束），不重复记录 host 侧内容
//
// 返回：
//   - 任一步骤非零退出即失败返回；错误信息包含 exitCode 与最后 5 行输出，
//     便于在不重新连接目标机的情况下定位问题
//   - clone/pull 因权限失败（目标机自己没有该仓库的访问权限）时错误原样透出，
//     不做任何兜底或凭据代填——见文件头「不搬运 git 凭据」的边界说明
func EnsureCheckout(ctx context.Context, run Runner, path, repoURL, branch string, onLine func(string)) error {
	exists, err := probeDirExists(ctx, run, path)
	if err != nil {
		log.Printf("[SuperDev][gitinfo] EnsureCheckout 探测目标目录是否存在失败: %v", err)
		return fmt.Errorf("探测目标目录是否存在失败: %w", err)
	}

	if !exists {
		cmd := fmt.Sprintf("git clone --branch %s %s %s", shellQuote(branch), shellQuote(repoURL), shellQuote(path))
		return runCheckoutStep(ctx, run, "clone", cmd, onLine)
	}

	if err := runCheckoutStep(ctx, run, "fetch", fmt.Sprintf("git -C %s fetch origin %s", shellQuote(path), shellQuote(branch)), onLine); err != nil {
		return err
	}
	if err := runCheckoutStep(ctx, run, "checkout", fmt.Sprintf("git -C %s checkout %s", shellQuote(path), shellQuote(branch)), onLine); err != nil {
		return err
	}
	return runCheckoutStep(ctx, run, "pull", fmt.Sprintf("git -C %s pull --ff-only origin %s", shellQuote(path), shellQuote(branch)), onLine)
}

// probeDirExists 探测目标机 path 是否存在（不区分文件/目录以外的其它情况，
// 只用 test -d 语义：存在且是目录才算 true）。
func probeDirExists(ctx context.Context, run Runner, path string) (bool, error) {
	cmd := fmt.Sprintf("test -d %s && echo yes || echo no", shellQuote(path))
	res, err := run(ctx, cmd, "")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) == "yes", nil
}

// runRemoteGit 在目标机上执行一条 `git -C path ...` 命令。只有 path 经 shellQuote
// 转义——args 是本包硬编码的子命令/参数字面量（如 "rev-parse"、"--short"），
// 不是外部输入，转义反而会把字面量的破折号参数拆坏；path 来自项目配置，
// 可能含空格等特殊字符，必须转义。
func runRemoteGit(ctx context.Context, run Runner, path string, args ...string) (CommandResult, error) {
	cmd := "git -C " + shellQuote(path) + " " + strings.Join(args, " ")
	return run(ctx, cmd, "")
}

// runCheckoutStep 执行 EnsureCheckout 的单个步骤：打日志记录开始/结束，
// 把 host 侧输出逐行转发给 onLine，非零退出或传输失败时构造包含
// exitCode 与最后 5 行输出的错误，便于定位。
func runCheckoutStep(ctx context.Context, run Runner, step, cmd string, onLine func(string)) error {
	log.Printf("[SuperDev][gitinfo] EnsureCheckout 步骤开始: %s", step)

	res, err := run(ctx, cmd, "")
	lines := splitLines(res.Stdout)
	// onLine 只接收 stdout：上层把它展示为转移进度文案，stderr 的诊断噪音
	// 应留在失败错误摘要里，避免把 SSH/git 警告伪装成步骤进度。
	for _, line := range lines {
		if onLine != nil {
			onLine(line)
		}
	}

	if err != nil {
		log.Printf("[SuperDev][gitinfo] EnsureCheckout 步骤 %s 执行异常（传输层故障）: %v", step, err)
		return fmt.Errorf("步骤 %s 执行异常: %w", step, err)
	}
	if res.ExitCode != 0 {
		// git 原始输出可能回显仓库自身配置里内嵌的凭据（submodule 里的 token、
		// url.<base>.insteadOf 改写规则），这类凭据不经 api 层捕获点，必须在
		// gitinfo 落日志与返回错误前就地脱敏，详见 redactURLCreds。
		// stderr 排在 stdout 之后：git 的失败原因几乎总在 stderr，取 last N 行时
		// 它必须落在保留窗口里，排前面会被 stdout 的进度行挤掉。
		combined := append(splitLines(res.Stdout), splitLines(res.Stderr)...)
		tail := redactLines(lastNLines(combined, 5))
		log.Printf("[SuperDev][gitinfo] EnsureCheckout 步骤 %s 失败, exitCode=%d, 最后输出: %v", step, res.ExitCode, tail)
		return fmt.Errorf("步骤 %s 失败, exitCode=%d, 最后输出: %s", step, res.ExitCode, strings.Join(tail, "\n"))
	}

	log.Printf("[SuperDev][gitinfo] EnsureCheckout 步骤结束: %s", step)
	return nil
}

// splitLines 按换行切分命令输出，丢弃末尾的空行（TrimRight 掉结尾的 "\n" 后再切分），
// 避免给 onLine 多回调一个空字符串。
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lastNLines 取切片最后 n 行，用于失败时的错误摘要（避免整段输出把日志刷屏）。
func lastNLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// credURLPattern 匹配 http(s) URL 里嵌在 host 之前的 userinfo：`scheme://`
// 之后、第一个 `/` 之前出现的 `...@` 段。`[^/@\s]*` 保证 `@` 必须在路径起始的
// `/` 之前，因此只命中真正嵌在 host 前的凭据，而不会误伤路径里出现的 `@`
// （如 https://host/a@b/c）。同时覆盖两种形态：`user:pass@`（冒号分隔）与裸
// `token@`（无冒号），因为 `[^/@\s]*` 对是否含冒号无所谓。
var credURLPattern = regexp.MustCompile(`(https?://)[^/@\s]*@`)

// redactURLCreds 把文本里 http(s) URL 内嵌的凭据（userinfo）替换成 ***，
// 只保留 scheme 与 host，覆盖 `user:pass@host` 与裸 `token@host` 两种形态。
//
// 为什么 gitinfo 要自带一份而不复用 api.redactCreds：分层约束——api 依赖
// gitinfo（api 侧在 clone 前于捕获点剥离凭据），gitinfo 不能反向 import api，
// 否则形成 import 循环。因此按本包已有 shellQuote 的先例，在包内放一个不导出
// 的小工具，让「gitinfo 落日志前脱敏」这道防线不依赖任何调用方，即便某个
// 未来调用方忘了在上层脱敏，密钥也不会从 gitinfo 层日志/错误漏出。
//
// 注意：本包版本比 api.redactCreds 更全——api 版只命中带冒号的 user:pass@，
// 这里连裸 token@ 也一并抹掉，正对应 git 回显 submodule/insteadOf 凭据的形态。
func redactURLCreds(s string) string {
	return credURLPattern.ReplaceAllString(s, "${1}***@")
}

// redactLines 对每行套用 redactURLCreds，返回一个新切片，不改动入参底层数组
// （lastNLines 返回的是原 lines 的子切片，原地改会污染已回调给 onLine 的数据）。
func redactLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = redactURLCreds(line)
	}
	return out
}

// shellQuote 把字符串包装成单引号 shell 字面量，把内部的单引号替换成转义序列：
//
//	'\''
//
// 为什么需要它：本包所有远端命令都是拼接成字符串交给 Runner 执行的，
// pipeline/ssh_executor.go:144 的 `cd %s && %s` 就是因为 workDir 未转义，
// 一旦路径里带空格或特殊字符就会拼出错误的 shell 命令甚至命令注入。
// 这里的 path/branch/URL 全部来自项目配置或用户输入，同样不可信，
// 必须在每处拼接前统一转义。单引号转义是 POSIX shell 里唯一在所有场景下
// 都安全的写法：单引号字面量内部不解释任何字符（包括 $、反引号、反斜杠），
// 只有单引号自身需要用上面那个转义序列打断字面量、插入一个转义后的单引号、
// 再重新进入字面量。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
