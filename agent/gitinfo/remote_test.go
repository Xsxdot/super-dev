// remote_test.go 验证目标机 git 探测与检出在假 Runner 下的行为。
//
// 职责：
//   - 用按 cmd 前缀匹配的假 Runner 驱动 InspectRemote 的三个分支
//     （目录不存在/存在但非仓库/全命中字段解析）
//   - 用记录命令序列的假 Runner 断言 EnsureCheckout 两条路径
//     （clone vs fetch+checkout+pull）拼出的命令串，以及 shellQuote
//     对含空格与单引号路径的转义是否正确（防命令注入/参数错位）
//
// 边界：
//   - 不起真实进程/真实 SSH，只验证本包的命令拼接与字段解析逻辑，
//     真实远端命令执行的正确性由 Task 4/5 接线后的集成验证覆盖
package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeCall 记录一次假 Runner 收到的调用，供测试断言命令序列。
type fakeCall struct {
	cmd     string
	workDir string
}

// fakeRunner 是测试用的 Runner 假件：按 cmd 前缀匹配 canned 响应，
// 未匹配到任何前缀时测试直接失败，避免用例漏配导致误判通过。
type fakeRunner struct {
	t         *testing.T
	responses []fakeResponse
	calls     []fakeCall
}

type fakeResponse struct {
	prefix  string
	stdout  string
	stderr  string
	exitVal int
	err     error
}

func (f *fakeRunner) run(ctx context.Context, cmd, workDir string) (CommandResult, error) {
	f.calls = append(f.calls, fakeCall{cmd: cmd, workDir: workDir})
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.prefix) {
			return CommandResult{Stdout: r.stdout, Stderr: r.stderr, ExitCode: r.exitVal}, r.err
		}
	}
	f.t.Fatalf("fakeRunner 未配置响应的命令: %q", cmd)
	return CommandResult{}, nil
}

// ---- InspectRemote 三分支 ----

// TestInspectRemote_DirNotExists 验证 test -d 命中 no 分支时，
// 直接返回 DirExists=false 的全零 Snapshot，不再往下探测。
func TestInspectRemote_DirNotExists(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no\n", exitVal: 0},
	}}

	probe, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if err != nil {
		t.Fatalf("目录不存在不应返回 error: %v", err)
	}
	if probe.DirExists {
		t.Fatalf("DirExists 应为 false, got %+v", probe)
	}
	if probe != (RemoteProbe{}) {
		t.Fatalf("目录不存在时应为全零 RemoteProbe, got %+v", probe)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("目录不存在应在 test -d 后立即返回，不应再调用其它命令，实际调用: %+v", fr.calls)
	}
}

// TestInspectRemote_ExistsNotRepo 验证目录存在但 rev-parse 失败（非仓库）时，
// DirExists=true 但 IsRepo=false，且不再继续探测分支/远端等字段。
func TestInspectRemote_ExistsNotRepo(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-parse", stdout: "", exitVal: 128},
	}}

	probe, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if err != nil {
		t.Fatalf("非仓库不应返回 error: %v", err)
	}
	if !probe.DirExists {
		t.Fatalf("目录存在，DirExists 应为 true")
	}
	if probe.IsRepo {
		t.Fatalf("rev-parse 非零退出应判定 IsRepo=false, got %+v", probe)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("非仓库应在 rev-parse 后停止，不应再探测分支等字段，实际调用: %+v", fr.calls)
	}
}

// TestInspectRemote_BareRepo 验证 bare 仓库场景：rev-parse exit 0 但输出 "false"，
// 与 local.go 的 Inspect 保持同款契约，判定 IsRepo=false。
func TestInspectRemote_BareRepo(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-parse", stdout: "false\n", exitVal: 0},
	}}

	probe, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if err != nil {
		t.Fatalf("bare 仓库不应返回 error: %v", err)
	}
	if probe.IsRepo {
		t.Fatalf("bare 仓库应判定 IsRepo=false, got %+v", probe)
	}
}

// TestInspectRemote_AllHit 验证全部命中时字段解析正确：分支、origin、脏状态、领先提交数。
func TestInspectRemote_AllHit(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-parse", stdout: "true\n", exitVal: 0},
		{prefix: "git -C '/srv/app' symbolic-ref", stdout: "main\n", exitVal: 0},
		{prefix: "git -C '/srv/app' remote", stdout: "https://example.com/a.git\n", exitVal: 0},
		{prefix: "git -C '/srv/app' status", stdout: " M file.go\n", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-list", stdout: "3\n", exitVal: 0},
	}}

	probe, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if err != nil {
		t.Fatalf("InspectRemote 失败: %v", err)
	}
	if !probe.DirExists || !probe.IsRepo {
		t.Fatalf("全命中应 DirExists=true, IsRepo=true, got %+v", probe)
	}
	if probe.Branch != "main" {
		t.Errorf("Branch = %q, want main", probe.Branch)
	}
	if probe.RemoteURL != "https://example.com/a.git" {
		t.Errorf("RemoteURL = %q, want https://example.com/a.git", probe.RemoteURL)
	}
	if !probe.Dirty {
		t.Errorf("status --porcelain 非空应判定 Dirty=true")
	}
	if probe.Ahead != 3 {
		t.Errorf("Ahead = %d, want 3", probe.Ahead)
	}
}

// TestInspectRemote_NoUpstreamDegradesAheadToMinusOne 验证无上游时 rev-list 非零退出，
// Ahead 降级为 -1（而不是 0），语义与 local.go 的 Snapshot.Ahead 一致。
func TestInspectRemote_NoUpstreamDegradesAheadToMinusOne(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-parse", stdout: "true\n", exitVal: 0},
		{prefix: "git -C '/srv/app' symbolic-ref", stdout: "main\n", exitVal: 0},
		{prefix: "git -C '/srv/app' remote", stdout: "", exitVal: 1},
		{prefix: "git -C '/srv/app' status", stdout: "", exitVal: 0},
		{prefix: "git -C '/srv/app' rev-list", stdout: "", exitVal: 128},
	}}

	probe, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if err != nil {
		t.Fatalf("InspectRemote 失败: %v", err)
	}
	if probe.Ahead != -1 {
		t.Errorf("无上游应 Ahead=-1, got %d", probe.Ahead)
	}
	if probe.RemoteURL != "" {
		t.Errorf("无 origin 应 RemoteURL 为空, got %q", probe.RemoteURL)
	}
}

// TestInspectRemote_TransportFailurePropagates 验证 Runner 报告传输层故障（err 非 nil）时，
// InspectRemote 必须整体失败上抛，不能被误判成「不是仓库」。
func TestInspectRemote_TransportFailurePropagates(t *testing.T) {
	wantErr := errors.New("ssh: connection refused")
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "", exitVal: 0, err: wantErr},
	}}

	_, err := InspectRemote(context.Background(), fr.run, "/srv/app")
	if !errors.Is(err, wantErr) {
		t.Fatalf("传输层故障应原样上抛, got %v", err)
	}
}

// ---- EnsureCheckout 命令序列与 shell 安全 ----

// TestEnsureCheckout_CloneWhenDirMissing 验证目录不存在时只执行一条 clone 命令，
// 且 branch/repoURL/path 都经过 shellQuote 转义（用含空格与单引号的路径触发转义路径）。
func TestEnsureCheckout_CloneWhenDirMissing(t *testing.T) {
	const path = `/srv/my project's app`
	const repoURL = "https://example.com/a.git"
	const branch = "main"

	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no\n", exitVal: 0},
		{prefix: "git clone", stdout: "Cloning into '...'\ndone\n", exitVal: 0},
	}}

	var lines []string
	err := EnsureCheckout(context.Background(), fr.run, path, repoURL, branch, func(l string) {
		lines = append(lines, l)
	})
	if err != nil {
		t.Fatalf("EnsureCheckout 失败: %v", err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("目录不存在应只有 test -d + clone 两条命令，实际: %+v", fr.calls)
	}

	wantQuotedPath := shellQuote(path)
	cloneCmd := fr.calls[1].cmd
	wantCloneCmd := fmt.Sprintf("git clone --branch %s %s %s", shellQuote(branch), shellQuote(repoURL), wantQuotedPath)
	if cloneCmd != wantCloneCmd {
		t.Fatalf("clone 命令串不匹配:\ngot:  %s\nwant: %s", cloneCmd, wantCloneCmd)
	}
	// 显式断言转义细节：单引号被替换为 '\''，保证含空格与单引号的路径不会拆散参数
	// 或被 shell 当成命令的一部分注入执行。
	wantEscaped := `'/srv/my project'\''s app'`
	if wantQuotedPath != wantEscaped {
		t.Fatalf("shellQuote(path) = %s, want %s", wantQuotedPath, wantEscaped)
	}
	if !strings.Contains(cloneCmd, wantEscaped) {
		t.Fatalf("clone 命令串应包含转义后的路径 %s, got %s", wantEscaped, cloneCmd)
	}
	if len(lines) != 2 || lines[0] != "Cloning into '...'" || lines[1] != "done" {
		t.Fatalf("onLine 应收到 clone 命令的逐行输出, got %+v", lines)
	}
}

// TestEnsureCheckout_FetchCheckoutPullWhenDirExists 验证目录已存在（同源仓库）时
// 依次执行 fetch → checkout → pull --ff-only 三条命令，且都带上 shellQuote 转义。
func TestEnsureCheckout_FetchCheckoutPullWhenDirExists(t *testing.T) {
	const path = `/srv/app`
	const repoURL = "https://example.com/a.git"
	const branch = "release/1.0"

	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' fetch", stdout: "fetched\n", exitVal: 0},
		{prefix: "git -C '/srv/app' checkout", stdout: "switched\n", exitVal: 0},
		{prefix: "git -C '/srv/app' pull", stdout: "up to date\n", exitVal: 0},
	}}

	err := EnsureCheckout(context.Background(), fr.run, path, repoURL, branch, nil)
	if err != nil {
		t.Fatalf("EnsureCheckout 失败: %v", err)
	}

	if len(fr.calls) != 4 {
		t.Fatalf("目录已存在应有 test -d + fetch + checkout + pull 四条命令，实际: %+v", fr.calls)
	}
	wantSeq := []string{
		fmt.Sprintf("git -C %s fetch origin %s", shellQuote(path), shellQuote(branch)),
		fmt.Sprintf("git -C %s checkout %s", shellQuote(path), shellQuote(branch)),
		fmt.Sprintf("git -C %s pull --ff-only origin %s", shellQuote(path), shellQuote(branch)),
	}
	for i, want := range wantSeq {
		got := fr.calls[i+1].cmd
		if got != want {
			t.Fatalf("命令序列第 %d 步不匹配:\ngot:  %s\nwant: %s", i+1, got, want)
		}
	}
}

// TestEnsureCheckout_CloneFailureIncludesExitCodeAndTail 验证 clone 失败时错误信息
// 包含 exitCode 与最后 5 行输出，便于不重连目标机就能定位问题。
func TestEnsureCheckout_CloneFailureIncludesExitCodeAndTail(t *testing.T) {
	longOutput := "l1\nl2\nl3\nl4\nl5\nl6\nfatal: Authentication failed\n"
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no\n", exitVal: 0},
		{prefix: "git clone", stdout: longOutput, exitVal: 128},
	}}

	err := EnsureCheckout(context.Background(), fr.run, "/srv/app", "https://example.com/a.git", "main", nil)
	if err == nil {
		t.Fatalf("clone 失败应返回 error")
	}
	if !strings.Contains(err.Error(), "128") {
		t.Errorf("错误信息应包含 exitCode 128, got %v", err)
	}
	// 只应包含最后 5 行（l2..fatal），不应包含最早的 l1。
	if strings.Contains(err.Error(), "l1") {
		t.Errorf("错误信息不应包含超过最后 5 行的内容, got %v", err)
	}
	if !strings.Contains(err.Error(), "fatal: Authentication failed") {
		t.Errorf("错误信息应包含最后一行输出, got %v", err)
	}
}

// TestRunCheckoutStepIncludesStderr 钉死「命令失败时 stderr 必须进入错误信息」。
//
// 为什么：git 把失败原因（Host key verification failed、Permission denied、
// 分支不存在）全写在 stderr 上。丢掉 stderr 之后错误信息里只剩一个
// exitCode=128，使用者与排障者都无法判断该去修什么。
func TestRunCheckoutStepIncludesStderr(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no"},
		{prefix: "git clone", stderr: "fatal: Host key verification failed.", exitVal: 128},
	}}

	err := EnsureCheckout(context.Background(), fr.run, "/opt/demo", "git@example.com:x/y.git", "main", nil)

	if err == nil {
		t.Fatal("clone 失败应返回 error")
	}
	if !strings.Contains(err.Error(), "Host key verification failed") {
		t.Fatalf("错误信息应包含 stderr 原因, got %v", err)
	}
	if !strings.Contains(err.Error(), "exitCode=128") {
		t.Fatalf("错误信息应包含 exitCode=128, got %v", err)
	}
}

// TestRunCheckoutStepStderrRedacted 钉死 stderr 也要过凭据脱敏。
//
// 为什么单独一条：补回 stderr 的动作本身有反向风险——git 的失败输出常常
// 原样回显带内嵌凭据的 remote URL。修「诊断信息缺失」不能顺手制造
// 「凭据写进日志与数据库」。
func TestRunCheckoutStepStderrRedacted(t *testing.T) {
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no"},
		{prefix: "git clone", stderr: "fatal: could not read from https://user:s3cr3t@example.com/x/y.git", exitVal: 128},
	}}

	err := EnsureCheckout(context.Background(), fr.run, "/opt/demo", "https://example.com/x/y.git", "main", nil)

	if err == nil {
		t.Fatal("clone 失败应返回 error")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("stderr 中的凭据不应出现在错误信息: %v", err)
	}
}

// TestEnsureCheckout_FailureOutputRedactsCredentials 验证 clone 失败路径的错误
// 摘要会脱敏 git 原始输出里回显的凭据（user:pass@ 与裸 token@ 两种形态），
// 只保留 host。git 可能把仓库自身配置里的凭据（submodule 内嵌 token、
// url.<base>.insteadOf 改写）回显进输出，这类凭据不经 api 层捕获点，必须在
// gitinfo 落日志/返回错误前就地脱敏，否则密钥会随 gitinfo 层日志外泄。
func TestEnsureCheckout_FailureOutputRedactsCredentials(t *testing.T) {
	credOutput := "Cloning into 'app'...\n" +
		"fatal: unable to access 'https://user:s3cret@host/org/repo.git/'\n" +
		"fatal: submodule 'https://ghp_baretoken@host/org/sub.git' auth failed\n"
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "no\n", exitVal: 0},
		{prefix: "git clone", stdout: credOutput, exitVal: 128},
	}}

	err := EnsureCheckout(context.Background(), fr.run, "/srv/app", "https://example.com/a.git", "main", nil)
	if err == nil {
		t.Fatalf("clone 失败应返回 error")
	}
	msg := err.Error()
	if strings.Contains(msg, "s3cret") {
		t.Errorf("错误摘要不应包含 user:pass 凭据, got %q", msg)
	}
	if strings.Contains(msg, "ghp_baretoken") {
		t.Errorf("错误摘要不应包含裸 token 凭据, got %q", msg)
	}
	if !strings.Contains(msg, "host/org/repo.git") {
		t.Errorf("脱敏后应保留 host 与路径, got %q", msg)
	}
	if !strings.Contains(msg, "***") {
		t.Errorf("凭据应被替换为 ***, got %q", msg)
	}
}

// TestEnsureCheckout_TransportFailureStopsSequence 验证 fetch 阶段传输层故障时
// 立即中断，不再往下执行 checkout/pull。
func TestEnsureCheckout_TransportFailureStopsSequence(t *testing.T) {
	wantErr := errors.New("ssh: broken pipe")
	fr := &fakeRunner{t: t, responses: []fakeResponse{
		{prefix: "test -d", stdout: "yes\n", exitVal: 0},
		{prefix: "git -C '/srv/app' fetch", stdout: "", exitVal: 0, err: wantErr},
	}}

	err := EnsureCheckout(context.Background(), fr.run, "/srv/app", "https://example.com/a.git", "main", nil)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("传输层故障应原样上抛, got %v", err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("fetch 传输失败后不应再调用 checkout/pull，实际: %+v", fr.calls)
	}
}

// TestRedactURLCreds 直接验证凭据脱敏：同时抹掉 http(s) URL 内嵌的
// user:pass@ 与裸 token@ 两种形态，只保留 scheme 与 host；不误伤无凭据的
// URL，也不误伤路径里出现的 @。
func TestRedactURLCreds(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"user_pass", "fatal: access 'https://user:s3cret@host/org/repo.git/'", "fatal: access 'https://***@host/org/repo.git/'"},
		{"bare_token", "remote: https://ghp_baretoken@host/org/repo.git rejected", "remote: https://***@host/org/repo.git rejected"},
		{"no_creds_untouched", "Cloning from https://host/org/repo.git", "Cloning from https://host/org/repo.git"},
		{"http_scheme", "err https://tok@host/x", "err https://***@host/x"},
		{"at_in_path_untouched", "see https://host/a@b/c", "see https://host/a@b/c"},
		{"both_forms_one_line", "https://user:s3cret@host/a.git and https://ghp_baretoken@host/b.git", "https://***@host/a.git and https://***@host/b.git"},
	}
	for _, c := range cases {
		if got := redactURLCreds(c.in); got != c.want {
			t.Errorf("%s: redactURLCreds(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// 显式钉住「两个密钥都被抹掉、host 保留」，与 finding 描述一致。
	both := redactURLCreds("https://user:s3cret@host/a.git https://ghp_baretoken@host/b.git")
	if strings.Contains(both, "s3cret") || strings.Contains(both, "ghp_baretoken") {
		t.Errorf("两种凭据都应被抹掉, got %q", both)
	}
	if !strings.Contains(both, "host/a.git") || !strings.Contains(both, "host/b.git") {
		t.Errorf("脱敏后应保留 host, got %q", both)
	}
}

// TestShellQuote 直接验证 shellQuote 的转义规则：单引号包裹 + 内部单引号替换为转义序列。
func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"simple", "'simple'"},
		{"has space", "'has space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
