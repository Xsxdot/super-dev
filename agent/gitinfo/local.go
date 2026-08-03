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
//   - Snapshot: 探测结果；git 不存在或 rootPath 非 git 仓库时 IsRepo=false，不算错误
//   - error: 仅在探测流程本身异常（当前实现始终返回 nil，预留给未来扩展）时非空
//
// 注意：
//   - 本方法不打日志（预检高频路径，结果由调用方按需呈现），git 二进制缺失除外
//   - 单条子命令失败按字段级降级，不让整个探测失败（如无上游时 Ahead=-1）
func Inspect(ctx context.Context, rootPath string) (Snapshot, error) {
	if !gitAvailable(ctx) {
		warnMissingGitOnce.Do(func() {
			log.Printf("[SuperDev][gitinfo] 未找到 git 可执行文件，本机 git 状态探测将始终返回 IsRepo=false")
		})
		return Snapshot{IsRepo: false}, nil
	}

	if _, err := runGitCommand(ctx, rootPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		// rootPath 不是 git 仓库（或不存在），按契约降级为 IsRepo=false，不报错。
		return Snapshot{IsRepo: false}, nil
	}

	snap := Snapshot{IsRepo: true}

	// detached HEAD 时 symbolic-ref 会失败，按字段级降级为空分支名。
	if branch, err := runGitCommand(ctx, rootPath, "symbolic-ref", "--short", "HEAD"); err == nil {
		snap.Branch = branch
	}

	// 无 origin 时命令失败，按字段级降级为空 RemoteURL。
	if remoteURL, err := runGitCommand(ctx, rootPath, "remote", "get-url", "origin"); err == nil {
		snap.RemoteURL = remoteURL
	}

	// status --porcelain 契约：非空输出即存在未提交变更（含未跟踪文件）。
	// 只看 stdout 与退出码，stderr 丢弃不进错误。
	if status, err := runGitCommand(ctx, rootPath, "status", "--porcelain"); err == nil {
		snap.Dirty = status != ""
	}

	// 无上游配置时 rev-list 失败，按字段级降级为 -1（见 Ahead 字段注释）。
	if aheadOut, err := runGitCommand(ctx, rootPath, "rev-list", "--count", "@{upstream}..HEAD"); err == nil {
		if ahead, parseErr := strconv.Atoi(aheadOut); parseErr == nil {
			snap.Ahead = ahead
		} else {
			snap.Ahead = -1
		}
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
