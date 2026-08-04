// handler_integrations_fs.go 实现远端编程智能体接入的受限文件读写端点。
//
// 职责：
//   - 提供 stat/read/list/write/rename/write-batch/delete 七个哑的、白名单
//     约束的文件原语，专供桌面端 connector（Rust `RemoteAgentFs`，Task 8）
//     远端安装 MCP 配置 / skill / session hook 时使用
//   - 所有 I/O 一律基于 integrations_paths.go（Task 2）提供的
//     integrationPathAllowed / integrationDeleteAllowed 的返回值执行，不接受
//     调用方声称的原始路径直接落地
//
// 边界：
//   - 白名单外一律 403，不解释语义、不做任何智能体方言的配置合并——方言知识
//     全部在桌面端 Rust，本文件只提供哑的文件原语
//   - 不代理到远端机器（那是 Task 5 的职责）；本文件的端点只处理「运行本端点
//     这台机器」自己的文件
//   - 不把文件内容写进日志；日志只记路径、字节数、Principal
package api

import (
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// integrationsFsMaxReadBytes 是 read 端点允许返回的单文件内容上限。
const integrationsFsMaxReadBytes = 1 << 20 // 1MB

// integrationsFsMaxWriteBytes 是 write 端点允许写入的单文件内容上限，与
// integrationsFsMaxReadBytes 保持一致（配置类小文件的合理上限）。
const integrationsFsMaxWriteBytes = 1 << 20 // 1MB

// integrationsFsMaxListEntries 是 list 端点单次遍历允许返回的最大条目数，
// 超过即视为异常大目录，413 拒绝，避免受限通道被用来枚举巨型目录耗尽资源。
const integrationsFsMaxListEntries = 1000

// integrationsFsWriteBatchMaxFiles 是 write-batch 单批次允许的最大文件数。
const integrationsFsWriteBatchMaxFiles = 100

// integrationsFsWriteBatchMaxBytes 是 write-batch 单批次全部文件内容之和的
// 上限，对应 skill 目录整体安装场景（几十个文件、几百 KB～数 MB）。
const integrationsFsWriteBatchMaxBytes = 4 << 20 // 4MB

// integrationsHome 返回受限文件端点用于白名单校验的 home 根目录。
//
// 默认取 os.UserHomeDir()；测试经 App.integrationsHomeOverride 覆盖为
// t.TempDir()，避免测试真的读写运行测试的开发机的真实 home 目录。生产环境
// 该字段恒为空串，回退到真实 home。
func (a *App) integrationsHome() (string, error) {
	if a.integrationsHomeOverride != "" {
		return a.integrationsHomeOverride, nil
	}
	return os.UserHomeDir()
}

// integrationsFsNewFileModeRestricted 是 write 端点 new_file_mode 字段唯一被
// 接受的非空取值，语义是「新建文件用 0600」。
//
// 刻意做成枚举字符串而不是让调用方直接下发八进制数字：受限文件通道不应该把
// 「目标文件最终是什么权限」这件事完全交给客户端（下发 0777 就没人拦得住），
// 服务端只承认自己定义好的、有限的几档语义。当前只有一档，因为桌面端只需要
// 表达 mutate_config 的 0600；将来若真有第二档，在这里加常量并在
// integrationsFsResolveNewFileMode 里加分支，客户端不会因此获得任意权限位。
const integrationsFsNewFileModeRestricted = "restricted"

// integrationsFsResolveNewFileMode 把请求里的 new_file_mode 翻译成【新建文件】
// 使用的权限位。
//
// 参数：
//   - raw: 请求体里的 new_file_mode 字段原值
//
// 返回：
//   - (新建文件权限位, 取值是否合法)
//
// 注意：
//   - 空串（字段缺席即为空串）必须回 0o644——这是本字段引入之前的行为，Task 8
//     的 RemoteAgentFs 现有请求全部不带这个字段，缺省语义一变它们就会回归
//   - 未知取值返回 false 让调用方 400，而不是静默退回默认：静默退回会让调用方
//     以为自己拿到了更紧的权限，实际却落了更宽的一档
func integrationsFsResolveNewFileMode(raw string) (os.FileMode, bool) {
	switch raw {
	case "":
		return 0o644, true
	case integrationsFsNewFileModeRestricted:
		return 0o600, true
	default:
		return 0, false
	}
}

// integrationsFsPathIsSymlink 用 os.Lstat 判断【请求中声明的】路径末段自身是不是
// 符号链接。
//
// 为什么必须用请求里的原始路径而不是 integrationPathAllowed 的返回值：后者已经
// 把已存在部分做过 EvalSymlinks 收敛，末段若是符号链接，返回的就是解析后的真实
// 目标——对它 Lstat 永远得到 false，符号链接这条信息在那一步就已经丢了。
//
// 为什么必须用 Lstat 而不是 Stat：Stat 跟随符号链接，对一个指向普通文件的链接
// 只会说「这是普通文件」，桌面端因此无法在远端复刻本机 mutate_config 的
// 「拒绝符号链接目标」守卫。
//
// Lstat 失败（不存在、权限不足等）一律回 false：不存在的路径不是符号链接，而
// 其它失败情况下调用方随后的实际 I/O 也会自行失败，这里不越权把它变成拒绝。
func integrationsFsPathIsSymlink(rawPath string) bool {
	info, err := os.Lstat(filepath.Clean(rawPath))
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// integrationsFsStat 处理 GET /api/integrations/fs/stat：返回白名单内路径的
// 存在性/类型/大小/是否符号链接，供桌面端判断目标文件当前状态。
//
// exists / is_dir 保持 os.Stat 的【跟随符号链接】语义不变（桌面端既有判断依赖
// 它）；is_symlink 是本次新增的一位，走 os.Lstat 的不跟随语义，两者刻意不同源。
func (a *App) integrationsFsStat(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	target, err := integrationPathAllowed(home, rawPath)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: stat 被白名单拒绝 path=%s by=%s", rawPath, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	isSymlink := integrationsFsPathIsSymlink(rawPath)
	info, statErr := os.Stat(target)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			// 悬空符号链接（链接本身在、指向的目标没了）会走到这里：exists 仍是
			// false（跟随语义下确实读不到东西），但 is_symlink 必须如实为 true，
			// 否则桌面端会把它当成「不存在，可以放心创建」而写穿这条链接。
			jsonOK(w, map[string]any{"exists": false, "is_dir": false, "is_symlink": isSymlink, "size": int64(0)})
			return
		}
		log.Printf("[SuperDev] integrations: stat 失败 path=%s：%v", target, statErr)
		jsonError(w, http.StatusInternalServerError, "stat failed")
		return
	}
	jsonOK(w, map[string]any{"exists": true, "is_dir": info.IsDir(), "is_symlink": isSymlink, "size": info.Size()})
}

// integrationsFsRead 处理 GET /api/integrations/fs/read：读取白名单内文件内容。
//
// 目标不存在时返回 200 + {exists:false}，而不是 404——桌面端在探测「这个配置
// 文件是否已存在」时这是常见路径，不应该被当成错误处理。内容 >1MB 返回 413。
func (a *App) integrationsFsRead(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	target, err := integrationPathAllowed(home, rawPath)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: 读取被白名单拒绝 path=%s by=%s", rawPath, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	f, openErr := os.Open(target)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			jsonOK(w, map[string]any{"exists": false})
			return
		}
		log.Printf("[SuperDev] integrations: 打开文件失败 path=%s：%v", target, openErr)
		jsonError(w, http.StatusInternalServerError, "read failed")
		return
	}
	defer f.Close()
	info, statErr := f.Stat()
	if statErr != nil {
		log.Printf("[SuperDev] integrations: 读取前 stat 失败 path=%s：%v", target, statErr)
		jsonError(w, http.StatusInternalServerError, "read failed")
		return
	}
	if info.IsDir() {
		log.Printf("[SuperDev] integrations: 读取目标是目录 path=%s", target)
		jsonError(w, http.StatusBadRequest, "path is a directory")
		return
	}
	// 读到上限+1 字节即可判断是否超限，不依赖 Stat 返回的 Size——避免和并发
	// 写入造成的大小竞态耦合，也省一次系统调用。
	data, readErr := io.ReadAll(io.LimitReader(f, integrationsFsMaxReadBytes+1))
	if readErr != nil {
		log.Printf("[SuperDev] integrations: 读取失败 path=%s：%v", target, readErr)
		jsonError(w, http.StatusInternalServerError, "read failed")
		return
	}
	if len(data) > integrationsFsMaxReadBytes {
		log.Printf("[SuperDev] integrations: 读取内容过大 path=%s", target)
		jsonError(w, http.StatusRequestEntityTooLarge, "content too large")
		return
	}
	jsonOK(w, map[string]string{"content": string(data)})
}

// integrationsFsList 处理 GET /api/integrations/fs/list：递归列出白名单内目录
// 下所有文件的相对路径（相对于查询目录本身，正斜杠归一 + 排序），供桌面端做
// skill 目录内容比对（判断是否已安装/是否需要更新）。
//
// 目标目录不存在时视为空目录（skill 尚未安装是常见路径，不是错误）；条目数
// 超过上限返回 413，避免受限通道被用来枚举巨型目录耗尽资源。
func (a *App) integrationsFsList(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	target, err := integrationPathAllowed(home, rawPath)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: list 被白名单拒绝 path=%s by=%s", rawPath, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	if _, statErr := os.Stat(target); statErr != nil {
		if os.IsNotExist(statErr) {
			jsonOK(w, map[string]any{"files": []string{}})
			return
		}
		log.Printf("[SuperDev] integrations: list stat 失败 path=%s：%v", target, statErr)
		jsonError(w, http.StatusInternalServerError, "stat failed")
		return
	}

	files := make([]string, 0, 64)
	tooMany := false
	walkErr := filepath.WalkDir(target, func(p string, d fs.DirEntry, walkEntryErr error) error {
		if walkEntryErr != nil {
			return walkEntryErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(target, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) > integrationsFsMaxListEntries {
			tooMany = true
			return fs.SkipAll
		}
		return nil
	})
	if tooMany {
		log.Printf("[SuperDev] integrations: list 条目数超限 path=%s", target)
		jsonError(w, http.StatusRequestEntityTooLarge, "too many entries")
		return
	}
	if walkErr != nil {
		log.Printf("[SuperDev] integrations: list 遍历失败 path=%s：%v", target, walkErr)
		jsonError(w, http.StatusInternalServerError, "list failed")
		return
	}
	sort.Strings(files)
	jsonOK(w, map[string]any{"files": files})
}

// integrationsFsWrite 处理 PUT /api/integrations/fs/write：白名单内原子写，可选备份。
//
// 权限位：目标已存在时沿用它原有的权限位（不放宽也不收紧）；只有新建文件
// 才用 new_file_mode 指定的那一档（缺省 0o644）——理由与细节见函数体内 mode
// 计算前的注释。
//
// 两个可选字段（Task 9b 新增，缺席时行为与引入之前逐字节一致）：
//   - require_regular_file: true 时，目标自身是符号链接或非普通文件一律 409
//     拒绝。这条守卫**必须由本端点执行**而不是由桌面端先 stat 再 write：那样
//     两次调用之间存在 TOCTOU 窗口，攻击者可以在窗口内把目标换成符号链接，
//     客户端的判定形同虚设
//   - new_file_mode: 新建文件的权限档位，取值见 integrationsFsResolveNewFileMode
func (a *App) integrationsFsWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path               string `json:"path"`
		Content            string `json:"content"`
		Backup             bool   `json:"backup"`
		RequireRegularFile bool   `json:"require_regular_file"`
		NewFileMode        string `json:"new_file_mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Content) > integrationsFsMaxWriteBytes {
		jsonError(w, http.StatusRequestEntityTooLarge, "content too large")
		return
	}
	newFileMode, modeOK := integrationsFsResolveNewFileMode(req.NewFileMode)
	if !modeOK {
		log.Printf("[SuperDev] integrations: 写入的 new_file_mode 非法 path=%s value=%s", req.Path, req.NewFileMode)
		jsonError(w, http.StatusBadRequest, "invalid new_file_mode")
		return
	}
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	target, err := integrationPathAllowed(home, req.Path)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: 写入被白名单拒绝 path=%s by=%s", req.Path, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	// 守卫必须排在 MkdirAll / 备份 / 写入之前：备份那一步（copyFile）会**跟随**
	// 符号链接读出被指向文件的内容，再把它落到白名单内的 .superdev-bak，是一条
	// 真实的读取放大路径；等到写入那一步再拒绝已经晚了。
	//
	// 判定用 Lstat 而不是 Stat：Lstat 下符号链接自身的 Mode 带 ModeSymlink 位、
	// IsRegular() 恒为 false，所以「非普通文件」这一个条件同时盖住了符号链接与
	// 目录/FIFO/套接字；换成 Stat 就会跟随链接、把指向普通文件的链接判成合法目标。
	if req.RequireRegularFile {
		info, lstatErr := os.Lstat(filepath.Clean(req.Path))
		switch {
		case lstatErr == nil && !info.Mode().IsRegular():
			name, _, _ := principalFromRequest(r)
			log.Printf("[SuperDev] integrations: 写入被非普通文件守卫拒绝 path=%s mode=%s by=%s",
				target, info.Mode(), name)
			jsonCodeError(w, http.StatusConflict, "unsafe_write_target",
				"target is not a regular file", nil)
			return
		case lstatErr != nil && !os.IsNotExist(lstatErr):
			// 「查不出来」不等于「安全」：调用方明确要求了这条守卫，而我们无法
			// 判定目标类型，只能 fail-closed。与桌面端本机侧的
			// LocalFs::check_write_target 对称——那边对非 NotFound 的 lstat 错误
			// 同样是返回错误（config_stat_failed），而不是当成普通文件放行。
			log.Printf("[SuperDev] integrations: 写入守卫无法判定目标类型 path=%s：%v", target, lstatErr)
			jsonCodeError(w, http.StatusConflict, "unsafe_write_target",
				"cannot determine target type", nil)
			return
		}
		// 剩下的是 os.IsNotExist(lstatErr)：目标不存在，本来就该新建，放行。
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		log.Printf("[SuperDev] integrations: 创建父目录失败 path=%s：%v", target, err)
		jsonError(w, http.StatusInternalServerError, "mkdir failed")
		return
	}

	// 目标已存在时必须沿用它原有的权限位，不能被这次写入悄悄放宽——用户可能
	// 手工把 ~/.claude/settings.json 收紧成 0600（里面可能有为其它 MCP
	// server 配置的 API key），一次远端写入不该把它放宽回 0644；这与本文件
	// copyFile 把备份统一写成 0o600 的理由（宁紧勿松）是同一条红线，写入
	// 目标本身也不能例外。只有新建文件才用 0o644 默认值。
	//
	// 这里是相对 brief Step 3 范例代码（硬编码 0o644）的一处明确授权偏离
	// ——人类已裁决：保留原有权限，不要为了字面忠于 brief 把它改回硬编码。
	//
	// new_file_mode 只参与「新建」这一档，不改动上面这条裁决：已存在的目标仍然
	// 由它自己的权限位说了算，客户端说 restricted 也不会把一个 0o644 的既有
	// 配置收紧掉（那同样是未经用户同意改动用户文件）。
	info, statErr := os.Stat(target)
	targetExists := statErr == nil
	mode := newFileMode
	switch {
	case targetExists:
		mode = info.Mode().Perm()
	case statErr != nil && !os.IsNotExist(statErr):
		// stat 失败但不是"不存在"这种正常情况（权限异常等未知状态）：
		// fail-safe 收紧到 0o600，不要静默沿用放宽的 0o644 默认值。
		log.Printf("[SuperDev] integrations: 写入前 stat 异常，回退更紧权限 path=%s：%v", target, statErr)
		mode = 0o600
	}

	backupPath := ""
	if req.Backup && targetExists {
		// 只在目标已存在时才有旧内容可备份；命名规则见 integrationBackupPath
		// 头注释，必须与桌面端 Rust backup_path() 逐字节一致。
		backupPath = integrationBackupPath(target)
		if err := copyFile(target, backupPath); err != nil {
			log.Printf("[SuperDev] integrations: 备份失败 path=%s：%v", target, err)
			jsonError(w, http.StatusInternalServerError, "backup failed")
			return
		}
	}
	if err := atomicWriteFile(target, []byte(req.Content), mode); err != nil {
		log.Printf("[SuperDev] integrations: 原子写失败 path=%s：%v", target, err)
		jsonError(w, http.StatusInternalServerError, "write failed")
		return
	}
	name, _, _ := principalFromRequest(r)
	log.Printf("[SuperDev] integrations: 已写入 path=%s bytes=%d backup=%v mode=%s by=%s",
		target, len(req.Content), backupPath != "", mode, name)
	jsonOK(w, map[string]string{"backup_path": backupPath})
}

// integrationsFsRename 处理 POST /api/integrations/fs/rename：把 from 改名为
// to，供 connector 安装流程做「旧 skill 目录备份/失败恢复」使用。
//
// from 与 to 必须分别独立通过白名单校验——只校验其中一侧会让另一侧成为越权
// 读写的后门（例如把白名单外的任意文件 rename 进白名单根，或反过来把白名单
// 内的文件 rename 到白名单外）。
func (a *App) integrationsFsRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	fromTarget, err := integrationPathAllowed(home, req.From)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: rename 源路径被白名单拒绝 from=%s by=%s", req.From, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	toTarget, err := integrationPathAllowed(home, req.To)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: rename 目标路径被白名单拒绝 to=%s by=%s", req.To, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	if err := os.MkdirAll(filepath.Dir(toTarget), 0o755); err != nil {
		log.Printf("[SuperDev] integrations: rename 创建目标父目录失败 to=%s：%v", toTarget, err)
		jsonError(w, http.StatusInternalServerError, "mkdir failed")
		return
	}
	if err := os.Rename(fromTarget, toTarget); err != nil {
		log.Printf("[SuperDev] integrations: rename 失败 from=%s to=%s：%v", fromTarget, toTarget, err)
		jsonError(w, http.StatusInternalServerError, "rename failed")
		return
	}
	name, _, _ := principalFromRequest(r)
	log.Printf("[SuperDev] integrations: 已 rename from=%s to=%s by=%s", fromTarget, toTarget, name)
	jsonOK(w, map[string]string{})
}

// integrationsFsWriteBatchFile 是 write-batch 请求体里单个文件项。
type integrationsFsWriteBatchFile struct {
	RelPath    string `json:"rel_path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
}

// integrationsFsWriteBatch 处理 PUT /api/integrations/fs/write-batch：在 dir 下
// 批量原子写入多个文件，供 skill 目录整体安装使用。
//
// 两阶段设计：
//  1. 全量校验（文件数上限、总字节上限、每个 rel_path 格式 + 拼接后的白名单
//     校验）——任一项不过，整批拒绝、不落盘任何内容，避免半成品状态。
//  2. 依次原子写——某一项 I/O 失败时中止，返回 500 与已成功写入的 rel_path
//     清单，供调用方知道磁盘当前的真实状态（此时必然是部分完成，本端点不做
//     自动回滚，恢复策略是调用方职责）。
func (a *App) integrationsFsWriteBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir   string                         `json:"dir"`
		Files []integrationsFsWriteBatchFile `json:"files"`
	}
	// 6MB 请求体上限：略高于内容 4MB 硬上限，留出 JSON 结构、字段名、字符串
	// 转义的余量；真正的内容上限由下面的 totalBytes 检查负责。
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Files) > integrationsFsWriteBatchMaxFiles {
		log.Printf("[SuperDev] integrations: write-batch 文件数超限 dir=%s count=%d", req.Dir, len(req.Files))
		jsonError(w, http.StatusBadRequest, "too many files")
		return
	}
	totalBytes := 0
	for _, f := range req.Files {
		totalBytes += len(f.Content)
	}
	if totalBytes > integrationsFsWriteBatchMaxBytes {
		log.Printf("[SuperDev] integrations: write-batch 总字节超限 dir=%s bytes=%d", req.Dir, totalBytes)
		jsonError(w, http.StatusRequestEntityTooLarge, "content too large")
		return
	}
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	name, _, _ := principalFromRequest(r)

	if _, err := integrationPathAllowed(home, req.Dir); err != nil {
		log.Printf("[SuperDev] integrations: write-batch dir 被白名单拒绝 dir=%s by=%s", req.Dir, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}

	// 阶段一：全量校验。targets 与 req.Files 按下标一一对应，全部通过后才
	// 进入阶段二的实际写入。
	targets := make([]string, len(req.Files))
	for i, f := range req.Files {
		if !integrationRelPathSafe(f.RelPath) {
			log.Printf("[SuperDev] integrations: write-batch rel_path 非法 dir=%s rel_path=%s by=%s", req.Dir, f.RelPath, name)
			jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
			return
		}
		// 拼接后必须再过一次白名单：dir 本身合法不代表 dir 内部某个已存在的
		// 名字不是指向白名单外的符号链接——integrationPathAllowed 对已存在
		// 祖先的 EvalSymlinks 收敛正是为了拦住这种情况，只做字符串层面的
		// rel_path 检查（上面的 integrationRelPathSafe）拦不住。
		candidate := filepath.Join(req.Dir, filepath.FromSlash(f.RelPath))
		target, err := integrationPathAllowed(home, candidate)
		if err != nil {
			log.Printf("[SuperDev] integrations: write-batch 目标被白名单拒绝 dir=%s rel_path=%s by=%s", req.Dir, f.RelPath, name)
			jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
			return
		}
		targets[i] = target
	}

	// 阶段二：依次原子写。written 记录已成功的 rel_path，供中途失败时回显。
	written := make([]string, 0, len(req.Files))
	for i, f := range req.Files {
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(targets[i]), 0o755); err != nil {
			log.Printf("[SuperDev] integrations: write-batch 创建父目录失败 path=%s：%v", targets[i], err)
			jsonWrite(w, http.StatusInternalServerError, map[string]any{"error": "mkdir failed", "written": written})
			return
		}
		if err := atomicWriteFile(targets[i], []byte(f.Content), mode); err != nil {
			log.Printf("[SuperDev] integrations: write-batch 写入失败 path=%s：%v", targets[i], err)
			jsonWrite(w, http.StatusInternalServerError, map[string]any{"error": "write failed", "written": written})
			return
		}
		written = append(written, f.RelPath)
	}
	log.Printf("[SuperDev] integrations: write-batch 完成 dir=%s files=%d bytes=%d by=%s", req.Dir, len(req.Files), totalBytes, name)
	jsonOK(w, map[string]any{"written": written})
}

// integrationRelPathSafe 校验 write-batch 单个 rel_path 的基本格式：非空、
// 不以 "/" 开头（不允许伪装成绝对路径）、不含 ".." 段（不允许跳出 dir 本身）。
// 这只是格式层面的快速失败；真正的越权屏障仍是随后对拼接结果调用的
// integrationPathAllowed（拦住 dir 内部符号链接逃逸这类字符串检查发现不了的
// 情况）。
//
// 反斜杠/冒号显式拒绝 + filepath.IsLocal 双重把关：
//   - 本 agent 二进制会被编译到 Windows 远端机器上运行，那里 "\" 是真正的
//     路径分隔符、"C:" 是真正的盘符前缀。如果只按 "/" 切分再逐段比较 ".."
//     （旧实现），像 "..\\..\\..\\settings.json" 这样的 rel_path 在切分时是
//     单个 segment（不含 "/"），会被误判为安全，随后 filepath.Join 在
//     Windows 目标机上把它解析成真实的上级目录——外层 integrationPathAllowed
//     仍然会挡住越出白名单根的部分（所以不是白名单逃逸），但会落在请求的
//     dir 之外、调用方以为自己在写别的 connector 目录，污染面比预期大得多。
//   - filepath.IsLocal 是标准库提供的、按当前编译目标 GOOS 语义判断
//     "Join(base, path) 保证不逃出 base" 的权威实现，覆盖了空路径/绝对路径/
//     ".." 段这几条（原来的手写检查因此被它取代而不是叠加）。但它的判断会
//     随 GOOS 变化：本进程在类 Unix 系统（包括跑本文件单测的开发机）上编译
//     时，反斜杠只是普通文件名字符、不是分隔符，IsLocal 不会把
//     "..\\..\\x" 或 "C:x" 判定为不安全——这类攻击 payload 只有在 Windows
//     编译产物上才会真正逃逸。为了让 integrationRelPathSafe 这个纯函数的
//     判断不随 GOOS 变化（且能在非 Windows 开发机上用单元测试直接锁住这类
//     payload），显式拒绝反斜杠与冒号，不依赖 IsLocal 在当前平台是否认得
//     它们。
func integrationRelPathSafe(relPath string) bool {
	if relPath == "" {
		return false
	}
	if strings.ContainsAny(relPath, "\\:") {
		return false
	}
	return filepath.IsLocal(relPath)
}

// integrationsFsDelete 处理 DELETE /api/integrations/fs：递归删除窄白名单内的
// 目录，供卸载流程清理远端 skill 安装。窄白名单语义见
// integrations_paths.go 的 integrationDeleteAllowed 头注释——仅放行各智能体
// skill 目录树下名为 superdev 或 superdev.* 的目录，不是 write 端点用的宽
// 白名单。
func (a *App) integrationsFsDelete(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	home, err := a.integrationsHome()
	if err != nil {
		log.Printf("[SuperDev] integrations: 解析 home 失败：%v", err)
		jsonError(w, http.StatusInternalServerError, "resolve home failed")
		return
	}
	target, err := integrationDeleteAllowed(home, rawPath)
	if err != nil {
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: 删除被白名单拒绝 path=%s by=%s", rawPath, name)
		jsonCodeError(w, http.StatusForbidden, "path_not_allowed", "path not allowed", nil)
		return
	}
	if err := os.RemoveAll(target); err != nil {
		log.Printf("[SuperDev] integrations: 删除失败 path=%s：%v", target, err)
		jsonError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	name, _, _ := principalFromRequest(r)
	log.Printf("[SuperDev] integrations: 已删除 path=%s by=%s", target, name)
	jsonOK(w, map[string]string{})
}

// integrationBackupPath 按目标路径算出备份文件路径。
//
// 扩展名规则与桌面端 Rust backup_path() 逐字节一致：有扩展名 →
// "<name>.<ext>.superdev-bak"；无扩展名 → "<name>.superdev-bak"。这是
// 一条跨语言契约，本地/远端安装产生的备份文件名必须相同。
//
// 扩展名判定复刻 Rust std::path::Path::extension() 的语义，而不是 Go 标准库
// filepath.Ext：filepath.Ext(".bashrc") 会把整个 ".bashrc" 当成扩展名，但
// Rust 对「文件名以 '.' 开头且内部没有其它 '.'」的隐藏文件视为无扩展名——
// 两者对隐藏文件的判定不同，必须手写以保证与桌面端一致。
func integrationBackupPath(target string) string {
	dir := filepath.Dir(target)
	base := filepath.Base(target)
	ext := integrationFileExtensionRustStyle(base)
	if ext == "" {
		return filepath.Join(dir, base+".superdev-bak")
	}
	stem := base[:len(base)-len(ext)-1] // -1 去掉扩展名前的那个 '.'
	return filepath.Join(dir, stem+"."+ext+".superdev-bak")
}

// integrationFileExtensionRustStyle 复刻 Rust Path::extension() 的语义：
//   - 没有 '.' → 无扩展名
//   - 以 '.' 开头且这是文件名里唯一的 '.'（如 ".bashrc"）→ 无扩展名
//   - 否则 → 最后一个 '.' 之后的部分
func integrationFileExtensionRustStyle(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		// idx == -1：没有点；idx == 0：唯一的点在开头（LastIndex 已经是
		// "最后一个"，如果后面还有点，idx 会 > 0）——两种情况都是无扩展名。
		return ""
	}
	return name[idx+1:]
}

// atomicWriteFile 把 content 写入 path：先在同目录写一个临时文件，再
// rename 替换目标——rename 在同一文件系统内是原子操作，避免进程崩溃或并发
// 读取看到半份内容。
func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".integrations-fs-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// 任何提前返回路径都要清理残留 tmp 文件；rename 成功后 tmpPath 已不存在，
	// Remove 返回的 ENOENT 被忽略（defer 不检查返回值是有意的）。
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// copyFile 把 src 的内容原样复制到 dst（备份用途），不保留 src 的权限位——
// 备份文件统一按 0o600 落盘：配置文件里可能含用户为其它 MCP server 配置的
// API key，宁紧勿松。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}
