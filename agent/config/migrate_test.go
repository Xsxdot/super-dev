package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/config"
)

// legacyFixture 是一份典型的 legacy 单文件配置：有疑似密钥（变量与 env_vars
// 各一）、有固化的绝对 working_dir、有已迁走的 UI 状态、有 log_rules，也有
// 刻意公开的 debug_credentials。
//
// debug_credentials 必须用 model.DebugCredential 的真实 schema（name/value/
// desc）书写：yaml.v3 对结构体外的键静默丢弃，写成 account/password 这类不
// 存在的字段，值根本进不了内存，"凭据随共享层入库" 这条断言就会验在空气上。
const legacyFixture = `
name: tk
variables:
  PUBLIC_URL: http://localhost
  CLIENT_SECRET: real-secret-value
debug_credentials:
  - name: password
    value: Money8888
    desc: 测试账号密码
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        working_dir: %s
        env_vars:
          PORT: "9100"
          OPENAI_API_KEY: sk-abc123def456
env_selected_service_ids:
  dev: [server]
log_rules:
  - id: r1
`

func TestBuildMigrationPlan(t *testing.T) {
	dir := t.TempDir()
	// working_dir 用绝对路径构造固化现场
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, filepath.Join(dir, "server")))
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/\nnode_modules/\n")

	plan, err := config.BuildMigrationPlan(dir)
	assert.NoError(t, err)
	assert.Equal(t, 1, plan.ServiceCount)
	assert.Equal(t, []string{"dev"}, plan.UIStateEnvs)
	assert.Len(t, plan.RelativizedPaths, 1, "绝对 working_dir 进相对化清单")

	// 疑似密钥：CLIENT_SECRET（键名命中）+ OPENAI_API_KEY（键名+sk- 值双命中）
	keys := map[string]bool{}
	for _, s := range plan.Suspects {
		keys[s.Key] = true
		assert.NotContains(t, s.Masked, "real-secret-value", "预览必须脱敏")
		assert.NotContains(t, s.Masked, "sk-abc123def456")
	}
	assert.True(t, keys["CLIENT_SECRET"])
	assert.True(t, keys["OPENAI_API_KEY"])
	assert.False(t, keys["PORT"], "普通变量不进疑似清单")
	assert.False(t, keys["password"], "debug_credentials 属共享层，不扫描（用户裁决）")

	// gitignore：撤掉整目录忽略，改为只忽略机器层与备份
	assert.Contains(t, plan.Gitignore.RemoveLines, ".superdev/")
	assert.Contains(t, plan.Gitignore.AddLines, ".superdev/local.yaml")
	assert.Contains(t, plan.Gitignore.AddLines, ".superdev/config.yaml.bak")
}

// cleanLegacyFixture 是一份「没有任何要提醒的东西」的 legacy 配置：没有
// 疑似密钥键名/值前缀，没有 working_dir（因此内存态里没有绝对路径），
// .gitignore 已经是迁移后的目标状态。用来盯住 BuildMigrationPlan 在
// "全干净" 这一档结果下是否仍然正确——尤其是切片字段是否仍然是空数组
// 而不是 JSON 里的 null（nil 切片经 encoding/json 编码就是 "null"）。
const cleanLegacyFixture = `
name: clean
variables:
  PUBLIC_URL: http://localhost
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        command: go run ./cmd/server
        env_vars:
          PORT: "9100"
env_selected_service_ids:
  dev: [server]
`

func TestBuildMigrationPlanCleanProjectMarshalsEmptySlicesNotNull(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, cleanLegacyFixture)
	// .gitignore 已经是迁移后的目标状态：机器层/备份行都在，整目录忽略不在。
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/local.yaml\n.superdev/config.yaml.bak\nnode_modules/\n")

	plan, err := config.BuildMigrationPlan(dir)
	assert.NoError(t, err)

	// Go 值层面：确实什么都没扫到。
	assert.Empty(t, plan.Suspects)
	assert.Empty(t, plan.RelativizedPaths)
	assert.Empty(t, plan.Gitignore.RemoveLines)
	assert.Empty(t, plan.Gitignore.AddLines)

	// 只断言 Go 值 empty 不够——nil 切片和 make([]T, 0) 在 assert.Empty 下
	// 表现相同，但序列化结果天差地别（"null" vs "[]"），desktop 端对
	// suspects 之类字段做 .map(...) 会因为 null 直接崩。必须落到编码后的
	// JSON 字符串上验证。
	encoded, err := json.Marshal(plan)
	assert.NoError(t, err)
	body := string(encoded)
	assert.Contains(t, body, `"suspects":[]`)
	assert.Contains(t, body, `"relativized_paths":[]`)
	assert.Contains(t, body, `"remove_lines":[]`)
	assert.Contains(t, body, `"add_lines":[]`)
	assert.NotContains(t, body, "null")
}

func TestBuildMigrationPlanAlreadySplit(t *testing.T) {
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", "name: demo\nservices: []\n")
	_, err := config.BuildMigrationPlan(dir)
	assert.ErrorIs(t, err, config.ErrAlreadyMigrated)
}

func TestApplyMigration(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, filepath.Join(dir, "server")))
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/\nnode_modules/\n")
	uiStore := config.NewUIStateStore(t.TempDir())

	decisions := []config.MigrationDecision{
		{Scope: "variables", Key: "CLIENT_SECRET", Disposition: "local"},
		{Scope: "env_vars", Service: "server", Env: "dev", Key: "OPENAI_API_KEY", Disposition: "local"},
	}
	assert.NoError(t, config.ApplyMigration(dir, decisions, uiStore))

	// 1. project.yaml：相对路径、无密钥、无 UI 状态、保留 log_rules 与 debug_credentials
	proj, _ := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	s := string(proj)
	assert.Contains(t, s, "working_dir: server")
	assert.NotContains(t, s, dir)
	assert.NotContains(t, s, "real-secret-value")
	assert.NotContains(t, s, "sk-abc123def456")
	assert.NotContains(t, s, "env_selected_service_ids")
	assert.Contains(t, s, "log_rules")
	assert.Contains(t, s, "Money8888", "debug_credentials 留在共享层")

	// 2. local.yaml：密钥入本机层
	loc, _ := os.ReadFile(filepath.Join(dir, ".superdev", "local.yaml"))
	assert.Contains(t, string(loc), "real-secret-value")
	assert.Contains(t, string(loc), "sk-abc123def456")

	// 3. UI 状态入 store
	assert.Equal(t, []string{"server"}, uiStore.EnvSelected(dir)["dev"])

	// 4. 备份与 gitignore
	_, err := os.Stat(filepath.Join(dir, ".superdev", "config.yaml.bak"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, ".superdev", "config.yaml"))
	assert.True(t, os.IsNotExist(err))
	// 按行断言而非子串断言：整目录忽略行是否真的被移除，是这一步唯一要紧的事
	// ——`.superdev/` 还在，project.yaml 就永远进不了 git，整个分层特性直接失效。
	lines := gitignoreLines(t, dir)
	assert.NotContains(t, lines, ".superdev/", "整目录忽略行已移除")
	assert.Contains(t, lines, ".superdev/local.yaml")
	assert.Contains(t, lines, ".superdev/config.yaml.bak")
	assert.Contains(t, lines, "node_modules/", "无关行原样保留")

	// 5. 迁移后 Load 语义等价：合并态与迁移前一致
	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	assert.Equal(t, "split", p.ConfigFormat)
	assert.Equal(t, "real-secret-value", p.Variables["CLIENT_SECRET"])
	assert.Equal(t, "sk-abc123def456", p.Services[0].Deployments[0].Env["OPENAI_API_KEY"])
	assert.Equal(t, "9100", p.Services[0].Deployments[0].Env["PORT"])

	// 6. 幂等：重跑报 ErrAlreadyMigrated
	assert.ErrorIs(t, config.ApplyMigration(dir, nil, uiStore), config.ErrAlreadyMigrated)
}

func TestApplyMigrationSharedDisposition(t *testing.T) {
	// 用户选择 CLIENT_SECRET 留共享层（不挡、只亮的另一半）
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, "server"))
	uiStore := config.NewUIStateStore(t.TempDir())
	decisions := []config.MigrationDecision{
		{Scope: "variables", Key: "CLIENT_SECRET", Disposition: "shared"},
		{Scope: "env_vars", Service: "server", Env: "dev", Key: "OPENAI_API_KEY", Disposition: "local"},
	}
	assert.NoError(t, config.ApplyMigration(dir, decisions, uiStore))
	proj, _ := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	assert.Contains(t, string(proj), "real-secret-value", "用户明选 shared 则尊重")
}

// runtimeEnvVarsFixture 把密钥放在 runtime.env_vars 且不给顶层 env_vars。
// 这种写法下 deploymentsFromYAML 会让 dep.Env 直接别名到 Runtime.EnvVars，
// 两者是同一个 map——共享层若只清 dep.Env，明文仍会随 runtime 块入库。
const runtimeEnvVarsFixture = `
name: rt
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        runtime:
          type: language
          cwd: server
          env_vars:
            OPENAI_API_KEY: sk-abc123def456
            PORT: "9100"
`

// runtimeEnvFixture 把密钥放在 runtime.env——language runtime 下真正生效的
// 环境变量载体（EffectiveEnv 优先于 EnvVars），且它从未被别名进 dep.Env。
const runtimeEnvFixture = `
name: rt
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        runtime:
          type: language
          cwd: server
          env:
            DB_PASSWORD: hunter2-plaintext
            LOG_LEVEL: debug
`

func TestApplyMigrationMovesRuntimeEnvVarsSecretOutOfSharedLayer(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, runtimeEnvVarsFixture)
	uiStore := config.NewUIStateStore(t.TempDir())

	// decisions 为空：全部未决，按安全默认去本机层。
	assert.NoError(t, config.ApplyMigration(dir, nil, uiStore))

	proj := readSuperdevFile(t, dir, "project.yaml")
	assert.NotContains(t, proj, "sk-abc123def456", "runtime.env_vars 里的密钥不得留在入库文件")
	assert.Contains(t, proj, "9100", "非密钥留共享层")
	loc := readSuperdevFile(t, dir, "local.yaml")
	assert.Contains(t, loc, "sk-abc123def456", "密钥入机器层")

	// 语义等价：真正拉起进程的一侧读的是 Runtime.EffectiveEnv()，密钥必须在那里
	// 可见——只并回 dep.Env 等于让服务丢了这个变量。
	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	rt := p.Services[0].Deployments[0].Runtime
	assert.NotNil(t, rt)
	assert.Equal(t, "sk-abc123def456", rt.EffectiveEnv()["OPENAI_API_KEY"], "机器层密钥必须并回 runtime 生效载体")
	assert.Equal(t, "9100", rt.EffectiveEnv()["PORT"], "共享层非密钥变量不受影响")
	assert.Equal(t, "sk-abc123def456", p.Services[0].Deployments[0].Env["OPENAI_API_KEY"])
}

func TestApplyMigrationMovesRuntimeEnvSecretOutOfSharedLayer(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, runtimeEnvFixture)

	// 预览必须先看见它——用户对没被亮出来的键无从做处置决定。
	plan, err := config.BuildMigrationPlan(dir)
	assert.NoError(t, err)
	suspectKeys := map[string]bool{}
	for _, s := range plan.Suspects {
		suspectKeys[s.Key] = true
		assert.NotContains(t, s.Masked, "hunter2-plaintext", "预览必须脱敏")
	}
	assert.True(t, suspectKeys["DB_PASSWORD"], "runtime.env 里的疑似密钥必须进预览清单")

	uiStore := config.NewUIStateStore(t.TempDir())
	assert.NoError(t, config.ApplyMigration(dir, nil, uiStore))

	proj := readSuperdevFile(t, dir, "project.yaml")
	assert.NotContains(t, proj, "hunter2-plaintext", "runtime.env 里的密钥不得留在入库文件")
	assert.Contains(t, proj, "debug", "非密钥留共享层")
	loc := readSuperdevFile(t, dir, "local.yaml")
	assert.Contains(t, loc, "hunter2-plaintext", "密钥入机器层")

	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	rt := p.Services[0].Deployments[0].Runtime
	assert.NotNil(t, rt)
	assert.Equal(t, "hunter2-plaintext", rt.EffectiveEnv()["DB_PASSWORD"], "机器层密钥必须并回 runtime 生效载体")
	assert.Equal(t, "debug", rt.EffectiveEnv()["LOG_LEVEL"], "共享层非密钥变量不受影响")
}

// TestApplyMigrationStripsSecretFromEveryEnvCarrier 盯的是最隐蔽的一种泄露：
// 同一个键同时躺在 runtime.env 与 runtime.env_vars 里。只清生效的那一个，
// 剥空后的 map 会被 yaml omitempty 整个省略，下次 Load 时 EffectiveEnv()
// 回落到另一个载体，把陈旧的明文密钥又复活出来——而且是在入库文件里。
func TestApplyMigrationStripsSecretFromEveryEnvCarrier(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
name: rt
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        runtime:
          type: language
          env:
            API_TOKEN: effective-token
          env_vars:
            API_TOKEN: shadowed-token
`)
	uiStore := config.NewUIStateStore(t.TempDir())
	assert.NoError(t, config.ApplyMigration(dir, nil, uiStore))

	proj := readSuperdevFile(t, dir, "project.yaml")
	assert.NotContains(t, proj, "effective-token", "生效载体里的密钥必须剥离")
	assert.NotContains(t, proj, "shadowed-token", "被遮蔽载体里的明文同样会入库，必须一并剥离")

	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	rt := p.Services[0].Deployments[0].Runtime
	assert.NotNil(t, rt)
	assert.Equal(t, "effective-token", rt.EffectiveEnv()["API_TOKEN"], "并回的是迁移前实际生效的那个值")
}

// gitignoreLines 把项目根 .gitignore 读成行列表（去掉尾部空行），供做
// 「某一行是否存在」的精确断言——子串断言在这里会漏判。
func gitignoreLines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	assert.NoError(t, err)
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// readSuperdevFile 读取 dir/.superdev/<name> 并返回字符串内容。
func readSuperdevFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".superdev", name))
	assert.NoError(t, err)
	return string(data)
}

// mustWriteFile 写入任意绝对路径文件（不局限于 .superdev/ 目录），仿
// loader_test.go 里的 mustWriteSuperdev，用于给迁移测试构造项目根
// .gitignore 等现场文件。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
