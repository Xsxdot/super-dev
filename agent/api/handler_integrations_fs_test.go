// handler_integrations_fs_test.go 覆盖受限文件六端点（Task 4）。
//
// 职责：
//   - 验证 stat/read/list/write/rename/write-batch/delete 的成功路径契约字段
//   - 验证每个端点至少一条白名单外 403 用例，覆盖 `..` 逃逸与符号链接逃逸
//   - 验证 write-batch 的文件数/总字节上限、rel_path 格式校验（含
//     integrationRelPathSafe 纯函数的跨平台表驱动测试，覆盖 Windows 专属的
//     反斜杠/盘符逃逸构造——这些构造在 macOS 上跑 filepath.Join 不会重现，
//     所以直接测纯函数而不是只走 HTTP 层）
//   - 验证 write 对已存在文件的权限位保留（不悄悄放宽）
//   - 验证 delete 窄白名单（仅 skills/superdev* 放行）
//   - 验证匿名请求 401（鉴权红线，任选一端点覆盖即可，其余端点共用同一中间件）
//
// 边界：
//   - 不覆盖 Task 5 的跨机代理，那不属于本文件范围
//   - home 全部经 App.integrationsHomeOverride 注入 t.TempDir()，不触达开发机
//     真实 home 目录
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newIntegrationsFsTestApp 创建一个 home 指向 t.TempDir() 的测试 App，避免受限
// 文件端点的测试触达运行测试的开发机真实 home 目录。
func newIntegrationsFsTestApp(t *testing.T) (*App, string) {
	t.Helper()
	app := newTestAppForPackage(t)
	home := t.TempDir()
	app.integrationsHomeOverride = home
	return app, home
}

// fsQuery 拼出形如 "/api/integrations/fs/stat?path=..." 的请求路径，负责
// URL 转义，避免测试里因为手写转义漏字符导致假阳性/假阴性。
func fsQuery(endpoint, path string) string {
	v := url.Values{}
	v.Set("path", path)
	return endpoint + "?" + v.Encode()
}

func doWriteRequest(t *testing.T, app *App, path, content string, backup bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"path": path, "content": content, "backup": backup})
	require.NoError(t, err)
	return httptestDo(t, app, http.MethodPut, "/api/integrations/fs/write", bytes.NewReader(body))
}

// integrationsFsWriteBatchFileForTest 镜像请求体里单个文件项，供测试构造 body。
type integrationsFsWriteBatchFileForTest struct {
	RelPath    string `json:"rel_path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
}

func doWriteBatchRequest(t *testing.T, app *App, dir string, files []integrationsFsWriteBatchFileForTest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"dir": dir, "files": files})
	require.NoError(t, err)
	return httptestDo(t, app, http.MethodPut, "/api/integrations/fs/write-batch", bytes.NewReader(body))
}

// TestIntegrationsFsWriteReadStatListRoundTrip 覆盖 brief Step 1 的核心往返场景：
// write 一个文件 → read 读回内容一致 → stat 报告 exists/is_dir/size 正确 →
// list 能在父目录下枚举到这个相对路径。
func TestIntegrationsFsWriteReadStatListRoundTrip(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "skills", "superdev", "SKILL.md")

	writeResp := doWriteRequest(t, app, target, "hello skill", false)
	require.Equal(t, http.StatusOK, writeResp.Code, writeResp.Body.String())
	var writeOut struct {
		BackupPath string `json:"backup_path"`
	}
	require.NoError(t, json.Unmarshal(writeResp.Body.Bytes(), &writeOut))
	require.Empty(t, writeOut.BackupPath, "未要求备份时 backup_path 必须为空")

	readResp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/read", target), nil)
	require.Equal(t, http.StatusOK, readResp.Code)
	var readOut struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(readResp.Body.Bytes(), &readOut))
	require.Equal(t, "hello skill", readOut.Content)

	statResp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/stat", target), nil)
	require.Equal(t, http.StatusOK, statResp.Code)
	var statOut struct {
		Exists bool  `json:"exists"`
		IsDir  bool  `json:"is_dir"`
		Size   int64 `json:"size"`
	}
	require.NoError(t, json.Unmarshal(statResp.Body.Bytes(), &statOut))
	require.True(t, statOut.Exists)
	require.False(t, statOut.IsDir)
	require.Equal(t, int64(len("hello skill")), statOut.Size)

	listResp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/list", filepath.Join(home, ".claude", "skills", "superdev")), nil)
	require.Equal(t, http.StatusOK, listResp.Code)
	var listOut struct {
		Files []string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(listResp.Body.Bytes(), &listOut))
	require.Equal(t, []string{"SKILL.md"}, listOut.Files)
}

// TestIntegrationsFsStatReportsNotExists 覆盖 stat 对不存在路径的响应：exists=false。
func TestIntegrationsFsStatReportsNotExists(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "does-not-exist.json")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/stat", target), nil)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Exists bool `json:"exists"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.False(t, out.Exists)
}

// TestIntegrationsFsStatRejectsPathOutsideWhitelist 覆盖 stat 的白名单外 403。
func TestIntegrationsFsStatRejectsPathOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".ssh", "authorized_keys")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/stat", target), nil)
	require.Equal(t, http.StatusForbidden, resp.Code)
	require.Contains(t, resp.Body.String(), "path_not_allowed")
}

// TestIntegrationsFsReadReturnsExistsFalseWhenMissing 覆盖 read 对不存在文件
// 返回 200 + {exists:false}，而不是 404——brief 契约明确写了这一点。
func TestIntegrationsFsReadReturnsExistsFalseWhenMissing(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "missing.json")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/read", target), nil)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Exists bool `json:"exists"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.False(t, out.Exists)
}

// TestIntegrationsFsReadRejectsOversizedContent 覆盖 read 对 >1MB 内容返回 413。
func TestIntegrationsFsReadRejectsOversizedContent(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	target := filepath.Join(dir, "big.json")
	big := bytes.Repeat([]byte("a"), (1<<20)+1)
	require.NoError(t, os.WriteFile(target, big, 0o644))

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/read", target), nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
}

// TestIntegrationsFsReadRejectsPathOutsideWhitelist 覆盖 read 的白名单外 403。
func TestIntegrationsFsReadRejectsPathOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, "Documents", "secret.txt")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/read", target), nil)
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationsFsReadRejectsDotDotEscape 覆盖 `..` 逃逸：candidate 字面路径
// 经 Clean 后跳出白名单根，必须 403，而不是巧合地仍落在根内。
func TestIntegrationsFsReadRejectsDotDotEscape(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "..", "..", "etc-like-outside.txt")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/read", target), nil)
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationsFsListReturnsEmptyWhenDirMissing 覆盖 list 对不存在目录返回
// {files:[]}，而不是错误——skill 尚未安装是常见路径。
func TestIntegrationsFsListReturnsEmptyWhenDirMissing(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "skills", "not-installed-yet")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/list", target), nil)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Files []string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Empty(t, out.Files)
}

// TestIntegrationsFsListSortsAndUsesForwardSlashes 覆盖多层目录场景下相对路径
// 用正斜杠归一并排序输出。
func TestIntegrationsFsListSortsAndUsesForwardSlashes(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	root := filepath.Join(home, ".claude", "skills", "superdev")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "hooks", "session-start"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0o644))

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/list", root), nil)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Files []string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, []string{"AGENTS.md", "SKILL.md", "hooks/session-start"}, out.Files)
}

// TestIntegrationsFsListRejectsTooManyEntries 覆盖 list >1000 条返回 413。
func TestIntegrationsFsListRejectsTooManyEntries(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	root := filepath.Join(home, ".claude", "skills", "huge")
	require.NoError(t, os.MkdirAll(root, 0o755))
	for i := 0; i < 1001; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, "f"+strconv.Itoa(i)+".txt"), []byte("x"), 0o644))
	}

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/list", root), nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
}

// TestIntegrationsFsListRejectsPathOutsideWhitelist 覆盖 list 的白名单外 403。
func TestIntegrationsFsListRejectsPathOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, "Downloads")

	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/list", target), nil)
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationsFsWriteBackupNamingMatchesDesktopConvention 覆盖备份命名与
// 桌面端 Rust backup_path() 的跨语言契约：settings.json → settings.json.superdev-bak。
func TestIntegrationsFsWriteBackupNamingMatchesDesktopConvention(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "settings.json")

	// 先写一版初始内容，backup 才有东西可备份（brief：backup 仅在目标已存在时生效）。
	first := doWriteRequest(t, app, target, `{"v":1}`, false)
	require.Equal(t, http.StatusOK, first.Code)

	second := doWriteRequest(t, app, target, `{"v":2}`, true)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var out struct {
		BackupPath string `json:"backup_path"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &out))
	require.Equal(t, "settings.json.superdev-bak", filepath.Base(out.BackupPath))

	backupContent, err := os.ReadFile(out.BackupPath)
	require.NoError(t, err)
	require.Equal(t, `{"v":1}`, string(backupContent), "备份文件必须是写入前的旧内容")

	newContent, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, `{"v":2}`, string(newContent))
}

// TestIntegrationsFsWriteBackupNoExtension 覆盖无扩展名文件的备份命名：
// <name>.superdev-bak（不带中间的扩展名段）。
func TestIntegrationsFsWriteBackupNoExtension(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "CLAUDE")

	require.Equal(t, http.StatusOK, doWriteRequest(t, app, target, "v1", false).Code)
	resp := doWriteRequest(t, app, target, "v2", true)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		BackupPath string `json:"backup_path"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "CLAUDE.superdev-bak", filepath.Base(out.BackupPath))
}

// TestIntegrationsFsWriteSkipsBackupWhenTargetMissing 覆盖 backup=true 但目标
// 尚不存在时，不产生 backup_path（没有旧内容可备份）。
func TestIntegrationsFsWriteSkipsBackupWhenTargetMissing(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "fresh.json")

	resp := doWriteRequest(t, app, target, `{"a":1}`, true)
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		BackupPath string `json:"backup_path"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Empty(t, out.BackupPath)
}

// TestIntegrationsFsWriteRejectsOversizedContent 覆盖 write 对 >1MB content 返回 413。
func TestIntegrationsFsWriteRejectsOversizedContent(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "big.json")
	big := strings.Repeat("a", (1<<20)+1)

	resp := doWriteRequest(t, app, target, big, false)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
}

// TestIntegrationsFsWriteRejectsPathOutsideWhitelist 覆盖 write 的白名单外 403。
func TestIntegrationsFsWriteRejectsPathOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".ssh", "authorized_keys")

	resp := doWriteRequest(t, app, target, "pwned", false)
	require.Equal(t, http.StatusForbidden, resp.Code)
	require.Contains(t, resp.Body.String(), "path_not_allowed")
}

// TestIntegrationsFsWriteRejectsSymlinkEscape 覆盖符号链接逃逸：白名单根内一个
// 指向 home 之外的符号链接，写入其下的文件必须仍然 403，而不是借道成功写到
// 白名单根之外的真实目标。
func TestIntegrationsFsWriteRejectsSymlinkEscape(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, ".claude", "escape")))

	target := filepath.Join(home, ".claude", "escape", "pwned.txt")
	resp := doWriteRequest(t, app, target, "pwned", false)
	require.Equal(t, http.StatusForbidden, resp.Code)

	_, statErr := os.Stat(filepath.Join(outside, "pwned.txt"))
	require.True(t, os.IsNotExist(statErr), "符号链接逃逸必须被拦住，真实目标不应被写入")
}

// TestIntegrationsFsWriteAtomicNoTempFileLeftBehind 覆盖原子写不会在目标目录
// 残留临时文件。
func TestIntegrationsFsWriteAtomicNoTempFileLeftBehind(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude")
	target := filepath.Join(dir, "atomic.json")

	resp := doWriteRequest(t, app, target, "content", false)
	require.Equal(t, http.StatusOK, resp.Code)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "目标目录只应留下最终文件，不应残留 tmp 文件")
	require.Equal(t, "atomic.json", entries[0].Name())
}

// TestIntegrationsFsWritePreservesExistingFileMode 覆盖评审 Important#1：
// 目标文件已存在时，写入不能悄悄把它的权限位放宽到硬编码的 0o644——例如用户
// 手工把 ~/.claude/settings.json 收紧成 0600（其中可能有为其它 MCP server
// 配置的 API key），一次远端写入不该把它放宽回全局可读。
func TestIntegrationsFsWritePreservesExistingFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位语义")
	}
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude")
	target := filepath.Join(dir, "settings.json")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(target, []byte(`{"v":1}`), 0o600))

	resp := doWriteRequest(t, app, target, `{"v":2}`, false)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "已存在文件的权限位必须原样保留，不能被写入放宽")

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, `{"v":2}`, string(content))
}

// TestIntegrationsFsWriteNewFileDefaultsTo0644 覆盖新建文件（目标此前不存在）
// 仍然使用 0o644 默认权限——权限保留逻辑只对「已存在」的目标生效，不影响
// 新建文件的既有行为。
func TestIntegrationsFsWriteNewFileDefaultsTo0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位语义")
	}
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "fresh-mode.json")

	resp := doWriteRequest(t, app, target, `{"a":1}`, false)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "新建文件必须使用 0o644 默认权限")
}

// TestIntegrationsFsRenameMovesFile 覆盖 rename 成功路径。
func TestIntegrationsFsRenameMovesFile(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	from := filepath.Join(home, ".claude", "skills", "superdev")
	to := filepath.Join(home, ".claude", "skills", "superdev.superdev-bak-1")
	require.NoError(t, os.MkdirAll(from, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(from, "SKILL.md"), []byte("x"), 0o644))

	body, err := json.Marshal(map[string]string{"from": from, "to": to})
	require.NoError(t, err)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/fs/rename", bytes.NewReader(body))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	_, statErr := os.Stat(from)
	require.True(t, os.IsNotExist(statErr))
	_, err = os.Stat(filepath.Join(to, "SKILL.md"))
	require.NoError(t, err)
}

// TestIntegrationsFsRenameRejectsWhenFromOutsideWhitelist 覆盖 rename 的
// from/to 双校验：from 在白名单外必须 403，即便 to 合法。
func TestIntegrationsFsRenameRejectsWhenFromOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	from := filepath.Join(home, ".ssh", "id_rsa")
	to := filepath.Join(home, ".claude", "stolen")

	body, err := json.Marshal(map[string]string{"from": from, "to": to})
	require.NoError(t, err)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/fs/rename", bytes.NewReader(body))
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationsFsRenameRejectsWhenToOutsideWhitelist 覆盖 rename 的
// from/to 双校验：to 在白名单外必须 403，即便 from 合法——只校验一侧会让另一侧
// 成为越权写入的后门。
func TestIntegrationsFsRenameRejectsWhenToOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	from := filepath.Join(home, ".claude", "skills", "superdev")
	require.NoError(t, os.MkdirAll(from, 0o755))
	to := filepath.Join(home, ".ssh", "authorized_keys")

	body, err := json.Marshal(map[string]string{"from": from, "to": to})
	require.NoError(t, err)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/fs/rename", bytes.NewReader(body))
	require.Equal(t, http.StatusForbidden, resp.Code)

	_, statErr := os.Stat(from)
	require.NoError(t, statErr, "校验失败时不应发生任何改名")
}

// TestIntegrationsFsWriteBatchWritesAllFilesWithModes 覆盖 write-batch 成功路径：
// 多个文件落盘、内容正确、executable 决定权限位。
func TestIntegrationsFsWriteBatchWritesAllFilesWithModes(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")

	resp := doWriteBatchRequest(t, app, dir, []integrationsFsWriteBatchFileForTest{
		{RelPath: "SKILL.md", Content: "skill body"},
		{RelPath: "hooks/session-start", Content: "#!/bin/sh\necho hi\n", Executable: true},
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var out struct {
		Written []string `json:"written"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.ElementsMatch(t, []string{"SKILL.md", "hooks/session-start"}, out.Written)

	skillContent, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "skill body", string(skillContent))

	hookInfo, err := os.Stat(filepath.Join(dir, "hooks", "session-start"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), hookInfo.Mode().Perm(), "executable=true 必须落地为 0755")

	skillInfo, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), skillInfo.Mode().Perm(), "非 executable 必须落地为 0644")
}

// TestIntegrationsFsWriteBatchRejectsTooManyFiles 覆盖 batch 上限：101 个文件 → 400。
func TestIntegrationsFsWriteBatchRejectsTooManyFiles(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")

	files := make([]integrationsFsWriteBatchFileForTest, 101)
	for i := range files {
		files[i] = integrationsFsWriteBatchFileForTest{RelPath: "f" + strconv.Itoa(i) + ".txt", Content: "x"}
	}
	resp := doWriteBatchRequest(t, app, dir, files)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	entries, err := os.ReadDir(dir)
	if err == nil {
		require.Empty(t, entries, "校验失败必须整批拒绝，不写任何文件")
	}
}

// TestIntegrationsFsWriteBatchAcceptsExactlyMaxFiles 覆盖边界：恰好 100 个文件
// 必须成功（上限是 >100 才拒绝，不是 >=100）。
func TestIntegrationsFsWriteBatchAcceptsExactlyMaxFiles(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")

	files := make([]integrationsFsWriteBatchFileForTest, 100)
	for i := range files {
		files[i] = integrationsFsWriteBatchFileForTest{RelPath: "f" + strconv.Itoa(i) + ".txt", Content: "x"}
	}
	resp := doWriteBatchRequest(t, app, dir, files)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
}

// TestIntegrationsFsWriteBatchRejectsRelPathWithDotDot 覆盖 rel_path 带 `..` → 403。
func TestIntegrationsFsWriteBatchRejectsRelPathWithDotDot(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")

	resp := doWriteBatchRequest(t, app, dir, []integrationsFsWriteBatchFileForTest{
		{RelPath: "../../etc/passwd", Content: "pwned"},
	})
	require.Equal(t, http.StatusForbidden, resp.Code)
	require.Contains(t, resp.Body.String(), "path_not_allowed")

	_, statErr := os.Stat(filepath.Join(home, "etc", "passwd"))
	require.True(t, os.IsNotExist(statErr))
}

// TestIntegrationsFsWriteBatchRejectsRelPathStartingWithSlash 覆盖 rel_path
// 以 `/` 开头（伪装成绝对路径）必须 403。
func TestIntegrationsFsWriteBatchRejectsRelPathStartingWithSlash(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")

	resp := doWriteBatchRequest(t, app, dir, []integrationsFsWriteBatchFileForTest{
		{RelPath: "/etc/passwd", Content: "pwned"},
	})
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationRelPathSafe 是 integrationRelPathSafe 的表驱动纯函数测试，
// 覆盖评审 Important#2：Windows 专属的反斜杠/盘符逃逸构造
// （"..\\..\\x"、"C:x" 等）在 macOS/Linux 上跑 filepath.Join 不会重现
// （反斜杠、冒号在类 Unix 系统上只是普通文件名字符），必须直接测这个纯函数
// 本身，而不是只走 HTTP 层——否则本仓库在非 Windows 开发机上跑测试永远看
// 不到这类 payload 被拒绝，直到有人真的在 Windows 远端机器上触发。
func TestIntegrationRelPathSafe(t *testing.T) {
	cases := []struct {
		name    string
		relPath string
		want    bool
	}{
		{name: "空字符串", relPath: "", want: false},
		{name: "普通文件名", relPath: "SKILL.md", want: true},
		{name: "多层子目录", relPath: "hooks/session-start", want: true},
		{name: "多层子目录含点号但非逃逸", relPath: "a/./b.txt", want: true},
		{name: "以斜杠开头_伪装绝对路径", relPath: "/etc/passwd", want: false},
		{name: "单个ddot段_跳出dir", relPath: "../../etc/passwd", want: false},
		{name: "中间夹带ddot段", relPath: "a/../../b", want: false},
		{name: "Windows反斜杠ddot逃逸", relPath: `..\..\..\settings.json`, want: false},
		{name: "Windows前导反斜杠", relPath: `\x`, want: false},
		{name: "Windows盘符相对路径", relPath: `C:x`, want: false},
		{name: "Windows反斜杠混合ddot", relPath: `a\..\..\b`, want: false},
		{name: "文件名含冒号", relPath: "weird:name.txt", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := integrationRelPathSafe(tc.relPath)
			require.Equal(t, tc.want, got, "relPath=%q", tc.relPath)
		})
	}
}

// TestIntegrationsFsWriteBatchRejectsDirOutsideWhitelist 覆盖 write-batch 的
// 白名单外 403：dir 本身不在任何白名单根内。
func TestIntegrationsFsWriteBatchRejectsDirOutsideWhitelist(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, "Documents", "not-whitelisted")

	resp := doWriteBatchRequest(t, app, dir, []integrationsFsWriteBatchFileForTest{
		{RelPath: "a.txt", Content: "x"},
	})
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestIntegrationsFsWriteBatchAbortsOnFirstFailureAndReportsWritten 覆盖
// 「首个失败即中止，返回 500 与已写清单」：用一个只读父目录制造中途 I/O 失败，
// 断言已成功写入的文件出现在响应的 written 清单里，且后续文件未写入。
//
// 平台限制：本用例靠 0o500 目录权限位制造真实的 I/O 失败。Windows 不认
// Unix 权限位语义（0o500 不会真的挡住在其下创建文件），且 os.Getuid() 在
// Windows 上恒返回 -1，不会触发下面的 root 用户跳过守卫，所以显式跳过。
func TestIntegrationsFsWriteBatchAbortsOnFirstFailureAndReportsWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位语义，0o500 目录不会真的拒绝写入")
	}
	if os.Getuid() == 0 {
		t.Skip("root 用户不受目录权限位限制，跳过本用例")
	}
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "skills", "superdev")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	blockedSubdir := filepath.Join(dir, "locked")
	require.NoError(t, os.MkdirAll(blockedSubdir, 0o500)) // 只读+可执行，禁止在其下创建新文件
	t.Cleanup(func() { _ = os.Chmod(blockedSubdir, 0o755) })

	resp := doWriteBatchRequest(t, app, dir, []integrationsFsWriteBatchFileForTest{
		{RelPath: "ok.txt", Content: "first"},
		{RelPath: "locked/blocked.txt", Content: "second"},
	})
	require.Equal(t, http.StatusInternalServerError, resp.Code)
	var out struct {
		Written []string `json:"written"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, []string{"ok.txt"}, out.Written)

	content, err := os.ReadFile(filepath.Join(dir, "ok.txt"))
	require.NoError(t, err)
	require.Equal(t, "first", string(content))
}

// TestIntegrationsFsDeleteAllowsSuperdevSkillDir 覆盖 delete 窄白名单放行路径：
// .claude/skills/superdev 必须成功删除。
func TestIntegrationsFsDeleteAllowsSuperdevSkillDir(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "skills", "superdev")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("x"), 0o644))

	resp := httptestDo(t, app, http.MethodDelete, fsQuery("/api/integrations/fs", target), nil)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	_, statErr := os.Stat(target)
	require.True(t, os.IsNotExist(statErr))
}

// TestIntegrationsFsDeleteRejectsSettingsJSON 覆盖 delete 窄白名单拒绝路径：
// .claude/settings.json 不在「skills 根下的 superdev 目录」范围内，必须 403。
func TestIntegrationsFsDeleteRejectsSettingsJSON(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0o644))

	resp := httptestDo(t, app, http.MethodDelete, fsQuery("/api/integrations/fs", target), nil)
	require.Equal(t, http.StatusForbidden, resp.Code)

	_, statErr := os.Stat(target)
	require.NoError(t, statErr, "白名单拒绝时文件必须原样保留")
}

// TestIntegrationsFsRejectsAnonymousRequest 覆盖鉴权红线：受限文件端点绝不进
// securityBypassPath，匿名请求必须 401——用 stat 端点覆盖，其余端点共用同一
// withSecurity 中间件，逻辑等价不重复覆盖。
func TestIntegrationsFsRejectsAnonymousRequest(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "settings.json")

	resp := httptestDoWithHeader(t, app, http.MethodGet, fsQuery("/api/integrations/fs/stat", target), nil,
		map[string]string{"Authorization": ""},
	)
	require.Equal(t, http.StatusUnauthorized, resp.Code, "匿名请求必须 401")
	require.Contains(t, resp.Body.String(), "agent token required")
}

// doWriteRequestWithOptions 与 doWriteRequest 同义，但额外把 Task 9b 新增的
// 可选字段（require_regular_file / new_file_mode）拼进请求体。
//
// 单独一个 helper 而不是给 doWriteRequest 加参数：既有测试全部按「不带新字段」
// 的形状发请求，正是向后兼容性的活证据，不应该被这次扩展改写。
func doWriteRequestWithOptions(t *testing.T, app *App, path, content string, backup bool, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"path": path, "content": content, "backup": backup}
	for key, value := range extra {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return httptestDo(t, app, http.MethodPut, "/api/integrations/fs/write", bytes.NewReader(body))
}

// seedSymlinkInsideWhitelist 在白名单根内造一个指向【同一根内】另一个真实文件
// 的符号链接，返回 (链接路径, 真实文件路径)。
//
// 刻意让链接目标留在根内：指向根外的链接会先被 integrationPathAllowed 以 403
// 挡下（既有测试 TestIntegrationsFsWriteRejectsSymlinkEscape 覆盖了那条），
// 那样就测不到本次新增的「非普通文件目标」守卫本身。
func seedSymlinkInsideWhitelist(t *testing.T, home string) (string, string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	real := filepath.Join(dir, "real-settings.json")
	require.NoError(t, os.WriteFile(real, []byte(`{"real":true}`), 0o600))
	link := filepath.Join(dir, "settings.json")
	require.NoError(t, os.Symlink(real, link))
	return link, real
}

// TestIntegrationsFsStatReportsIsSymlink 覆盖 stat 新增的 is_symlink 字段：
// 判定必须是 lstat 语义（看路径末段自身是不是链接），而不是 os.Stat 的跟随
// 语义——后者对指向普通文件的链接只会说"这是个普通文件"，桌面端就再也无法
// 在远端复刻本机 mutate_config 的符号链接守卫。
//
// 同时锁住 exists / is_dir 的既有语义不变（仍是跟随语义）。
func TestIntegrationsFsStatReportsIsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	app, home := newIntegrationsFsTestApp(t)
	link, real := seedSymlinkInsideWhitelist(t, home)

	linkOut := statForTest(t, app, link)
	require.True(t, linkOut.IsSymlink, "指向普通文件的符号链接必须被报成 is_symlink")
	require.True(t, linkOut.Exists, "exists 保持跟随语义：链接指向的普通文件存在")
	require.False(t, linkOut.IsDir)

	realOut := statForTest(t, app, real)
	require.False(t, realOut.IsSymlink, "普通文件不能被误报成符号链接")
	require.True(t, realOut.Exists)

	missingOut := statForTest(t, app, filepath.Join(home, ".claude", "nope.json"))
	require.False(t, missingOut.IsSymlink, "不存在的路径不是符号链接")
	require.False(t, missingOut.Exists)

	dirOut := statForTest(t, app, filepath.Join(home, ".claude"))
	require.False(t, dirOut.IsSymlink)
	require.True(t, dirOut.IsDir, "目录判定必须保持不变")
}

// integrationsFsStatOut 是 stat 响应体的测试侧镜像。
type integrationsFsStatOut struct {
	Exists    bool  `json:"exists"`
	IsDir     bool  `json:"is_dir"`
	IsSymlink bool  `json:"is_symlink"`
	Size      int64 `json:"size"`
}

func statForTest(t *testing.T, app *App, path string) integrationsFsStatOut {
	t.Helper()
	resp := httptestDo(t, app, http.MethodGet, fsQuery("/api/integrations/fs/stat", path), nil)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var out integrationsFsStatOut
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	return out
}

// TestIntegrationsFsWriteRejectsSymlinkTargetWhenGuardRequested 覆盖新增守卫：
// require_regular_file=true 时，目标自身是符号链接必须被明确错误码拒绝，
// 且既不备份也不写入——备份那一步会 **跟随** 链接读出被指向文件的内容再落到
// 白名单内的 .superdev-bak，是一条真实的读取放大路径。
func TestIntegrationsFsWriteRejectsSymlinkTargetWhenGuardRequested(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	app, home := newIntegrationsFsTestApp(t)
	link, real := seedSymlinkInsideWhitelist(t, home)

	resp := doWriteRequestWithOptions(t, app, link, `{"pwned":true}`, true,
		map[string]any{"require_regular_file": true})

	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	require.Contains(t, resp.Body.String(), "unsafe_write_target",
		"必须是稳定错误码，而不是 500 泛化错误")

	content, err := os.ReadFile(real)
	require.NoError(t, err)
	require.Equal(t, `{"real":true}`, string(content), "被拒绝的写入不得改动链接指向的真实文件")
	_, statErr := os.Stat(filepath.Join(home, ".claude", "settings.json.superdev-bak"))
	require.True(t, os.IsNotExist(statErr), "守卫必须先于备份执行，不能留下备份文件")
}

// TestIntegrationsFsWriteRejectsDirectoryTargetWhenGuardRequested 覆盖守卫的
// 另一半语义：非普通文件（这里是目录）同样必须被拒绝，而不是掉进 500。
func TestIntegrationsFsWriteRejectsDirectoryTargetWhenGuardRequested(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(target, 0o755))

	resp := doWriteRequestWithOptions(t, app, target, `{}`, false,
		map[string]any{"require_regular_file": true})

	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	require.Contains(t, resp.Body.String(), "unsafe_write_target")
}

// TestIntegrationsFsWriteWithoutGuardKeepsLegacySymlinkBehavior 是向后兼容的
// 正面证据：不带 require_regular_file 的请求（Task 8 的 RemoteAgentFs 现有形状）
// 行为与本次扩展之前完全一致——符号链接目标仍然照旧接受。
func TestIntegrationsFsWriteWithoutGuardKeepsLegacySymlinkBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	app, home := newIntegrationsFsTestApp(t)
	link, _ := seedSymlinkInsideWhitelist(t, home)

	resp := doWriteRequest(t, app, link, `{"v":2}`, false)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
}

// TestIntegrationsFsWriteRestrictedNewFileMode 覆盖新增的 new_file_mode：
// 桌面端本机 mutate_config 对新建配置文件用 0600，远端必须能表达同一语义，
// 否则同一份配置在本机装是 0600、远端装却是 0644。
func TestIntegrationsFsWriteRestrictedNewFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位语义")
	}
	app, home := newIntegrationsFsTestApp(t)
	fresh := filepath.Join(home, ".claude", "restricted.json")

	resp := doWriteRequestWithOptions(t, app, fresh, `{"a":1}`, false,
		map[string]any{"new_file_mode": "restricted"})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	info, err := os.Stat(fresh)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "restricted 新建文件必须落 0600")
}

// TestIntegrationsFsWriteRestrictedModeStillPreservesExistingMode 锁住 Task 4
// 人类裁决不被本次扩展回退：new_file_mode 只影响【新建】，已存在文件仍然保留
// 它自己的权限位（哪怕比 restricted 更宽）。
func TestIntegrationsFsWriteRestrictedModeStillPreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位语义")
	}
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude")
	target := filepath.Join(dir, "existing.json")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(target, []byte(`{"v":1}`), 0o640))

	resp := doWriteRequestWithOptions(t, app, target, `{"v":2}`, false,
		map[string]any{"new_file_mode": "restricted"})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm(),
		"已存在文件的权限位由目标自己决定，new_file_mode 不得覆盖它")
}

// TestIntegrationsFsWriteRejectsUnknownNewFileMode 覆盖未知取值必须快速失败，
// 而不是静默退回默认值——静默退回会让调用方以为自己拿到了更紧的权限。
func TestIntegrationsFsWriteRejectsUnknownNewFileMode(t *testing.T) {
	app, home := newIntegrationsFsTestApp(t)
	target := filepath.Join(home, ".claude", "bad-mode.json")

	resp := doWriteRequestWithOptions(t, app, target, `{}`, false,
		map[string]any{"new_file_mode": "0777"})
	require.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
	_, statErr := os.Stat(target)
	require.True(t, os.IsNotExist(statErr), "参数非法时不得落盘")
}

// TestIntegrationsFsWriteGuardFailsClosedWhenTypeIsUndecidable 覆盖守卫的
// fail-closed 分支：调用方要求了 require_regular_file，而服务端**判不出**目标
// 类型（这里用一个不可搜索的父目录制造 EACCES）时必须拒绝，不能把「查不出来」
// 当成「安全」而放行——这与桌面端 LocalFs::check_write_target 对非 NotFound
// 的 lstat 错误一律上报为错误是对称的。
func TestIntegrationsFsWriteGuardFailsClosedWhenTypeIsUndecidable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 目录权限位语义")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限位，造不出 lstat 失败")
	}
	app, home := newIntegrationsFsTestApp(t)
	dir := filepath.Join(home, ".claude", "opaque")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	target := filepath.Join(dir, "settings.json")
	require.NoError(t, os.WriteFile(target, []byte(`{"v":1}`), 0o600))
	// 去掉执行位（搜索权限）→ 对 dir 之下任何路径的 lstat 都会 EACCES。
	require.NoError(t, os.Chmod(dir, 0o600))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	resp := doWriteRequestWithOptions(t, app, target, `{"v":2}`, false,
		map[string]any{"require_regular_file": true})

	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	require.Contains(t, resp.Body.String(), "unsafe_write_target")
}
