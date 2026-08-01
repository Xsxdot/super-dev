// migrate.go —— legacy config.yaml → split 双层格式的迁移。
//
// 职责：
//   - BuildMigrationPlan：读 legacy 配置，产出预览（疑似密钥、路径相对化
//     清单、UI 状态去向、.gitignore 变更）
//   - ApplyMigration：按用户处置决定落两层 + uistate + 备份 + gitignore
//     （Task 7 落地，本文件目前只有 preview 半部）
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

// scanSuspects 扫描 p.Variables 与各 service 每个 deployment 的 Env，返回
// 疑似密钥清单（值已脱敏）。debug_credentials 不在扫描范围内——那是刻意
// 公开的测试凭据，属共享层，去留已由用户在别处裁决，不属于「疑似密钥」
// 的问题域。
//
// 独立成可复用的内部函数：Task 7 的 ApplyMigration 需要用同一份扫描结果
// 决定「未被人显式处置的疑似项默认去本机层」，若两处各自重写一份正则匹配
// 逻辑，改动只同步一边迟早会分叉出行为不一致的 bug。
func scanSuspects(p model.Project) []SuspectEntry {
	var out []SuspectEntry

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
	var out []string
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

	change := GitignoreChange{}
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
