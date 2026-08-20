// gitinfo_test 验证本机 git 状态探测在真实仓库上的行为。
//
// 职责：
//   - 用 t.TempDir() + 真实 git 命令构造仓库场景（非仓库/干净/脏/带上游）
//   - 锁定 Inspect 对外暴露的字段语义，尤其 Ahead=-1 与错误降级契约
//
// 边界：
//   - 不 mock git 命令，全部通过真实 git 二进制驱动，验证的是真实行为而非桩实现
//   - 不测试 git 二进制缺失场景（需要修改 PATH，价值低，跳过）
package gitinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo 在临时目录初始化一个真实 git 仓库并配置 user.name/user.email，
// 避免在缺少全局 git 配置的干净环境（如 CI）里 commit 失败。
// 生成一次初始提交后返回仓库根目录绝对路径。
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("写入初始文件失败: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

// runGit 在指定目录同步执行一条 git 子命令，失败时终止测试并打印 stderr 便于定位。
// 返回 trim 后的标准输出，供断言分支名等实际值使用。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 执行失败: %v\n输出: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestInspect_NotARepo 验证非仓库目录不报错，只是 IsRepo=false。
func TestInspect_NotARepo(t *testing.T) {
	dir := t.TempDir()

	snap, err := Inspect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Inspect 对非仓库目录不应返回 error（应降级为 IsRepo=false）: %v", err)
	}
	if snap.IsRepo {
		t.Fatalf("非 git 目录应判定 IsRepo=false, got %+v", snap)
	}
}

// TestInspect_CleanRepoNoUpstream 验证干净仓库、无上游、无 origin 时的字段取值，
// 尤其 Ahead 必须是 -1（区分「已同步」与「没配上游」），分支名按实际值断言。
func TestInspect_CleanRepoNoUpstream(t *testing.T) {
	dir := initTestRepo(t)
	wantBranch := runGit(t, dir, "symbolic-ref", "--short", "HEAD")

	snap, err := Inspect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	if !snap.IsRepo {
		t.Fatalf("真实仓库应判定 IsRepo=true, got %+v", snap)
	}
	if snap.Branch != wantBranch {
		t.Errorf("Branch = %q, want %q（来自 git symbolic-ref 实际值）", snap.Branch, wantBranch)
	}
	if snap.Dirty {
		t.Errorf("刚提交的干净仓库不应 Dirty=true")
	}
	if snap.Ahead != -1 {
		t.Errorf("无上游时 Ahead 应为 -1（表示「没配上游」）, got %d", snap.Ahead)
	}
	if snap.RemoteURL != "" {
		t.Errorf("未配置 origin 时 RemoteURL 应为空, got %q", snap.RemoteURL)
	}
}

// TestInspect_DirtyAndRemote 验证未跟踪文件触发 Dirty，以及 origin 的 fetch URL 被正确读取。
func TestInspect_DirtyAndRemote(t *testing.T) {
	dir := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("写脏文件失败: %v", err)
	}
	runGit(t, dir, "remote", "add", "origin", "https://example.com/a.git")

	snap, err := Inspect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	if !snap.Dirty {
		t.Errorf("存在未跟踪文件应判定 Dirty=true")
	}
	if snap.RemoteURL != "https://example.com/a.git" {
		t.Errorf("RemoteURL = %q, want https://example.com/a.git", snap.RemoteURL)
	}
}

// TestInspect_AheadWithUpstream 验证配上游后 Ahead 能正确统计领先提交数。
// 用本地 bare 仓库充当 origin，避免测试依赖网络。
func TestInspect_AheadWithUpstream(t *testing.T) {
	dir := initTestRepo(t)

	bareDir := t.TempDir()
	runGit(t, bareDir, "init", "--bare")
	runGit(t, dir, "remote", "add", "origin", bareDir)
	branch := runGit(t, dir, "symbolic-ref", "--short", "HEAD")
	runGit(t, dir, "push", "-u", "origin", branch)

	// push -u 之后本地与上游同步（Ahead 应为 0），再提交一次让它领先 1，
	// 这样能确认 Ahead 统计的是「上游之后的提交数」而不是固定值。
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	runGit(t, dir, "add", "more.txt")
	runGit(t, dir, "commit", "-m", "second commit")

	snap, err := Inspect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Inspect 失败: %v", err)
	}
	if snap.Ahead != 1 {
		t.Errorf("push -u 后本地再提交一次应 Ahead=1, got %d", snap.Ahead)
	}
	if snap.RemoteURL != bareDir {
		t.Errorf("RemoteURL = %q, want %q", snap.RemoteURL, bareDir)
	}
}

// TestInspect_BareRepo 是一个正确性缺陷的回归测试：`git rev-parse --is-inside-work-tree`
// 在 bare 仓库（只有对象库、没有工作区）内同样 exit 0，但 stdout 打印的是 "false"。
// 之前的实现只看 err==nil 就判定 IsRepo=true，会把 bare 仓库误报为普通仓库，
// 导致后续字段（Branch/Dirty/Ahead）在一个根本没有工作区的目录上被强行探测，取值无意义。
func TestInspect_BareRepo(t *testing.T) {
	bareDir := t.TempDir()
	runGit(t, bareDir, "init", "--bare")

	snap, err := Inspect(context.Background(), bareDir)
	if err != nil {
		t.Fatalf("Inspect 对 bare 仓库不应返回 error: %v", err)
	}
	if snap.IsRepo {
		t.Fatalf("bare 仓库没有工作区，应判定 IsRepo=false, got %+v", snap)
	}
}

// TestInspect_ContextCancelled 验证 ctx 取消/超时是基础设施故障，必须能与「确实不是仓库」
// 区分开——调用方（Task 3/4/5 的转移预检）拿 Snapshot 做转移决策，如果把 ctx 超时误判成
// IsRepo=false，会当成「这里根本不是仓库」处理，结论完全错误。因此这种情况必须返回非 nil error，
// 而不是像 git 语义降级（无上游/无 origin/detached HEAD）那样静默体现在字段上。
func TestInspect_ContextCancelled(t *testing.T) {
	dir := initTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Inspect(ctx, dir)
	if err == nil {
		t.Fatalf("已取消的 context 应返回非 nil error，不能被静默降级为 IsRepo=false")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error 应可用 errors.Is 判定为 context.Canceled, got %v", err)
	}
}

// TestListIgnoredEntriesFoldsDirectoriesAndSkipsTracked 钉死
// ListIgnoredEntries 的三条契约：忽略的文件要列出、忽略的目录折叠成一条、
// 被跟踪的文件不出现。
//
// 为什么这条重要：转移靠 git clone 把代码送到目标机，被忽略的文件结构性
// 地留在原地——config.yaml、证书、本地覆盖配置正是这一类，而它们往往是
// 服务能否跑起来的前提。目录折叠则是规模闸门：node_modules 展开会有上万条。
func TestListIgnoredEntriesFoldsDirectoriesAndSkipsTracked(t *testing.T) {
	dir := initTestRepo(t)
	writeIgnoredFixture(t, dir)

	entries, err := ListIgnoredEntries(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListIgnoredEntries 失败: %v", err)
	}

	got := map[string]bool{}
	for _, e := range entries {
		got[e] = true
	}
	if !got["config.yaml"] {
		t.Errorf("被忽略的文件 config.yaml 未列出，实际=%v", entries)
	}
	if !got["node_modules/"] {
		t.Errorf("被忽略的目录应折叠成一条目录项 node_modules/，实际=%v", entries)
	}
	if got["node_modules/pkg/index.js"] {
		t.Errorf("目录折叠后不该再展开其内容，实际=%v", entries)
	}
	if got["tracked.txt"] {
		t.Errorf("被跟踪的文件不该出现在忽略清单里，实际=%v", entries)
	}
}

func writeIgnoredFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("config.yaml\nnode_modules/\n"), 0o644); err != nil {
		t.Fatalf("写 .gitignore 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("api_key: x"), 0o644); err != nil {
		t.Fatalf("写 config.yaml 失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatalf("建 node_modules 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写 index.js 失败: %v", err)
	}
}
