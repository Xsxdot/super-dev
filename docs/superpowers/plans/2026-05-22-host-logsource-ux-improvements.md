# Host & LogSource UX Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 优化主机管理和远程监听页面 UX：移除 SSH config 导入、改进主机表单（默认 root、SSH 密钥检测、标签式 tags、测试连接）、改进监听任务表单（中文表头、独立 tags、命令预览+安全参数）。

**Architecture:** 后端新增两个 API（`POST /api/hosts/test-connection` 和 `GET /api/hosts/detect-ssh-keys`），同时给 `LogSource` 模型加 `Tags` 和 `ExtraArgs` 字段；前端抽取可复用 `TagInput.vue` 组件，分别改造 `HostFormModal.vue` 和 `LogSourceFormModal.vue`，并清除 SSH config 导入相关代码。

**Tech Stack:** Go 1.22（`golang.org/x/crypto/ssh`）、Vue 3 + TypeScript、Tauri（`@tauri-apps/plugin-dialog`）、Vitest

---

## 文件改动总览

### 后端（`agent/`）
| 文件 | 改动 |
|------|------|
| `model/model.go` | `LogSource` 增加 `Tags []string` + `ExtraArgs []string` |
| `collector/command.go` | `BuildCommand` 增加 `extraArgs []string` 参数，白名单校验后追加 |
| `collector/command_test.go` | 增加 extra args 相关测试 |
| `api/handler_hosts.go` | 新增 `testConnection`、`detectSshKeys` handler |
| `api/handler_hosts_test.go` | 新增两个 handler 的集成测试 |
| `api/server.go` | 注册两条新路由 |

### 前端（`desktop/src/`）
| 文件 | 改动 |
|------|------|
| `components/Settings/TagInput.vue` | **新建**：可复用的标签输入组件 |
| `components/Settings/HostFormModal.vue` | 使用 TagInput、默认 root、密钥检测、测试连接 |
| `components/Settings/HostManagerTab.vue` | 移除导入按钮、移除 SshConfigImportModal 引用、表头中文化 |
| `components/Sidebar/LogSourceFormModal.vue` | 使用 TagInput、中文表头、tags 字段、命令预览+安全参数 |
| `api/agent.ts` | 新增两个 API 函数、`LogSource` 类型加 `tags/extra_args`、`LogSourceCreatePayload` 加同字段、新增 `TestConnectionPayload`/`TestConnectionResult` 类型、新增 `detectSshKeys` 接口 |
| `stores/remote.ts` | `createLogSource`/`updateLogSource` 透传新字段 |
| `components/Settings/__tests__/HostManagerTab.test.ts` | 移除导入相关断言、加表头中文断言 |

---

## Task 1：后端 — `LogSource` 模型增加 Tags 和 ExtraArgs

**Files:**
- Modify: `agent/model/model.go`

- [ ] **Step 1: 修改 LogSource 结构体**

在 `agent/model/model.go` 中，将 `LogSource` 结构体改为：

```go
// LogSource 表示一个监听任务：在哪些 Host 上以何种 type 采集哪个 name。
//
// Tags 是监听任务自身的标签，与关联 Host 的 Tags 无关。
// ExtraArgs 是追加给采集命令的额外参数（白名单校验后追加），如 ["--since", "1h"]。
type LogSource struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Type      LogSourceType `json:"type"`
	HostIDs   []string      `json:"host_ids"`
	Tags      []string      `json:"tags"`
	ExtraArgs []string      `json:"extra_args"`
}
```

- [ ] **Step 2: 运行现有测试确认无回归**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./model/... -v
```

Expected: PASS（无测试失败）

- [ ] **Step 3: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add agent/model/model.go
git commit -m "feat(model): add Tags and ExtraArgs to LogSource"
```

---

## Task 2：后端 — `BuildCommand` 支持 ExtraArgs 白名单

**Files:**
- Modify: `agent/collector/command.go`
- Modify: `agent/collector/command_test.go`

- [ ] **Step 1: 写失败测试**

在 `agent/collector/command_test.go` 中，在现有测试之后追加：

```go
func TestBuildCommandExtraArgs(t *testing.T) {
	// 合法的 extra args 正常追加
	argv, err := BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h"})
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager", "--since", "1h"}, argv)

	// docker 同样支持
	argv, err = BuildCommand(model.LogSourceTypeDocker, "nova-api", []string{"--tail", "100"})
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "nova-api", "--tail", "100"}, argv)

	// 非法 arg（无 -- 前缀）被拒绝
	_, err = BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"rm"})
	assert.Error(t, err)

	// 非法 arg（含注入字符）被拒绝
	_, err = BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h; rm -rf /"})
	assert.Error(t, err)

	// 空 extra args 不影响结果
	argv, err = BuildCommand(model.LogSourceTypeJournalctl, "nova-api", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager"}, argv)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./collector/... -run TestBuildCommandExtraArgs -v
```

Expected: FAIL（`BuildCommand` 签名不匹配）

- [ ] **Step 3: 修改 `BuildCommand` 签名和实现**

将 `agent/collector/command.go` 中的 `BuildCommand` 替换为：

```go
// argRegex 限制每个额外参数只允许安全字符：字母、数字、-、_、/、.、: 和空格。
// 参数名必须以 -- 开头（长选项）或 - 开头（短选项）。
var argRegex = regexp.MustCompile(`^(-{1,2}[a-zA-Z][a-zA-Z0-9-]*)$|^([a-zA-Z0-9._/:@-]{1,64})$`)

// ErrInvalidArg 表示 extraArgs 中某个参数含非法字符或格式不符合要求。
var ErrInvalidArg = errors.New("invalid extra arg: only safe flag/value characters allowed")

// BuildCommand 按 type 模板组合 argv，name 作为参数（不进 shell 解析）。
//
// 参数：
//   - t: 采集类型，必须在 LogSourceType 枚举内
//   - name: 校验通过的服务名/容器名
//   - extraArgs: 额外追加参数，每个元素须通过 argRegex 校验
//
// 返回：
//   - argv 切片，调用方用 exec.Command(argv[0], argv[1:]...) 执行
//   - type 不支持、name 非法或 extraArgs 含非法字符时返回错误
func BuildCommand(t model.LogSourceType, name string, extraArgs []string) ([]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	for _, arg := range extraArgs {
		if !argRegex.MatchString(arg) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidArg, arg)
		}
	}
	var base []string
	switch t {
	case model.LogSourceTypeJournalctl:
		base = []string{"journalctl", "-fu", name, "-o", "cat", "--no-pager"}
	case model.LogSourceTypeDocker:
		base = []string{"docker", "logs", "-f", name}
	default:
		return nil, ErrUnsupportedType
	}
	return append(base, extraArgs...), nil
}
```

- [ ] **Step 4: 修复调用方**

`BuildCommand` 签名改变，需要更新 `agent/collector/manager.go` 中的调用。找到调用处：

```bash
grep -n "BuildCommand" /Users/xushixin/workspace/super-debug/agent/collector/manager.go
```

将调用改为传入第三个参数 `nil`（manager 层不知道 extraArgs，由上层透传）：

在 `manager.go` 中，将 `BuildCommand(t, name)` 改为 `BuildCommand(t, name, nil)`。

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./collector/... -v
```

Expected: PASS（所有测试通过）

- [ ] **Step 6: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add agent/collector/command.go agent/collector/command_test.go agent/collector/manager.go
git commit -m "feat(collector): BuildCommand supports validated extra args"
```

---

## Task 3：后端 — 新增测试连接和密钥检测接口

**Files:**
- Modify: `agent/api/handler_hosts.go`
- Modify: `agent/api/handler_hosts_test.go`
- Modify: `agent/api/server.go`

- [ ] **Step 1: 写失败测试**

在 `agent/api/handler_hosts_test.go` 末尾追加（在现有 `package api_test` 中）：

```go
func TestDetectSshKeys(t *testing.T) {
	app := newTestApp(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/hosts/detect-ssh-keys", nil)
	app.Handler().ServeHTTP(w, r)
	// 路由存在即 200（home dir 无 .ssh 时返回空列表，不是 404）
	assert.Equal(t, http.StatusOK, w.Code)
	var result []string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	// result 可以为空列表，但必须是数组类型
	assert.NotNil(t, result)
}

func TestTestConnectionBadRequest(t *testing.T) {
	app := newTestApp(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/hosts/test-connection", strings.NewReader(`{invalid}`))
	r.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTestConnectionUnreachable(t *testing.T) {
	app := newTestApp(t)
	body := `{"ssh_host":"127.0.0.1","ssh_port":1,"ssh_user":"nobody","ssh_password":"x"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/hosts/test-connection", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
	var result struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.False(t, result.OK)
	assert.NotEmpty(t, result.Message)
}
```

确认 `handler_hosts_test.go` 头部 import 包含 `"strings"`（如没有则加上）。

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./api/... -run "TestDetectSshKeys|TestTestConnection" -v
```

Expected: FAIL（路由不存在，返回 404 或编译错误）

- [ ] **Step 3: 实现两个 handler**

在 `agent/api/handler_hosts.go` 末尾追加（保留文件头注释）：

```go
// testConnectionRequest 是 POST /api/hosts/test-connection 的请求体。
type testConnectionRequest struct {
	SSHHost    string `json:"ssh_host"`
	SSHPort    int    `json:"ssh_port"`
	SSHUser    string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	SSHKeyPath  string `json:"ssh_key_path"`
}

// testConnectionResult 是 POST /api/hosts/test-connection 的响应体。
type testConnectionResult struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
}

// testConnection 处理 POST /api/hosts/test-connection。
//
// 尝试用提供的凭据建立 SSH 连接并立即断开，返回成功/失败及延迟。
func (a *App) testConnection(w http.ResponseWriter, r *http.Request) {
	var req testConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SSHHost == "" || req.SSHUser == "" {
		jsonError(w, http.StatusBadRequest, "ssh_host and ssh_user are required")
		return
	}
	port := req.SSHPort
	if port == 0 {
		port = 22
	}

	creds := tunnel.Credentials{
		User:     req.SSHUser,
		Password: req.SSHPassword,
	}
	if req.SSHKeyPath != "" {
		key, err := tunnel.ReadPrivateKey(expandHome(req.SSHKeyPath))
		if err != nil {
			jsonOK(w, testConnectionResult{OK: false, Message: "读取私钥失败: " + err.Error()})
			return
		}
		creds.PrivateKey = key
	}

	cfg, err := tunnel.BuildClientConfig(creds)
	if err != nil {
		jsonOK(w, testConnectionResult{OK: false, Message: err.Error()})
		return
	}

	addr := fmt.Sprintf("%s:%d", req.SSHHost, port)
	start := time.Now()
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		jsonOK(w, testConnectionResult{OK: false, Message: err.Error()})
		return
	}
	_ = client.Close()
	jsonOK(w, testConnectionResult{
		OK:        true,
		Message:   "连接成功",
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// detectSshKeys 处理 GET /api/hosts/detect-ssh-keys。
//
// 扫描 ~/.ssh/ 目录，返回看起来是私钥（无 .pub 后缀）的文件路径列表。
func (a *App) detectSshKeys(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		jsonOK(w, []string{})
		return
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		// .ssh 不存在或无权限：返回空列表而非错误
		jsonOK(w, []string{})
		return
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// 跳过 .pub 公钥、known_hosts、authorized_keys、config
		if strings.HasSuffix(name, ".pub") ||
			name == "known_hosts" ||
			name == "authorized_keys" ||
			name == "config" {
			continue
		}
		keys = append(keys, filepath.Join("~/.ssh", name))
	}
	if keys == nil {
		keys = []string{}
	}
	jsonOK(w, keys)
}

// expandHome 将路径中的 ~ 展开为实际 home 目录。
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
```

在 `handler_hosts.go` 顶部 import 块中确保包含：

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
	"github.com/superdev/agent/tunnel"
)
```

- [ ] **Step 4: 在 server.go 注册新路由**

在 `agent/api/server.go` 的 `Handler()` 方法中，在「远程主机管理」注释块下加两行（注意：**必须在 `POST /api/hosts` 之前**，否则具体路径被通配符覆盖）：

```go
// 远程主机管理
mux.HandleFunc("GET /api/hosts/detect-ssh-keys", a.detectSshKeys)
mux.HandleFunc("POST /api/hosts/test-connection", a.testConnection)
mux.HandleFunc("GET /api/hosts", a.listHosts)
mux.HandleFunc("POST /api/hosts", a.createHost)
mux.HandleFunc("PUT /api/hosts/{id}", a.updateHost)
mux.HandleFunc("DELETE /api/hosts/{id}", a.deleteHost)
```

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./api/... -run "TestDetectSshKeys|TestTestConnection" -v
```

Expected: PASS

- [ ] **Step 6: 运行全量后端测试**

```bash
cd /Users/xushixin/workspace/super-debug/agent
go test ./... 2>&1 | tail -20
```

Expected: 所有包 PASS，无编译错误

- [ ] **Step 7: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add agent/api/handler_hosts.go agent/api/handler_hosts_test.go agent/api/server.go
git commit -m "feat(api): add test-connection and detect-ssh-keys endpoints"
```

---

## Task 4：前端 — 更新 API 类型定义

**Files:**
- Modify: `desktop/src/api/agent.ts`

- [ ] **Step 1: 更新类型定义**

在 `desktop/src/api/agent.ts` 中，做以下修改：

**1. 更新 `LogSource` 接口：**

```typescript
export interface LogSource {
  id: string
  name: string
  type: LogSourceType
  host_ids: string[]
  tags: string[]
  extra_args: string[]
}
```

**2. 更新 `LogSourceCreatePayload` 接口：**

```typescript
export interface LogSourceCreatePayload {
  name: string
  type: LogSourceType
  host_ids: string[]
  tags?: string[]
  extra_args?: string[]
}
```

**3. 在 `HostCreatePayload` 之后，追加两个新接口：**

```typescript
export interface TestConnectionPayload {
  ssh_host: string
  ssh_port: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
}

export interface TestConnectionResult {
  ok: boolean
  message: string
  latency_ms?: number
}
```

**4. 在 `api` 对象的「远程监听：Host CRUD」注释块下追加两个函数（在 `listHosts` 之前）：**

```typescript
  // 远程监听：Host 辅助操作
  detectSshKeys: () => request<string[]>('/api/hosts/detect-ssh-keys'),
  testConnection: (payload: TestConnectionPayload) =>
    request<TestConnectionResult>('/api/hosts/test-connection', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
```

- [ ] **Step 2: 确认 TypeScript 类型正确**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx tsc --noEmit 2>&1 | head -30
```

Expected: 无错误输出（或仅有与本次改动无关的已有警告）

- [ ] **Step 3: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/api/agent.ts
git commit -m "feat(api): add TestConnection, detectSshKeys types and API calls"
```

---

## Task 5：前端 — 新建 TagInput 组件

**Files:**
- Create: `desktop/src/components/Settings/TagInput.vue`

- [ ] **Step 1: 创建 TagInput.vue**

创建文件 `desktop/src/components/Settings/TagInput.vue`：

```vue
<!--
TagInput：标签式多值输入组件。

职责：
  - 已添加的 tag 以彩色 chip 形式展示，点击 × 可删除
  - 输入框内按 Enter 或逗号触发添加，自动 trim 并去重
  - 通过 v-model 双向绑定 tags 数组

边界：
  - 不负责持久化，只管理当前表单状态
  - 颜色由 tagColor 工具函数决定，与 HostManagerTab 保持一致
-->
<script setup lang="ts">
import { ref } from 'vue'
import { tagColor } from '@/lib/tagColor'

const props = defineProps<{ modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()

const input = ref('')

function addTag(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed || props.modelValue.includes(trimmed)) return
  emit('update:modelValue', [...props.modelValue, trimmed])
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    addTag(input.value)
    input.value = ''
  }
}

function onBlur() {
  if (input.value.trim()) {
    addTag(input.value)
    input.value = ''
  }
}

function removeTag(tag: string) {
  emit('update:modelValue', props.modelValue.filter(t => t !== tag))
}
</script>

<template>
  <div class="tag-input">
    <span
      v-for="tag in modelValue"
      :key="tag"
      class="chip"
      :style="{ background: tagColor(tag) }"
    >
      {{ tag }}
      <button class="remove" type="button" @click="removeTag(tag)">×</button>
    </span>
    <input
      v-model="input"
      class="tag-text"
      placeholder="输入后按 Enter 添加"
      @keydown="onKeydown"
      @blur="onBlur"
    />
  </div>
</template>

<style scoped>
.tag-input {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  align-items: center;
  min-height: 32px;
  padding: 4px 6px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 6px;
  color: #fff;
  border-radius: 2px;
  font-size: 10px;
}
.remove {
  padding: 0;
  color: inherit;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  opacity: 0.7;
}
.remove:hover {
  opacity: 1;
}
.tag-text {
  flex: 1;
  min-width: 80px;
  padding: 0 2px;
  color: var(--text-primary);
  background: transparent;
  border: none;
  font-size: 12px;
  outline: none;
}
</style>
```

- [ ] **Step 2: 运行前端单元测试确认无回归**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run 2>&1 | tail -20
```

Expected: 所有测试 PASS

- [ ] **Step 3: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Settings/TagInput.vue
git commit -m "feat(ui): add reusable TagInput component"
```

---

## Task 6：前端 — 改造 HostFormModal

**Files:**
- Modify: `desktop/src/components/Settings/HostFormModal.vue`

- [ ] **Step 1: 完整替换 HostFormModal.vue**

用以下内容替换 `desktop/src/components/Settings/HostFormModal.vue`：

```vue
<!--
HostFormModal：单 Host 新建与编辑表单。

职责：
  - 收集 Host 的 SSH、远端 agent 端口和 tag 字段
  - ssh_user 新建时默认 root
  - ssh_key_path 提供浏览和自动检测两种入口
  - 提供测试连接入口，展示完整错误信息
  - 将表单 payload 交由父组件保存

边界：
  - 不直接调用远程 API（测试连接除外）
  - 不负责 SSH config 批量导入
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { open as openDialog } from '@tauri-apps/plugin-dialog'
import { api } from '@/api/agent'
import type { Host, HostCreatePayload } from '@/api/agent'
import TagInput from './TagInput.vue'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const keyOptions = ref<string[]>([])
const showKeyDropdown = ref(false)
const testResult = ref<{ ok: boolean; message: string; latency_ms?: number } | null>(null)
const testing = ref(false)

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_password: '',
    ssh_key_path: '',
    remote_agent_port: 57017,
    tags: [],
  }
}

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    testResult.value = null
    keyOptions.value = []
    showKeyDropdown.value = false
    if (initial) {
      form.value = {
        name: initial.name,
        ssh_host: initial.ssh_host,
        ssh_port: initial.ssh_port,
        ssh_user: initial.ssh_user,
        ssh_password: initial.ssh_password ?? '',
        ssh_key_path: initial.ssh_key_path ?? '',
        remote_agent_port: initial.remote_agent_port,
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

async function browseKey() {
  const selected = await openDialog({ multiple: false, title: '选择 SSH 私钥文件' })
  if (selected && !Array.isArray(selected)) {
    form.value.ssh_key_path = selected
  }
}

async function detectKeys() {
  keyOptions.value = await api.detectSshKeys()
  showKeyDropdown.value = keyOptions.value.length > 0
  if (keyOptions.value.length === 0) {
    testResult.value = { ok: false, message: '未在 ~/.ssh/ 找到私钥文件' }
  }
}

function selectKey(path: string) {
  form.value.ssh_key_path = path
  showKeyDropdown.value = false
}

async function testConn() {
  testing.value = true
  testResult.value = null
  try {
    const result = await api.testConnection({
      ssh_host: form.value.ssh_host,
      ssh_port: form.value.ssh_port ?? 22,
      ssh_user: form.value.ssh_user,
      ssh_password: form.value.ssh_password,
      ssh_key_path: form.value.ssh_key_path,
    })
    testResult.value = result
  } catch (err) {
    testResult.value = { ok: false, message: err instanceof Error ? err.message : '请求失败' }
  } finally {
    testing.value = false
  }
}

function submit() {
  emit('submit', { ...form.value })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑主机' : '新建主机' }}</div>

      <div class="field">
        <label>名称 <span class="req">*</span></label>
        <input v-model="form.name" placeholder="nova-api-prod-01" data-test="host-form-name" />
      </div>

      <div class="row">
        <div class="field flex">
          <label>SSH 地址 <span class="req">*</span></label>
          <input v-model="form.ssh_host" placeholder="10.0.0.1" data-test="host-form-host" />
        </div>
        <div class="field port">
          <label>端口</label>
          <input v-model.number="form.ssh_port" type="number" min="1" data-test="host-form-port" />
        </div>
      </div>

      <div class="field">
        <label>SSH 用户 <span class="req">*</span></label>
        <input v-model="form.ssh_user" placeholder="root" data-test="host-form-user" />
      </div>

      <div class="field">
        <label>SSH 密码</label>
        <input v-model="form.ssh_password" type="password" placeholder="留空则用密钥" data-test="host-form-password" />
      </div>

      <div class="field">
        <label>SSH 私钥路径</label>
        <div class="row tight">
          <input v-model="form.ssh_key_path" placeholder="~/.ssh/id_ed25519" data-test="host-form-key" />
          <button type="button" @click="browseKey" data-test="host-form-browse">浏览</button>
          <button type="button" @click="detectKeys" data-test="host-form-detect">检测</button>
        </div>
        <div v-if="showKeyDropdown" class="key-dropdown">
          <div
            v-for="k in keyOptions"
            :key="k"
            class="key-option"
            @click="selectKey(k)"
          >{{ k }}</div>
        </div>
      </div>

      <div class="field">
        <label>远端 Agent 端口</label>
        <input v-model.number="form.remote_agent_port" type="number" min="1" data-test="host-form-agent-port" />
      </div>

      <div class="field">
        <label>标签</label>
        <TagInput v-model="form.tags!" data-test="host-form-tags" />
      </div>

      <div class="warn">密码会以明文存储在本机 hosts 配置文件中，请优先使用密钥。</div>

      <div class="test-conn">
        <button type="button" :disabled="testing" data-test="host-form-test" @click="testConn">
          {{ testing ? '测试中…' : '测试连接' }}
        </button>
        <span v-if="testResult" :class="testResult.ok ? 'ok' : 'fail'" class="test-msg">
          {{ testResult.ok
            ? `连接成功（${testResult.latency_ms}ms）`
            : testResult.message }}
        </span>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button type="button" class="primary" @click="submit" data-test="host-form-submit">保存</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal-body {
  width: min(480px, calc(100vw - 32px));
  max-height: 86vh;
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 12px;
  font-size: 14px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  margin-bottom: 10px;
  position: relative;
}
.field label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req { color: var(--status-failed); }
.field input {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.row { display: flex; gap: 8px; }
.row.tight { gap: 4px; }
.row.tight input, .field.flex { flex: 1; }
.field.port { width: 86px; }
.key-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  z-index: 10;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
  max-height: 140px;
  overflow-y: auto;
}
.key-option {
  padding: 5px 8px;
  font-size: 11px;
  font-family: var(--font-mono, monospace);
  cursor: pointer;
}
.key-option:hover { background: var(--bg-secondary); }
.warn {
  margin: 12px 0 8px;
  color: var(--status-failed);
  font-size: 11px;
}
.test-conn {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.test-msg {
  font-size: 11px;
  word-break: break-all;
}
.test-msg.ok { color: var(--status-ok, #3fb950); }
.test-msg.fail { color: var(--status-failed); }
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
button {
  padding: 5px 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
  font-size: 12px;
}
button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
button:disabled { cursor: not-allowed; opacity: 0.5; }
</style>
```

- [ ] **Step 2: 运行前端测试**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run 2>&1 | tail -20
```

Expected: PASS（现有测试未对 HostFormModal 内部结构做深度断言，不应失败）

- [ ] **Step 3: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Settings/HostFormModal.vue
git commit -m "feat(ui): HostFormModal — default root, key detect, tag chips, test-connection"
```

---

## Task 7：前端 — 改造 HostManagerTab（移除导入、表头中文化）

**Files:**
- Modify: `desktop/src/components/Settings/HostManagerTab.vue`
- Modify: `desktop/src/components/Settings/__tests__/HostManagerTab.test.ts`

- [ ] **Step 1: 检查现有测试中与 SSH 导入相关的断言**

```bash
grep -n "import\|SshConfig\|host-import" /Users/xushixin/workspace/super-debug/desktop/src/components/Settings/__tests__/HostManagerTab.test.ts
```

记下需要删除的行号。

- [ ] **Step 2: 更新 HostManagerTab.vue**

将 `HostManagerTab.vue` 中以下部分删除/替换：

**删除 import 行（script 部分）：**
```typescript
// 删除：
import SshConfigImportModal from './SshConfigImportModal.vue'
import type { Host, HostCreatePayload, SshConfigEntry } from '@/api/agent'

// 替换为：
import type { Host, HostCreatePayload } from '@/api/agent'
```

**删除 ref 和函数：**
- 删除 `const importVisible = ref(false)`
- 删除 `handleImport` 函数

**更新模板表头（将英文 th 改为中文）：**
```html
<thead>
  <tr>
    <th>名称</th>
    <th>连接地址</th>
    <th>标签</th>
    <th>隧道</th>
    <th></th>
  </tr>
</thead>
```

**删除模板中的导入按钮：**
```html
<!-- 删除这行 -->
<button data-test="host-import" @click="importVisible = true">从 SSH config 导入</button>
```

**删除模板中的 SshConfigImportModal 组件：**
```html
<!-- 删除这段 -->
<SshConfigImportModal
  :visible="importVisible"
  @import="handleImport"
  @cancel="importVisible = false"
/>
```

**更新 empty 提示文本（去掉导入提示）：**
```html
<div v-else class="empty">还没有主机，点击新建主机开始。</div>
```

- [ ] **Step 3: 更新测试文件，删除导入相关断言**

在 `HostManagerTab.test.ts` 中，找到引用 `host-import`、`SshConfigImportModal` 或 `handleImport` 的断言行，将其删除。

- [ ] **Step 4: 运行前端测试**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run --reporter verbose 2>&1 | grep -E "PASS|FAIL|Error"
```

Expected: 所有测试 PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Settings/HostManagerTab.vue \
        desktop/src/components/Settings/__tests__/HostManagerTab.test.ts
git commit -m "feat(ui): HostManagerTab — remove SSH config import, Chinese table headers"
```

---

## Task 8：前端 — 改造 LogSourceFormModal

**Files:**
- Modify: `desktop/src/components/Sidebar/LogSourceFormModal.vue`

- [ ] **Step 1: 完整替换 LogSourceFormModal.vue**

用以下内容替换 `desktop/src/components/Sidebar/LogSourceFormModal.vue`：

```vue
<!--
LogSourceFormModal：远程监听任务新建与编辑表单。

职责：
  - 收集 LogSource 的任务名称、采集类型、关联 host_ids、tags 和 extra_args
  - 根据 name + type 实时预览采集命令
  - 支持安全参数配置（固定参数名 + 可编辑值）
  - 将 payload 交由父组件保存

边界：
  - 不直接调用 API
  - 命令预览为纯前端拼接，不请求后端
  - extra_args 只支持预定义的安全参数集合，防止命令注入
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import TagInput from '@/components/Settings/TagInput.vue'
import type { LogSource, LogSourceCreatePayload, LogSourceType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: LogSource | null
}>()

const emit = defineEmits<{
  submit: [payload: LogSourceCreatePayload]
  cancel: []
}>()

const store = useRemoteStore()
const name = ref('')
const type = ref<LogSourceType>('journalctl')
const hostIds = ref<Set<string>>(new Set())
const tags = ref<string[]>([])

// 安全参数定义
interface SafeParam {
  flag: string          // 如 --since
  enabled: boolean
  value: string
  valueType: 'input' | 'select'
  options?: string[]    // select 时的选项
  types: LogSourceType[] // 哪些 type 支持此参数
}

const safeParams = ref<SafeParam[]>([
  {
    flag: '--since',
    enabled: false,
    value: '1h',
    valueType: 'input',
    types: ['journalctl', 'docker'],
  },
  {
    flag: '--priority',
    enabled: false,
    value: 'err',
    valueType: 'select',
    options: ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'],
    types: ['journalctl'],
  },
  {
    flag: '--output',
    enabled: false,
    value: 'cat',
    valueType: 'select',
    options: ['cat', 'json', 'short', 'verbose'],
    types: ['journalctl'],
  },
  {
    flag: '--tail',
    enabled: false,
    value: '100',
    valueType: 'input',
    types: ['docker'],
  },
])

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    // 重置安全参数
    safeParams.value.forEach(p => { p.enabled = false; p.value = defaultValue(p) })
    if (initial) {
      name.value = initial.name
      type.value = initial.type
      hostIds.value = new Set(initial.host_ids)
      tags.value = [...(initial.tags ?? [])]
      // 从 extra_args 还原参数状态
      restoreFromExtraArgs(initial.extra_args ?? [])
      return
    }
    name.value = ''
    type.value = 'journalctl'
    hostIds.value = new Set()
    tags.value = []
  },
  { immediate: true },
)

function defaultValue(p: SafeParam): string {
  if (p.flag === '--since') return '1h'
  if (p.flag === '--priority') return 'err'
  if (p.flag === '--output') return 'cat'
  if (p.flag === '--tail') return '100'
  return ''
}

function restoreFromExtraArgs(args: string[]) {
  for (let i = 0; i < args.length; i++) {
    const param = safeParams.value.find(p => p.flag === args[i])
    if (param) {
      param.enabled = true
      if (i + 1 < args.length && !args[i + 1].startsWith('-')) {
        param.value = args[i + 1]
        i++
      }
    }
  }
}

const visibleParams = computed(() =>
  safeParams.value.filter(p => p.types.includes(type.value)),
)

const extraArgs = computed<string[]>(() => {
  const args: string[] = []
  for (const p of visibleParams.value) {
    if (p.enabled) {
      args.push(p.flag)
      if (p.value) args.push(p.value)
    }
  }
  return args
})

const previewCommand = computed(() => {
  const n = name.value.trim() || '<任务名称>'
  let base: string[]
  if (type.value === 'journalctl') {
    base = ['journalctl', '-fu', n, '-o', 'cat', '--no-pager']
  } else {
    base = ['docker', 'logs', '-f', n]
  }
  return [...base, ...extraArgs.value].join(' ')
})

const canSubmit = computed(() => name.value.trim().length > 0 && hostIds.value.size > 0)

function toggleHost(id: string) {
  const next = new Set(hostIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  hostIds.value = next
}

function submit() {
  emit('submit', {
    name: name.value.trim(),
    type: type.value,
    host_ids: Array.from(hostIds.value),
    tags: tags.value,
    extra_args: extraArgs.value,
  })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑监听任务' : '新建监听任务' }}</div>

      <div class="field">
        <label>任务名称 <span class="req">*</span></label>
        <input v-model="name" placeholder="nova-api" data-test="logsource-form-name" />
      </div>

      <div class="field">
        <label>采集类型</label>
        <select v-model="type" data-test="logsource-form-type">
          <option value="journalctl">journalctl</option>
          <option value="docker">docker</option>
        </select>
      </div>

      <div class="field">
        <label>关联主机 <span class="req">*</span></label>
        <div v-if="store.hosts.length > 0" class="host-list">
          <label v-for="host in store.hosts" :key="host.id" class="host-row" data-test="logsource-form-host">
            <input type="checkbox" :checked="hostIds.has(host.id)" @change="toggleHost(host.id)" />
            <span class="hname">{{ host.name }}</span>
            <span class="tags">{{ host.tags.join(', ') || '(无标签)' }}</span>
          </label>
        </div>
        <div v-else class="empty">还没有主机。请先到设置页添加。</div>
      </div>

      <div class="field">
        <label>标签</label>
        <TagInput v-model="tags" data-test="logsource-form-tags" />
      </div>

      <div v-if="name.trim()" class="field">
        <label>命令预览</label>
        <code class="cmd-preview">{{ previewCommand }}</code>
      </div>

      <div v-if="name.trim()" class="field">
        <label>安全参数</label>
        <div class="params">
          <div v-for="param in visibleParams" :key="param.flag" class="param-row">
            <input
              :id="`param-${param.flag}`"
              v-model="param.enabled"
              type="checkbox"
            />
            <label :for="`param-${param.flag}`" class="param-flag">{{ param.flag }}</label>
            <template v-if="param.enabled">
              <input
                v-if="param.valueType === 'input'"
                v-model="param.value"
                class="param-val"
                :placeholder="defaultValue(param)"
              />
              <select v-else v-model="param.value" class="param-val">
                <option v-for="opt in param.options" :key="opt" :value="opt">{{ opt }}</option>
              </select>
            </template>
          </div>
        </div>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="!canSubmit"
          data-test="logsource-form-submit"
          @click="submit"
        >
          保存
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal-body {
  width: min(480px, calc(100vw - 32px));
  max-height: 86vh;
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 10px;
  font-size: 14px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  margin-bottom: 12px;
}
.field > label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req { color: var(--status-failed); }
.field input,
.field select {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.host-list {
  max-height: 200px;
  overflow-y: auto;
  border: 1px solid var(--border-secondary);
}
.host-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px;
  cursor: pointer;
  font-size: 12px;
}
.host-row:hover { background: var(--bg-secondary); }
.hname { font-weight: 600; }
.tags, .empty {
  color: var(--text-tertiary);
  font-size: 11px;
}
.empty { padding: 12px; text-align: center; }
.cmd-preview {
  display: block;
  padding: 6px 8px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  word-break: break-all;
  white-space: pre-wrap;
}
.params {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px 8px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
}
.param-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.param-flag {
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  color: var(--text-secondary);
  min-width: 80px;
}
.param-val {
  flex: 1;
  padding: 2px 6px;
  font-size: 11px;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
button {
  padding: 5px 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
  font-size: 12px;
}
button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
button:disabled { cursor: not-allowed; opacity: 0.5; }
</style>
```

- [ ] **Step 2: 运行前端测试**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run 2>&1 | tail -20
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/LogSourceFormModal.vue
git commit -m "feat(ui): LogSourceFormModal — Chinese labels, tags, command preview, safe params"
```

---

## Task 9：前端 — 更新 remote store 透传新字段

**Files:**
- Modify: `desktop/src/stores/remote.ts`

- [ ] **Step 1: 查看 remote store 当前的 createLogSource/updateLogSource**

```bash
grep -n "createLogSource\|updateLogSource\|LogSourceCreatePayload" /Users/xushixin/workspace/super-debug/desktop/src/stores/remote.ts
```

- [ ] **Step 2: 确认透传**

`LogSourceCreatePayload` 类型已在 Task 4 中加了 `tags` 和 `extra_args` 字段，store 调用 `api.createLogSource(payload)` 时会自动透传，无需额外修改。

运行测试确认：

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run --reporter verbose 2>&1 | grep -E "remote|PASS|FAIL"
```

Expected: PASS

- [ ] **Step 3: 全量测试**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx vitest run 2>&1 | tail -10
```

Expected: 所有测试 PASS

- [ ] **Step 4: TypeScript 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
npx tsc --noEmit 2>&1 | head -20
```

Expected: 无错误

- [ ] **Step 5: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/stores/remote.ts
git commit -m "chore: verify remote store passes LogSource tags and extra_args through"
```

---

## 自检清单

### Spec 覆盖检查

| 需求 | 对应 Task |
|------|-----------|
| 1. 移除 SSH config 导入 | Task 7 |
| 2. 新建主机默认 root + SSH 密钥检测 | Task 3（后端）+ Task 6（前端）|
| 3. tags 标签形式 | Task 5（TagInput）+ Task 6（主机）+ Task 8（监听）|
| 4. 表头中文（主机管理） | Task 7 |
| 5. 测试连接按钮+完整错误展示 | Task 3（后端）+ Task 6（前端）|
| 6. 监听任务独立 tags | Task 1（模型）+ Task 4（API 类型）+ Task 8（前端）|
| 7. 监听任务表头中文 | Task 8 |
| 8. 命令预览+安全参数（固定参数名+可编辑值） | Task 2（后端）+ Task 8（前端）|

所有需求均有对应 Task，无遗漏。
