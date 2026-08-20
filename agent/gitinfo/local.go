// Package gitinfo 提供本机 git 仓库状态的只读探测。
//
// 职责：
//   - 探测指定目录是否是 git 仓库、当前分支、origin 远端地址、是否有未提交变更、
//     领先上游的提交数
//   - 作为项目归属/转移预检（Task 3/4/5）的唯一 git 事实来源
//
// 边界：
//   - 只读探测，不执行任何写操作，不 push/pull/fetch/checkout
//   - git 不存在或目录非仓库时不报错，降级为 Snapshot.IsRepo=false
//   - 单条子命令失败按字段级降级（如无上游），不让整个探测失败
package gitinfo

import (
	"context"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// warnMissingGitOnce 保证 git 二进制缺失的告警只打一次。Inspect 是预检高频路径
// （可能被多次调用），逐次打印会刷屏，一次性 Warn 足够定位问题。
var warnMissingGitOnce sync.Once

// Snapshot 是一份仓库状态快照，预检的唯一 git 事实来源。
type Snapshot struct {
	IsRepo    bool   // false 时其余字段无意义（非仓库或 git 不可用）
	Branch    string // 当前分支名；detached HEAD 时为空
	RemoteURL string // origin 的 fetch URL；无 origin 时为空
	Dirty     bool   // 有未提交变更（含未跟踪文件）
	// Ahead 领先上游的提交数；无上游时为 -1。
	// 用 -1 而非 0，是为了区分「已与上游同步（0）」与「根本没配上游（-1）」，
	// 这两种状态对转移预检的含义完全不同。
	Ahead int
}

// Inspect 在本机 rootPath 上执行 git 探测。
//
// 参数：
//   - ctx: 控制探测过程中每条子命令的超时/取消
//   - rootPath: 待探测的本地目录绝对路径
//
// 返回：
//   - Snapshot: 探测结果；git 不存在、rootPath 非 git 仓库、或是 bare 仓库（无工作区）时
//     IsRepo=false，这些都是「确实如此」的语义，不算错误
//   - error: 仅当 ctx 被取消/超时时非空。这类基础设施故障必须能被调用方（Task 3/4/5 的转移预检）
//     与「确实不是仓库/没配上游」区分开——否则超时会被误判成「不是仓库」，做出错误的转移决策
//
// 注意：
//   - 本方法不打日志（预检高频路径，结果由调用方按需呈现），git 二进制缺失除外
//   - 子命令因 git 语义失败（无上游/无 origin/detached HEAD）按字段级降级，不让整个探测失败；
//     但子命令因 ctx 取消/超时失败时立即中断并把 ctx 错误原样上抛，不静默降级
func Inspect(ctx context.Context, rootPath string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		// 调用方传入的 ctx 在探测开始前就已取消/超时：不做任何探测直接上抛，
		// 避免被后面的逻辑误吞成 IsRepo=false（那是「确实不是仓库」的语义，含义完全不同）。
		return Snapshot{}, err
	}

	if !gitAvailable(ctx) {
		warnMissingGitOnce.Do(func() {
			log.Printf("[SuperDev][gitinfo] 未找到 git 可执行文件，本机 git 状态探测将始终返回 IsRepo=false")
		})
		return Snapshot{IsRepo: false}, nil
	}

	isWorkTree, err := runGitCommand(ctx, rootPath, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// 子命令因 ctx 取消/超时而失败：这是基础设施故障，不是「非仓库」，必须上抛，
			// 不能被下面一行的降级分支吞掉。
			return Snapshot{}, ctxErr
		}
		// rootPath 不是 git 仓库（或不存在），按契约降级为 IsRepo=false，不报错。
		return Snapshot{IsRepo: false}, nil
	}
	if isWorkTree != "true" {
		// bare 仓库（只有对象库、没有工作区）该命令同样 exit 0，但 stdout 是 "false"。
		// 只有真正处于工作区内才算 IsRepo=true，否则后续字段（分支/脏状态）无意义。
		return Snapshot{IsRepo: false}, nil
	}

	snap := Snapshot{IsRepo: true}

	// detached HEAD 时 symbolic-ref 会失败，按字段级降级为空分支名（git 语义失败，非基础设施故障）。
	if branch, err := runGitCommand(ctx, rootPath, "symbolic-ref", "--short", "HEAD"); err == nil {
		snap.Branch = branch
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, ctxErr
	}

	// 无 origin 时命令失败，按字段级降级为空 RemoteURL。
	if remoteURL, err := runGitCommand(ctx, rootPath, "remote", "get-url", "origin"); err == nil {
		snap.RemoteURL = remoteURL
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, ctxErr
	}

	// status --porcelain 契约：非空输出即存在未提交变更（含未跟踪文件）。
	// 只看 stdout 与退出码，stderr 丢弃不进错误。
	if status, err := runGitCommand(ctx, rootPath, "status", "--porcelain"); err == nil {
		snap.Dirty = status != ""
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, ctxErr
	}

	// 无上游配置时 rev-list 失败，按字段级降级为 -1（见 Ahead 字段注释）。
	if aheadOut, err := runGitCommand(ctx, rootPath, "rev-list", "--count", "@{upstream}..HEAD"); err == nil {
		if ahead, parseErr := strconv.Atoi(aheadOut); parseErr == nil {
			snap.Ahead = ahead
		} else {
			snap.Ahead = -1
		}
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, ctxErr
	} else {
		snap.Ahead = -1
	}

	return snap, nil
}

// gitAvailable 检查本机 PATH 上是否存在 git 可执行文件。
func gitAvailable(_ context.Context) bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// runGitCommand 执行一条 git 子命令并返回 TrimSpace 后的 stdout。
// stderr 被丢弃不计入错误信息，因为调用方只依赖退出码 + stdout 的 porcelain 契约判断成败。
func runGitCommand(ctx context.Context, rootPath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", rootPath}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ListIgnoredEntries 返回 rootPath 下被 git 忽略的条目（相对路径）。
//
// 参数：
//   - ctx: 请求上下文
//   - rootPath: 仓库根目录
//
// 返回：
//   - 条目列表：被忽略的整个目录折叠成一条以 "/" 结尾的目录项（--directory），
//     其余为文件相对路径；仓库不可用或 git 执行失败时返回错误
//
// 注意：
//   - 目录折叠是刻意的：node_modules/、target/ 这类目录动辄上万个文件，
//     逐条展开对调用方毫无用处，也会让输出规模失控。调用方按结尾的 "/"
//     区分目录项与文件项。
//   - 输出不含被 git 跟踪的文件——只回答「git 不会带走哪些东西」。
func ListIgnoredEntries(ctx context.Context, rootPath string) ([]string, error) {
	out, err := runGitCommand(ctx, rootPath, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	entries := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries, nil
}
