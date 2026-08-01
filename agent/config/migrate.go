// migrate.go —— legacy config.yaml → split 双层格式的迁移。
//
// 职责：
//   - BuildMigrationPlan：读 legacy 配置，产出预览（疑似密钥、路径相对化
//     清单、UI 状态去向、.gitignore 变更）
//   - ApplyMigration：按用户处置决定落两层 + uistate + 备份 + gitignore
//
// 边界：
//   - 迁移永远显式触发（desktop preview→apply 人审），本文件不做静默转换
//   - 不校验密钥真伪：「不挡、只亮」，处置权在人
package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// ErrAlreadyMigrated 表示项目已是 split 格式，无需迁移。
var ErrAlreadyMigrated = errors.New("project already uses split config format")

// suspectKeyRe 命中常见密钥类键名（大小写不敏感）。
var suspectKeyRe = regexp.MustCompile(`(?i)(secret|token|passwd|password|api[-_]?key|access[-_]?key|private[-_]?key|credential)`)

// suspectValRe 命中常见密钥值前缀（sk-/pk_/ghp_/xoxb- 等发行商格式）。
var suspectValRe = regexp.MustCompile(`^(sk|pk|ghp|gho|xox[a-z]|glpat|AKIA)[-_A-Za-z0-9]`)

// maskValue 保留前 4 字符用于辨认，其余打星；短值全星。
func maskValue(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:4] + strings.Repeat("*", min(len(v)-4, 12))
}

// MigrationPlan 是 BuildMigrationPlan 的产出：一份只读预览，描述把 legacy
// 单文件配置迁到 split 双层格式会带来的变化，但不实际改动任何文件。
// Task 8 经 HTTP 把它交给 desktop，Task 10 渲染成人审对话框。
type MigrationPlan struct {
	RootPath         string          `json:"root_path"`
	Suspects         []SuspectEntry  `json:"suspects"`
	UIStateEnvs      []string        `json:"ui_state_envs"` // 将迁 store 的环境名列表
	Gitignore        GitignoreChange `json:"gitignore"`
	ServiceCount     int             `json:"service_count"`
	RelativizedPaths []string        `json:"relativized_paths"` // 将被转相对的绝对路径清单（预览展示）
}

// SuspectEntry 是一条疑似密钥线索：可能来自项目级 variables，也可能来自
// 某个 service 在某个 env 下的 env_vars。Masked 是脱敏后的值，绝不携带
// 明文——迁移「不挡、只亮」，去留由人决定，但预览本身不能变成泄露渠道。
type SuspectEntry struct {
	Scope   string `json:"scope"`             // "variables" | "env_vars"
	Service string `json:"service,omitempty"` // env_vars 时为服务名
	Env     string `json:"env,omitempty"`
	Key     string `json:"key"`
	Masked  string `json:"masked_value"`
	Reason  string `json:"reason"`
}

// GitignoreChange 描述迁移会对项目根 .gitignore 做的增删建议（diff 而非
// 重写）：撤掉对整个 .superdev/ 目录的忽略（共享层要入库），改为只忽略
// 机器层文件与迁移备份。
type GitignoreChange struct {
	RemoveLines []string `json:"remove_lines"`
	AddLines    []string `json:"add_lines"`
}

// MigrationDecision 是人对单条 SuspectEntry 的处置：留在共享层还是本机层。
// Task 7 的 ApplyMigration 消费这份决定列表；未被显式决定的疑似项按「不挡、
// 只亮」原则默认去本机层（该默认值在 ApplyMigration 中生效，本文件不实现）。
type MigrationDecision struct {
	Scope       string `json:"scope"`
	Service     string `json:"service,omitempty"`
	Env         string `json:"env,omitempty"`
	Key         string `json:"key"`
	Disposition string `json:"disposition"` // "shared" | "local"
}

// BuildMigrationPlan 读取 rootPath 下的 legacy 配置（.superdev/config.yaml），
// 产出一份只读的迁移预览：疑似密钥清单（已脱敏）、需要相对化的绝对路径
// 清单、将迁往 agent 本地 UI 状态的环境名列表，以及 .gitignore 的增删建议。
// 本函数不写任何文件——实际落盘迁移由 Task 7 的 ApplyMigration 完成。
//
// 参数：
//   - rootPath: 项目根目录绝对路径
//
// 返回：
//   - MigrationPlan：只读迁移预览
//   - error：两个哨兵错误——
//     ErrNotFound：该目录既无 legacy 也无已迁移的 split 配置，无可迁移
//     对象（常见于全新空目录：DetectFormat 在这种情况下也会返回
//     FormatSplit，必须额外确认 project.yaml 确实存在才能把它当作
//     「已迁移」，否则会把从未初始化的目录误报为已迁移）；
//     ErrAlreadyMigrated：project.yaml 已存在，项目已是 split 格式，
//     无需迁移。
//     其余错误来自读取/解析 legacy 配置文件（原样透传并附加上下文）。
func BuildMigrationPlan(rootPath string) (MigrationPlan, error) {
	loader := NewLoader(rootPath)

	if loader.DetectFormat() == FormatSplit {
		// DetectFormat 对全新空目录也返回 FormatSplit（新项目默认新格式），
		// 因此「已经迁移过」与「压根没有任何配置」是两种不同情形，不能只凭
		// Format==Split 就下结论——必须再看 project.yaml 是否真的存在。
		if _, err := os.Stat(loader.projectPath()); err == nil {
			return MigrationPlan{}, ErrAlreadyMigrated
		}
		return MigrationPlan{}, ErrNotFound
	}

	project, err := loader.Load()
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("load legacy config: %w", err)
	}

	plan := MigrationPlan{
		RootPath:         rootPath,
		Suspects:         scanSuspects(project),
		UIStateEnvs:      uiStateEnvs(project),
		Gitignore:        gitignoreDiff(rootPath),
		ServiceCount:     len(project.Services),
		RelativizedPaths: collectRelativizedPaths(project, rootPath),
	}

	log.Printf("[SuperDev] config: migration plan built project=%s suspects=%d relativized=%d", rootPath, len(plan.Suspects), len(plan.RelativizedPaths))
	return plan, nil
}

// dispositionShared 是 MigrationDecision.Disposition 里唯一需要精确识别的取值。
// 另一个合法值 "local" 不必命名：判定写成「不等于 shared 即去本机层」，空串、
// 大小写写错、桌面端传来一个没见过的新值，全都自动落到安全的一侧。
const dispositionShared = "shared"

// ApplyMigration 执行 legacy → split 拆分。
//
// 参数：
//   - rootPath: 项目根
//   - decisions: 疑似密钥处置（缺省条目按 disposition=local 处理——安全默认）
//   - uiStore: UI 状态的目标 store
//
// 返回：
//   - error：ErrNotFound（无任何配置可迁）、ErrAlreadyMigrated（已是 split），
//     或某一步的 I/O 失败（错误消息带 "migration step N" 步骤上下文）
//
// 执行顺序（崩溃安全：project.yaml 落盘是格式翻转点，翻转前的中间产物
// 均可安全重跑；翻转后 config.yaml 残留只触发 DetectFormat 告警不破坏）：
//  1. 校验：legacy 存在且未迁移（否则 ErrNotFound / ErrAlreadyMigrated）
//  2. Load legacy → 按 decisions 拆出 localYAML（未决疑似项默认 local）
//  3. saveLocal 写机器层
//  4. uiStore.ReplaceEnvSelected 写 UI 状态
//  5. 写 project.yaml（复用 split Save 组装；保留 log_rules）
//  6. os.Rename config.yaml → config.yaml.bak
//  7. 重写 .gitignore（移除整目录忽略行、追加机器层忽略行）
//
// 注意：
//   - 步骤 6/7 失败不回滚 1-5：迁移已生效（split 可读），残留问题
//     以 error 返回并由日志揭示，人工可修
//   - 步骤 3 必须早于步骤 5：local.yaml 是「哪些键归机器层」的唯一声明，
//     步骤 5 内部的 splitOwnership 正是照着它把密钥从共享层剥掉的。顺序
//     一旦颠倒，密钥会先被写进准备入库的 project.yaml——那就是泄露本身。
func ApplyMigration(rootPath string, decisions []MigrationDecision, uiStore *UIStateStore) error {
	log.Printf("[SuperDev] config: migration started project=%s decisions=%d", rootPath, len(decisions))
	loader := NewLoader(rootPath)

	// 步骤 1：校验。与 BuildMigrationPlan 同一套判定——DetectFormat 对全新空目录
	// 也返回 FormatSplit，必须再看 project.yaml 是否真的存在，才能区分「已经迁移
	// 过」与「压根没有任何配置」。
	if loader.DetectFormat() == FormatSplit {
		if _, err := os.Stat(loader.projectPath()); err == nil {
			return ErrAlreadyMigrated
		}
		return ErrNotFound
	}

	// 步骤 2：读 legacy 合并态与 log_rules，并据处置决定算出机器层内容。
	// 此刻 DetectFormat 仍是 legacy，两次读取都落在 config.yaml 上。
	project, err := loader.Load()
	if err != nil {
		return fmt.Errorf("migration step 2 (load legacy config): %w", err)
	}
	rules, err := loader.LoadLogRules()
	if err != nil {
		return fmt.Errorf("migration step 2 (load legacy log_rules): %w", err)
	}
	local := buildLocalLayer(project, decisions)

	// 步骤 3：机器层落盘。此时 project.yaml 还不存在，格式仍是 legacy，config.yaml
	// 原封不动——崩在这里等于什么都没发生，重跑即可。
	if err := saveLocal(rootPath, local); err != nil {
		return fmt.Errorf("migration step 3 (write local.yaml): %w", err)
	}
	log.Printf("[SuperDev] config: migration wrote local.yaml keys=%d", localKeyCount(local))

	// 步骤 4：UI 状态搬去 agent 本地 store。同样在翻转点之前，可重跑。
	if err := uiStore.ReplaceEnvSelected(rootPath, project.EnvSelectedServiceIDs); err != nil {
		return fmt.Errorf("migration step 4 (write ui state): %w", err)
	}

	// 步骤 5：写 project.yaml —— 格式翻转点。传的是合并态 project 而非删过键的
	// 副本：splitOwnership 会照步骤 3 落下的 local.yaml 把归机器层的键剥离，
	// 剥离逻辑因此只有一份（Save 与迁移共用），不会两处分叉。
	if err := loader.saveSplitWithRules(project, rules); err != nil {
		return fmt.Errorf("migration step 5 (write project.yaml): %w", err)
	}
	log.Printf("[SuperDev] config: migration wrote project.yaml services=%d", len(project.Services))

	// 步骤 6：旧文件改名为备份。os.Rename 在同目录内是原子的，不存在"备份写了一半"
	// 的中间态；崩在这一步之前只是两个文件并存，DetectFormat 已判 split 且会告警。
	if err := os.Rename(loader.legacyPath(), loader.legacyPath()+".bak"); err != nil {
		return fmt.Errorf("migration step 6 (backup config.yaml): %w", err)
	}
	log.Printf("[SuperDev] config: migration backed up config.yaml -> config.yaml.bak")

	// 步骤 7：改写 .gitignore。纯 git 可见性问题，失败不影响配置可读。
	removed, added, err := rewriteGitignore(rootPath)
	if err != nil {
		return fmt.Errorf("migration step 7 (rewrite .gitignore): %w", err)
	}
	log.Printf("[SuperDev] config: migration updated .gitignore removed=%d added=%d", removed, added)

	log.Printf("[SuperDev] config: migration completed project=%s", rootPath)
	return nil
}

// buildLocalLayer 依据疑似密钥扫描结果与人的处置决定，算出应写入机器层的键值。
//
// 参数：
//   - p: legacy 加载出的合并态 Project（值都还在里面）
//   - decisions: 人对疑似项的处置列表
//
// 返回：
//   - localYAML：归机器层的 variables 与 per-deployment env_vars
//
// 注意：
//   - 未被显式判为 shared 的疑似项一律去本机层。这不是保守，是代价不对称：
//     一个值错留本机，成本是人手动搬一次；一个值错发共享层并入库，成本是一次
//     泄露且无法撤回。
//   - 只有 scanSuspects 认定的疑似项参与拆分。decisions 里指向非疑似键的条目
//     被忽略——decisions 是「对疑似项的处置」，不是任意搬迁指令。
func buildLocalLayer(p model.Project, decisions []MigrationDecision) localYAML {
	// 复用 preview 的同一份扫描：人在对话框里看到的疑似清单，与这里实际拆分的
	// 集合必须逐条对得上，否则会出现「预览没提示、迁移却把它搬走了」。
	suspects := map[string]bool{}
	for _, s := range scanSuspects(p) {
		suspects[decisionKey(s.Scope, s.Service, s.Env, s.Key)] = true
	}
	dispositions := map[string]string{}
	for _, d := range decisions {
		dispositions[decisionKey(d.Scope, d.Service, d.Env, d.Key)] = d.Disposition
	}
	goesLocal := func(scope, service, env, key string) bool {
		k := decisionKey(scope, service, env, key)
		return suspects[k] && dispositions[k] != dispositionShared
	}

	lf := localYAML{}
	for key, val := range p.Variables {
		if !goesLocal("variables", "", "", key) {
			continue
		}
		if lf.Variables == nil {
			lf.Variables = map[string]string{}
		}
		lf.Variables[key] = val
	}

	for _, svc := range p.Services {
		for _, dep := range svc.Deployments {
			for key, val := range dep.Env {
				if !goesLocal("env_vars", svc.Name, dep.EnvName, key) {
					continue
				}
				// overrideKey 与 mergeLocal/splitOwnership 用的是同一个函数，
				// 机器层的键才能在下一次 Load 时被认回同一个 deployment。
				k := overrideKey(svc, dep.EnvName)
				o := lf.Deployments[k]
				if o.EnvVars == nil {
					o.EnvVars = map[string]string{}
				}
				o.EnvVars[key] = val
				if lf.Deployments == nil {
					lf.Deployments = map[string]depOverrideYAML{}
				}
				lf.Deployments[k] = o
			}
		}
	}
	return lf
}

// decisionKey 把「作用域 + 服务 + 环境 + 键名」压成一个可比较的查表键。
// 用 \x00 分隔而非常见的 "/"：服务名与环境名都可能含有 "/"，用可打印字符
// 分隔会让 ("a/b", "c") 和 ("a", "b/c") 撞成同一个键。
func decisionKey(scope, service, env, key string) string {
	return scope + "\x00" + service + "\x00" + env + "\x00" + key
}

// localKeyCount 统计机器层承载的键总数（variables + 各 deployment 的 env_vars），
// 仅用于迁移日志里报一个可核对的数量。
func localKeyCount(lf localYAML) int {
	n := len(lf.Variables)
	for _, o := range lf.Deployments {
		n += len(o.EnvVars)
	}
	return n
}

// scanSuspects 扫描 p.Variables 与各 service 每个 deployment 的 Env，返回
// 疑似密钥清单（值已脱敏）。debug_credentials 不在扫描范围内——那是刻意
// 公开的测试凭据，属共享层，去留已由用户在别处裁决，不属于「疑似密钥」
// 的问题域。
//
// 独立成可复用的内部函数：Task 7 的 ApplyMigration 需要用同一份扫描结果
// 决定「未被人显式处置的疑似项默认去本机层」，若两处各自重写一份正则匹配
// 逻辑，改动只同步一边迟早会分叉出行为不一致的 bug。
func scanSuspects(p model.Project) []SuspectEntry {
	// 用 []SuspectEntry{} 而非 var out []SuspectEntry：干净项目（没有疑似密钥）
	// 是最常见的一档结果，nil 切片经 encoding/json 编码成 "null"，desktop 端
	// 一旦对 suspects 做 .map(...) 之类操作就会崩——必须让「零结果」也序列化成
	// 空数组 "[]"，而不是空值 "null"。
	out := []SuspectEntry{}

	for _, key := range sortedKeys(p.Variables) {
		val := p.Variables[key]
		if reason, ok := suspectReason(key, val); ok {
			out = append(out, SuspectEntry{
				Scope:  "variables",
				Key:    key,
				Masked: maskValue(val),
				Reason: reason,
			})
		}
	}

	for _, svc := range p.Services {
		for _, dep := range svc.Deployments {
			for _, key := range sortedKeys(dep.Env) {
				val := dep.Env[key]
				if reason, ok := suspectReason(key, val); ok {
					out = append(out, SuspectEntry{
						Scope:   "env_vars",
						Service: svc.Name,
						Env:     dep.EnvName,
						Key:     key,
						Masked:  maskValue(val),
						Reason:  reason,
					})
				}
			}
		}
	}

	return out
}

// suspectReason 判断 key/val 是否疑似密钥，命中时给出人可读的判定理由。
// 键名优先判断：键名命中比值前缀命中更常见也更可靠，两者都命中时理由取
// 键名侧的判定。
func suspectReason(key, val string) (string, bool) {
	if suspectKeyRe.MatchString(key) {
		return "键名疑似密钥", true
	}
	if suspectValRe.MatchString(val) {
		return "值前缀疑似密钥", true
	}
	return "", false
}

// sortedKeys 返回 map 的键的确定顺序列表。map 迭代顺序在 Go 中是随机的，
// 迁移预览这种要展示给人看、且要在两次调用间保持稳定的列表，顺序抖动会让
// 人怀疑扫描结果本身是不是也不稳定。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectRelativizedPaths 收集迁移会把哪些绝对路径转成相对路径（仅预览，
// 不做实际转换）。遍历每个 deployment 的 WorkDir/EnvFile，以及 Runtime 的
// WorkingDir/EnvFile/CWD 三个路径字段；只收集「当前是绝对路径，且相对化
// 后结果确实会变」的原值——RelativizePath 对 root 外部的绝对路径按设计
// 原样保留，那类路径不该出现在「将变化」的清单里误导用户。
func collectRelativizedPaths(p model.Project, rootPath string) []string {
	// 同 scanSuspects：干净项目（没有需要相对化的绝对路径）也要序列化成 "[]"
	// 而非 "null"，与 uiStateEnvs 已有的空切片约定保持一致。
	out := []string{}
	collect := func(path string) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		if RelativizePath(path, rootPath) != path {
			out = append(out, path)
		}
	}

	for _, svc := range p.Services {
		for _, dep := range svc.Deployments {
			collect(dep.WorkDir)
			collect(dep.EnvFile)
			if dep.Runtime != nil {
				collect(dep.Runtime.WorkingDir)
				collect(dep.Runtime.EnvFile)
				collect(dep.Runtime.CWD)
			}
		}
	}

	return out
}

// uiStateEnvs 返回 p.EnvSelectedServiceIDs 的键（环境名）排序列表——split
// 格式下该字段已迁移为 agent 本地 UI 状态（见 uistate.go），这里只列出
// legacy 配置里出现过哪些环境，供迁移预览展示「将搬去本地 store 的环境」。
func uiStateEnvs(p model.Project) []string {
	envs := make([]string, 0, len(p.EnvSelectedServiceIDs))
	for env := range p.EnvSelectedServiceIDs {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	return envs
}

// legacyGitignoreLines 是历史上可能出现的、忽略整个 .superdev/ 目录的写法
// ——迁移后共享层要入库，这些整目录忽略必须撤掉，否则 project.yaml 永远
// 进不了 git。
var legacyGitignoreLines = []string{".superdev/", ".superdev", "/.superdev/", "/.superdev"}

// addedGitignoreLines 是迁移后应当忽略的机器层文件与迁移备份。
var addedGitignoreLines = []string{".superdev/local.yaml", ".superdev/config.yaml.bak"}

// gitignoreDiff 读取 <rootPath>/.gitignore（不存在视为空文件），算出一份
// diff：RemoveLines 是文件里已存在、需要撤掉的整目录忽略行；AddLines 是
// 尚不存在、需要补上的机器层/备份忽略行。是 diff 而非重写——不触碰用户
// 写的其他忽略规则（如 node_modules/）。
func gitignoreDiff(rootPath string) GitignoreChange {
	existing := gitignoreLineSet(rootPath)

	// RemoveLines/AddLines 显式初始化为空切片而非 nil：.gitignore 已经是目标
	// 状态（该撤的整目录忽略已经没有，该加的机器层/备份行已经都在）是完全
	// 正常的一档结果，此时两个字段都不该序列化成 "null"。
	change := GitignoreChange{RemoveLines: []string{}, AddLines: []string{}}
	for _, line := range legacyGitignoreLines {
		if existing[line] {
			change.RemoveLines = append(change.RemoveLines, line)
		}
	}
	for _, line := range addedGitignoreLines {
		if !existing[line] {
			change.AddLines = append(change.AddLines, line)
		}
	}
	return change
}

// gitignoreBlockHeader 是迁移追加的忽略块抬头——让人在自己的 .gitignore 里
// 一眼看出这几行是谁加的、为什么加。
const gitignoreBlockHeader = "# SuperDev 机器层配置与迁移备份（不入库）"

// rewriteGitignore 按 gitignoreDiff 的结论就地改写项目根 .gitignore：精确移除
// 整目录忽略行，在末尾补上尚不存在的机器层/备份忽略行，其余行（含空行与注释）
// 原样保留。
//
// 参数：
//   - rootPath: 项目根目录
//
// 返回：
//   - removed: 实际被删掉的行数
//   - added: 实际追加的行数
//   - error: 读写 .gitignore 的 I/O 错误
//
// 注意：
//   - 是改写而非重建。用户自己的忽略规则（node_modules/ 之类）与迁移无关，
//     被迁移顺手吃掉是无法接受的副作用。
//   - 文件不存在按空文件处理：项目此前没有 .gitignore 是正常状态，此时会
//     新建一个只含机器层忽略块的文件。
//   - 无增无删时直接返回，不触碰文件——避免仅仅因为跑了一次迁移就在 git
//     里制造一个纯格式化的 diff。
func rewriteGitignore(rootPath string) (removed, added int, err error) {
	// 复用预览用的 gitignoreDiff：preview 给人看的增删清单与 apply 实际执行的
	// 动作必须来自同一处判定，否则「预览说要删 A，实际删了 B」这类偏差没有任何
	// 机制能发现。
	change := gitignoreDiff(rootPath)
	if len(change.RemoveLines) == 0 && len(change.AddLines) == 0 {
		return 0, 0, nil
	}

	path := filepath.Join(rootPath, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, 0, fmt.Errorf("read .gitignore: %w", err)
	}

	drop := map[string]bool{}
	for _, line := range change.RemoveLines {
		drop[line] = true
	}

	kept := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		// 与 gitignoreLineSet 用同一套裁剪规则（只去尾部 \r），保证 diff 判定
		// 命中的行在这里一定也命中。
		if drop[strings.TrimRight(line, "\r")] {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	// strings.Split 对以换行结尾的内容会多出一个空尾元素。先摘掉它、最后统一补
	// 一个换行：既不会吃掉用户写在中间的空行，也保证结果始终以换行收尾（原文件
	// 无尾换行时顺带补上）。
	if n := len(kept); n > 0 && kept[n-1] == "" {
		kept = kept[:n-1]
	}

	if len(change.AddLines) > 0 {
		// 与用户既有规则之间空一行，纯可读性；原文件本就以空行收尾时不再叠加。
		if n := len(kept); n > 0 && kept[n-1] != "" {
			kept = append(kept, "")
		}
		kept = append(kept, gitignoreBlockHeader)
		kept = append(kept, change.AddLines...)
		added = len(change.AddLines)
	}

	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		return 0, 0, fmt.Errorf("write .gitignore: %w", err)
	}
	return removed, added, nil
}

// gitignoreLineSet 读取 .gitignore 并按非空行去重成集合，便于 gitignoreDiff
// 做存在性判断。文件不存在视为空——项目此前没有 .gitignore 是正常状态，
// 不应被当作错误向上传播。
func gitignoreLineSet(rootPath string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(rootPath, ".gitignore"))
	if err != nil {
		return map[string]bool{}
	}
	lines := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		lines[line] = true
	}
	return lines
}
