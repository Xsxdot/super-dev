# 项目配置编辑器 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把一次性的、只读为主的项目配置流程，重构成可反复进入的「项目配置编辑器」——支持全新项目从零引导、已有项目随时增删 env/service、编辑 deployment（local/remote/env 变量/pipeline）。

**Architecture:** 后端把 `PUT /api/projects/{id}/setup` 升级为全量项目配置接口（按 service ID diff 做增删改，删除运行中 service 被拒），并拆分「探测目录 / 创建项目」让新建项目在保存成功前无副作用。前端新增 `ProjectConfigEditor.vue` 及一组子组件，全程本地草稿编辑、保存时全量提交。

**Tech Stack:** Go（标准库 net/http + httptest 测试）、Vue 3 `<script setup>` + Pinia + Vitest + @vue/test-utils。

参考 spec：`docs/superpowers/specs/2026-05-29-project-config-editor-design.md`

---

## 文件结构

**后端：**
- `agent/api/handler_vscode.go` — `setupRequest`/`setupServiceEntry` 升级 + `putProjectSetup` diff 逻辑（增删改 + 删除运行中守卫）
- `agent/api/handler_projects.go` — 新增 `probeProject`（探测目录）handler；`addProject` 保持「保存才落地」语义
- `agent/api/server.go` — 注册 `GET /api/projects/probe` 路由
- `agent/api/handler_vscode_test.go`、`agent/api/handler_projects_test.go` — 测试

**前端：**
- `desktop/src/api/agent.ts` — 升级 `SetupServiceEntry`/`SetupDeployment` 类型、新增 `Pipeline`/`Step` 类型、新增 `probeProject` 接口
- `desktop/src/lib/configDraft.ts`（新建）— 草稿模型 + 拍平为 payload 的纯函数 + 校验纯函数
- `desktop/src/lib/__tests__/configDraft.test.ts`（新建）
- `desktop/src/components/Settings/EnvKeyValueEditor.vue`（新建）+ 测试
- `desktop/src/components/Settings/PipelineEditor.vue`（新建）+ 测试
- `desktop/src/components/Settings/DeploymentForm.vue`（新建）
- `desktop/src/components/Settings/ServiceCard.vue`（新建）
- `desktop/src/components/Settings/ServiceList.vue`（新建）
- `desktop/src/components/Settings/EnvTabBar.vue`（新建）
- `desktop/src/components/Settings/LaunchImportPanel.vue`（新建，迁移 `matchLaunchToService`）
- `desktop/src/components/Settings/ProjectConfigEditor.vue`（新建）+ 测试
- `desktop/src/pages/SettingsPage.vue` — 入口按钮改「编辑配置」常驻 + 新建流程
- `desktop/src/stores/agent.ts` — `addProject` 改为「探测+保存」、`createProject`
- 删除 `desktop/src/components/Settings/ProjectSetupModal.vue` 及其测试

---

## 后端

### Task 1: `PUT /setup` 升级 setupServiceEntry 结构（新增 name/required/order）

**Files:**
- Modify: `agent/api/handler_vscode.go:51-61`
- Test: `agent/api/handler_vscode_test.go`

- [ ] **Step 1: 写失败测试 —— 新增 service（空 ID 由后端分配）**

加到 `agent/api/handler_vscode_test.go` 末尾：

```go
// TestPutProjectSetup_AddsNewService 验证 setup 可新增一个 ID 为空的 service，
// 后端分配 ID 并持久化 name/required/order。
func TestPutProjectSetup_AddsNewService(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	webSvcID := created.Services[0].ID

	setupBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services": []map[string]any{
			{"id": webSvcID, "name": "web", "required": false, "order": 0, "deployments": []any{}},
			{"id": "", "name": "worker", "required": true, "order": 1, "deployments": []map[string]any{
				{"env_name": "dev", "location": "local", "command": "go run ./worker"},
			}},
		},
	})
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))
	require.Len(t, updated.Services, 2)
	var worker *model.Service
	for i := range updated.Services {
		if updated.Services[i].Name == "worker" {
			worker = &updated.Services[i]
		}
	}
	require.NotNil(t, worker, "worker service 应已新增")
	assert.NotEmpty(t, worker.ID, "新 service 应分配 ID")
	assert.True(t, worker.Required)
	assert.Equal(t, 1, worker.Order)
	require.Len(t, worker.Deployments, 1)
	assert.Equal(t, "go run ./worker", worker.Deployments[0].Command)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd agent && go test ./api/ -run TestPutProjectSetup_AddsNewService -v`
Expected: FAIL —— 现在 `setupServiceEntry` 无 name/required/order 字段，新增 service 不生效，`updated.Services` 仍是 1 个。

- [ ] **Step 3: 升级 setupServiceEntry 结构**

替换 `agent/api/handler_vscode.go:51-61`：

```go
// setupRequest 是 PUT /api/projects/{id}/setup 的请求体结构（全量项目配置）。
type setupRequest struct {
	Environments []model.Environment `json:"environments"`
	Services     []setupServiceEntry `json:"services"`
}

// setupServiceEntry 描述单个 service 的全量配置。
//
// ID 为空表示新增 service（后端分配 ID）；ID 存在表示更新；
// 现有 service 不在请求列表中则被删除（删除逻辑在 putProjectSetup）。
type setupServiceEntry struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Required    bool               `json:"required"`
	Order       int                `json:"order"`
	Deployments []model.Deployment `json:"deployments"`
}
```

- [ ] **Step 4: 实现 diff 逻辑（增改 + 保留删除留到 Task 2）**

替换 `agent/api/handler_vscode.go:105-117`（「按 service ID 替换 deployments」那段）为按请求重建 services 列表：

```go
	// 按请求重建 services：ID 命中现有则保留运行时无关字段并更新；ID 为空则新增。
	// 请求中不出现的现有 service 将被丢弃（删除）——删除运行中守卫见下方。
	existing := map[string]model.Service{}
	for _, s := range a.projects[idx].Services {
		existing[s.ID] = s
	}

	newServices := make([]model.Service, 0, len(req.Services))
	for _, entry := range req.Services {
		deps := entry.Deployments
		if deps == nil {
			deps = []model.Deployment{}
		}
		svc := existing[entry.ID] // ID 为空时为零值 Service（新增）
		svc.ID = entry.ID
		svc.Name = entry.Name
		svc.Required = entry.Required
		svc.Order = entry.Order
		svc.Deployments = deps
		newServices = append(newServices, svc)
	}
	a.projects[idx].Services = newServices
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd agent && go test ./api/ -run TestPutProjectSetup -v`
Expected: PASS（新增的 `TestPutProjectSetup_AddsNewService` 和原有 `TestPutProjectSetup_AppliesEnvironmentsAndDeployments` 都过）。

- [ ] **Step 6: 提交**

```bash
git add agent/api/handler_vscode.go agent/api/handler_vscode_test.go
git commit -m "feat(api): setup 接口支持新增 service（全量 services diff）"
```

---

### Task 2: 删除运行中 service 被拒守卫

**Files:**
- Modify: `agent/api/handler_vscode.go`（putProjectSetup，在重建 services 前加守卫）
- Test: `agent/api/handler_vscode_test.go`

- [ ] **Step 1: 写失败测试 —— 删除不存在于请求的 service 时，若它正在运行则返回 409**

加到 `agent/api/handler_vscode_test.go` 末尾。运行中状态通过 manager 注入，参考 `listServices` 用的 `mgr.IsActive`。先查清注入方式：测试里没有现成 helper，用真实启动代价高；改为验证「请求未含某 service 时该 service 被删除」的正常路径，运行中守卫用一个最小单元测试覆盖判定函数：

```go
// TestPutProjectSetup_DeletesAbsentService 验证请求中不出现的 service 被删除（未运行时）。
func TestPutProjectSetup_DeletesAbsentService(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Len(t, created.Services, 1)

	// 提交一份不含任何 service 的配置 —— web 应被删除
	setupBody, _ := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services":     []any{},
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))
	assert.Len(t, updated.Services, 0, "未出现在请求中的 service 应被删除")
}
```

- [ ] **Step 2: 跑测试确认通过（Task 1 的重建逻辑已实现删除）**

Run: `cd agent && go test ./api/ -run TestPutProjectSetup_DeletesAbsentService -v`
Expected: PASS —— Task 1 的 `newServices` 重建天然删除了缺席 service。本步确认删除路径正确。

- [ ] **Step 3: 加运行中守卫**

在 `putProjectSetup` 重建 services 之前（`a.projects[idx]` 已定位、仍持锁），插入守卫。先收集请求中保留的 service ID，再检查被删除的 service 是否运行中：

```go
	// 删除运行中 service 守卫：被删除（不在请求中）且正在运行的 service 拒绝删除。
	keepIDs := map[string]bool{}
	for _, entry := range req.Services {
		if entry.ID != "" {
			keepIDs[entry.ID] = true
		}
	}
	if mgr, ok := a.managers[id]; ok {
		for _, s := range a.projects[idx].Services {
			if !keepIDs[s.ID] && mgr.IsActive(s.ID) {
				a.mu.Unlock()
				jsonError(w, http.StatusConflict, "请先停止服务「"+s.Name+"」再删除")
				return
			}
		}
	}
```

放在 Task 1 的「按请求重建 services」代码块之前。

- [ ] **Step 4: 跑全部 setup 测试确认通过**

Run: `cd agent && go test ./api/ -run TestPutProjectSetup -v`
Expected: PASS（守卫不影响未运行场景）。

- [ ] **Step 5: 提交**

```bash
git add agent/api/handler_vscode.go agent/api/handler_vscode_test.go
git commit -m "feat(api): setup 删除运行中 service 时返回 409 守卫"
```

---

### Task 3: 探测目录接口（probeProject）支持新建项目零引导

**Files:**
- Modify: `agent/api/handler_projects.go`（新增 `probeProject`）
- Modify: `agent/api/server.go:164` 附近（注册路由）
- Test: `agent/api/handler_projects_test.go`（新建）

- [ ] **Step 1: 写失败测试 —— 探测无 config 目录返回空骨架，不登记**

新建 `agent/api/handler_projects_test.go`：

```go
// handler_projects_test.go 验证项目探测与创建分离的行为。
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

// TestProbeProject_EmptyDir 验证探测无 .superdev/config.yaml 的目录返回空骨架，
// 且不写注册表（GET /api/projects 仍为空）。
func TestProbeProject_EmptyDir(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()

	probeURL := srv.URL + "/api/projects/probe?root_path=" + url.QueryEscape(dir)
	resp, err := http.Get(probeURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var probed model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&probed))
	assert.Empty(t, probed.Services, "空目录应返回无 service")
	assert.NotEmpty(t, probed.Name, "Name 应取目录名")
	assert.Equal(t, dir, probed.RootPath)

	// 未登记：项目列表仍为空
	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	assert.Len(t, projects, 0, "探测不应登记项目")
}

// TestProbeProject_ExistingConfig 验证探测已有 config 的目录返回解析后的 project。
func TestProbeProject_ExistingConfig(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	probeURL := srv.URL + "/api/projects/probe?root_path=" + url.QueryEscape(dir)
	resp, err := http.Get(probeURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var probed model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&probed))
	assert.Equal(t, "myapp", probed.Name)
	require.Len(t, probed.Services, 1)
	assert.Equal(t, "web", probed.Services[0].Name)
}

// 防止 unused import（fmt/strings 在后续 create 测试中使用）
var _ = fmt.Sprintf
var _ = strings.NewReader
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd agent && go test ./api/ -run TestProbeProject -v`
Expected: FAIL —— 404，`/api/projects/probe` 路由不存在。

- [ ] **Step 3: 实现 probeProject handler**

在 `agent/api/handler_projects.go` 顶部确认已 import `errors`、`path/filepath`、`github.com/superdev/agent/config`（`config` 已用）。新增（放在 `addProject` 之后）：

```go
// probeProject 处理 GET /api/projects/probe?root_path=...。
//
// 探测目录是否已有 .superdev/config.yaml：
//   - 有：返回解析后的 project（含 service 列表）供编辑器预填
//   - 无：返回空骨架（Name 取目录名，environments/services 为空）
//
// 注意：探测不写注册表、不写 YAML、不进内存；真正落地在 createProject。
func (a *App) probeProject(w http.ResponseWriter, r *http.Request) {
	rootPath := r.URL.Query().Get("root_path")
	if rootPath == "" {
		jsonError(w, http.StatusBadRequest, "root_path is required")
		return
	}

	loader := config.NewLoader(rootPath)
	p, err := loader.Load()
	if errors.Is(err, config.ErrNotFound) {
		// 空目录：返回骨架，Name 取目录名
		jsonOK(w, model.Project{
			Name:         filepath.Base(rootPath),
			RootPath:     rootPath,
			Environments: []model.Environment{},
			Services:     []model.Service{},
		})
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to load project config: "+err.Error())
		return
	}
	assignIDs(&p)
	jsonOK(w, p)
}
```

- [ ] **Step 4: 注册路由**

在 `agent/api/server.go:166`（`DELETE /api/projects/{id}` 之后）加：

```go
	mux.HandleFunc("GET /api/projects/probe", a.probeProject)
```

注意：`GET /api/projects/probe` 与 `GET /api/projects` 不冲突（Go 1.22 ServeMux 按最长匹配路由）。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd agent && go test ./api/ -run TestProbeProject -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add agent/api/handler_projects.go agent/api/server.go agent/api/handler_projects_test.go
git commit -m "feat(api): 新增 probe 探测接口（新建项目零引导，探测不落地）"
```

---

### Task 4: createProject —— 保存成功才登记（新建项目落地）

现状 `addProject` 已经是「Load → assignIDs → Save → registry → 内存」。对新建项目，前端会先 `probe`（不落地），用户配好后调 `addProject` 落地一个空目录项目。需确认 `addProject` 对空目录的兜底：Load 空目录会返回 `ErrNotFound` 而报错。改为：空目录时用空骨架继续落地。

**Files:**
- Modify: `agent/api/handler_projects.go:63-68`（addProject 的 Load 错误处理）
- Test: `agent/api/handler_projects_test.go`

- [ ] **Step 1: 写失败测试 —— addProject 对空目录不报错，落地空项目**

加到 `agent/api/handler_projects_test.go`：

```go
// TestAddProject_EmptyDirCreatesSkeleton 验证 addProject 对无 config 的目录
// 不报错，落地一个空骨架项目（含 ID，写入注册表）。
func TestAddProject_EmptyDirCreatesSkeleton(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.NotEmpty(t, created.ID)
	assert.Empty(t, created.Services)

	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	assert.Len(t, projects, 1, "addProject 应落地项目")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd agent && go test ./api/ -run TestAddProject_EmptyDirCreatesSkeleton -v`
Expected: FAIL —— 400 "failed to load project config: ... ErrNotFound"。

- [ ] **Step 3: addProject 空目录兜底**

替换 `agent/api/handler_projects.go:63-68`：

```go
	loader := config.NewLoader(req.RootPath)
	p, err := loader.Load()
	if errors.Is(err, config.ErrNotFound) {
		// 空目录：落地空骨架项目，首次 Save 生成 config.yaml
		p = model.Project{
			Name:         filepath.Base(req.RootPath),
			RootPath:     req.RootPath,
			Environments: []model.Environment{},
			Services:     []model.Service{},
		}
	} else if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to load project config: "+err.Error())
		return
	}
```

确认文件已 import `errors` 和 `path/filepath`（Task 3 已加）。

- [ ] **Step 4: 跑测试确认通过 + 全量回归**

Run: `cd agent && go test ./api/ -v` then `cd agent && go test ./...`
Expected: PASS（全部 api 测试 + 全仓库测试）。

- [ ] **Step 5: 提交**

```bash
git add agent/api/handler_projects.go agent/api/handler_projects_test.go
git commit -m "feat(api): addProject 支持空目录落地（新建项目保存即生成 config）"
```

---

## 前端：类型与纯函数层

### Task 5: 升级 api 类型 + probeProject 接口 + Pipeline 类型

**Files:**
- Modify: `desktop/src/api/agent.ts`（SetupDeployment/SetupServiceEntry 升级、新增 Pipeline/Step、新增 probeProject）

- [ ] **Step 1: 新增 Pipeline/Step 类型并补全 Deployment.pipeline**

在 `desktop/src/api/agent.ts` 的 `Deployment` 接口（行 29-43）末尾、`status` 之前加 `pipeline?` 字段，并在文件类型区新增 Step/Pipeline。先在 `DeployLocation` 定义（行 27）之后插入：

```ts
export type StepScope = 'local' | 'fan-out'
export type StepAction = 'run' | 'sync'

export interface PipelineStep {
  id: string
  name: string
  scope: StepScope
  action: StepAction
  command?: string
  work_dir?: string
  sync_from?: string
  sync_to?: string
}

export interface Pipeline {
  steps: PipelineStep[]
}
```

在 `Deployment` 接口里 `stop_command?: string` 之后加：

```ts
  pipeline?: Pipeline
```

- [ ] **Step 2: 升级 SetupDeployment / SetupServiceEntry 类型**

替换 `desktop/src/api/agent.ts:193-210`（`SetupDeployment`/`SetupServiceEntry`/`SetupPayload`）：

```ts
export interface SetupDeployment {
  id?: string
  env_name: string
  location: 'local' | 'remote'
  command?: string
  work_dir?: string
  env?: Record<string, string>
  host_ids?: string[]
  log_type?: LogSourceType
  log_target?: string
  start_command?: string
  stop_command?: string
  pipeline?: Pipeline
}

export interface SetupServiceEntry {
  id: string
  name: string
  required: boolean
  order: number
  deployments: SetupDeployment[]
}

export interface SetupPayload {
  environments: Array<{ id?: string; name: string; is_dev: boolean; order: number }>
  services: SetupServiceEntry[]
}
```

- [ ] **Step 3: 新增 probeProject api 方法**

在 `desktop/src/api/agent.ts` 的 `api` 对象内，`addProject` 之后加：

```ts
  probeProject: (root_path: string) =>
    request<Project>(`/api/projects/probe?root_path=${encodeURIComponent(root_path)}`),
```

- [ ] **Step 4: 类型检查**

Run: `cd desktop && pnpm vue-tsc --noEmit`
Expected: PASS（无类型错误；ProjectSetupModal 仍在但其用法兼容，Task 13 才删）。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/api/agent.ts
git commit -m "feat(api-types): 升级 setup payload 类型 + 新增 Pipeline/probeProject"
```

---

### Task 6: configDraft —— 草稿模型 + 拍平 + 校验纯函数

这是编辑器的核心逻辑层，全部纯函数，便于单测。草稿即编辑器内部 state（深拷贝自 Project）。

**Files:**
- Create: `desktop/src/lib/configDraft.ts`
- Test: `desktop/src/lib/__tests__/configDraft.test.ts`

- [ ] **Step 1: 写失败测试 —— flatten 拍平 + validate 校验**

新建 `desktop/src/lib/__tests__/configDraft.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { projectToDraft, draftToPayload, validateDraft } from '@/lib/configDraft'
import type { Project } from '@/api/agent'

function makeProject(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [
      {
        id: 's1', project_id: 'p1', name: 'web', status: '', command: '', work_dir: '',
        required: false, order: 0,
        deployments: [
          { id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '/tmp/demo', env: { A: '1' }, status: '' },
        ],
      },
    ],
    selected_service_ids: [],
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
  }
}

describe('configDraft', () => {
  it('projectToDraft 深拷贝，修改草稿不影响原对象', () => {
    const p = makeProject()
    const draft = projectToDraft(p)
    draft.services[0].name = 'changed'
    expect(p.services[0].name).toBe('web')
  })

  it('draftToPayload 拍平为 SetupPayload，忽略空 key 的 env 变量', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].env = { A: '1', '': 'ignored' }
    const payload = draftToPayload(draft)
    expect(payload.environments).toHaveLength(1)
    expect(payload.services[0].name).toBe('web')
    expect(payload.services[0].deployments[0].env).toEqual({ A: '1' })
  })

  it('validateDraft：env 名称为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.environments[0].name = ''
    expect(validateDraft(draft)).toContain('环境名称不能为空')
  })

  it('validateDraft：service 名称重复报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services.push({ ...draft.services[0], id: 's2' })
    expect(validateDraft(draft).some(e => e.includes('服务名'))).toBe(true)
  })

  it('validateDraft：local deployment 命令为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].command = ''
    expect(validateDraft(draft).some(e => e.includes('命令'))).toBe(true)
  })

  it('validateDraft：remote deployment 未选 host 报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0] = {
      id: 'd1', env_name: 'dev', location: 'remote', host_ids: [], status: '',
    } as never
    expect(validateDraft(draft).some(e => e.includes('主机'))).toBe(true)
  })

  it('validateDraft：合法草稿返回空数组', () => {
    expect(validateDraft(projectToDraft(makeProject()))).toEqual([])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/lib/__tests__/configDraft.test.ts`
Expected: FAIL —— `@/lib/configDraft` 不存在。

- [ ] **Step 3: 实现 configDraft.ts**

新建 `desktop/src/lib/configDraft.ts`：

```ts
/**
 * 项目配置草稿模型与转换/校验纯函数。
 *
 * 职责：
 *   - projectToDraft：把 Project 深拷贝成可编辑草稿
 *   - draftToPayload：把草稿拍平为后端 SetupPayload（忽略空 key 的 env 变量）
 *   - validateDraft：保存前校验，返回错误信息数组（空数组 = 通过）
 *
 * 边界：
 *   - 纯数据转换，不发请求、不依赖 Vue
 */
import type { Project, Deployment, Environment, SetupPayload, SetupDeployment } from '@/api/agent'

export interface ConfigDraftService {
  id: string
  name: string
  required: boolean
  order: number
  deployments: Deployment[]
}

export interface ConfigDraft {
  environments: Environment[]
  services: ConfigDraftService[]
}

/** projectToDraft 把 Project 深拷贝成草稿，编辑草稿不影响原对象。 */
export function projectToDraft(p: Project): ConfigDraft {
  return {
    environments: (p.environments ?? []).map(e => ({ ...e })),
    services: (p.services ?? []).map(s => ({
      id: s.id,
      name: s.name,
      required: s.required,
      order: s.order,
      deployments: (s.deployments ?? []).map(d => structuredClone(d)),
    })),
  }
}

/** stripEmptyEnvKeys 过滤掉 key 为空的 env 变量。 */
function stripEmptyEnvKeys(env?: Record<string, string>): Record<string, string> | undefined {
  if (!env) return undefined
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(env)) {
    if (k.trim() !== '') out[k] = v
  }
  return Object.keys(out).length ? out : undefined
}

/** draftToPayload 把草稿拍平为后端 SetupPayload。 */
export function draftToPayload(draft: ConfigDraft): SetupPayload {
  return {
    environments: draft.environments.map(e => ({
      id: e.id || undefined,
      name: e.name,
      is_dev: e.is_dev,
      order: e.order,
    })),
    services: draft.services.map(s => ({
      id: s.id,
      name: s.name,
      required: s.required,
      order: s.order,
      deployments: s.deployments.map<SetupDeployment>(d => ({
        id: d.id || undefined,
        env_name: d.env_name,
        location: d.location,
        command: d.command,
        work_dir: d.work_dir,
        env: stripEmptyEnvKeys(d.env),
        host_ids: d.host_ids,
        log_type: d.log_type,
        log_target: d.log_target,
        start_command: d.start_command,
        stop_command: d.stop_command,
        pipeline: d.pipeline,
      })),
    })),
  }
}

/** validateDraft 保存前校验，返回错误信息数组（空 = 通过）。 */
export function validateDraft(draft: ConfigDraft): string[] {
  const errors: string[] = []

  const envNames = new Set<string>()
  for (const e of draft.environments) {
    if (e.name.trim() === '') errors.push('环境名称不能为空')
    else if (envNames.has(e.name)) errors.push(`环境名称重复：${e.name}`)
    else envNames.add(e.name)
  }

  const svcNames = new Set<string>()
  for (const s of draft.services) {
    if (s.name.trim() === '') errors.push('服务名称不能为空')
    else if (svcNames.has(s.name)) errors.push(`服务名重复：${s.name}`)
    else svcNames.add(s.name)

    for (const d of s.deployments) {
      if (d.location === 'local' && (d.command ?? '').trim() === '' && !d.pipeline) {
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的本地命令不能为空`)
      }
      if (d.location === 'remote' && (d.host_ids ?? []).length === 0) {
        errors.push(`服务「${s.name}」在「${d.env_name}」环境未选择主机`)
      }
      if (d.pipeline) {
        for (const step of d.pipeline.steps) {
          if (step.action === 'run' && (step.command ?? '').trim() === '') {
            errors.push(`服务「${s.name}」流水线步骤「${step.name || step.id}」命令不能为空`)
          }
          if (step.action === 'sync' && ((step.sync_from ?? '').trim() === '' || (step.sync_to ?? '').trim() === '')) {
            errors.push(`服务「${s.name}」流水线同步步骤「${step.name || step.id}」路径不能为空`)
          }
        }
      }
    }
  }

  return errors
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/lib/__tests__/configDraft.test.ts`
Expected: PASS（7 个用例）。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/lib/configDraft.ts desktop/src/lib/__tests__/configDraft.test.ts
git commit -m "feat(config): configDraft 草稿模型 + 拍平 + 校验纯函数"
```

---

## 前端：叶子组件

### Task 7: EnvKeyValueEditor —— env 变量 key-value 编辑器

**Files:**
- Create: `desktop/src/components/Settings/EnvKeyValueEditor.vue`
- Test: `desktop/src/components/Settings/__tests__/EnvKeyValueEditor.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/EnvKeyValueEditor.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EnvKeyValueEditor from '@/components/Settings/EnvKeyValueEditor.vue'

describe('EnvKeyValueEditor', () => {
  it('展示已有变量为行', () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: { A: '1', B: '2' } } })
    expect(wrapper.findAll('[data-test="env-row"]')).toHaveLength(2)
  })

  it('点击添加变量 emit 含新空行的对象', async () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: {} } })
    await wrapper.find('[data-test="env-add"]').trigger('click')
    const rows = wrapper.findAll('[data-test="env-row"]')
    expect(rows).toHaveLength(1)
  })

  it('删除行 emit 移除该 key 的对象', async () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: { A: '1' } } })
    await wrapper.find('[data-test="env-del"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    expect(emitted![emitted!.length - 1][0]).toEqual({})
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/EnvKeyValueEditor.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 EnvKeyValueEditor.vue**

`modelValue` 是 `Record<string,string>`。内部维护行数组（保留顺序、允许空 key 行存在于编辑期），每次变更 emit 重建的对象。新建 `desktop/src/components/Settings/EnvKeyValueEditor.vue`：

```vue
<!--
EnvKeyValueEditor：环境变量 key-value 行编辑器。

职责：
  - 把 Record<string,string> 展示为可增删的 key/value 行
  - 每次变更 emit 重建后的对象（空 key 行在父层拍平时忽略）
边界：
  - 不做校验，不发请求
-->
<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{ modelValue: Record<string, string> }>()
const emit = defineEmits<{ 'update:modelValue': [Record<string, string>] }>()

interface Row { key: string; value: string }
const rows = ref<Row[]>([])

// 外部值变化时同步行（仅在引用变化时重建，避免编辑时被打断）
watch(
  () => props.modelValue,
  val => {
    rows.value = Object.entries(val ?? {}).map(([key, value]) => ({ key, value }))
  },
  { immediate: true },
)

function emitRows() {
  const out: Record<string, string> = {}
  for (const r of rows.value) {
    out[r.key] = r.value
  }
  emit('update:modelValue', out)
}

function addRow() {
  rows.value.push({ key: '', value: '' })
}

function delRow(i: number) {
  rows.value.splice(i, 1)
  emitRows()
}
</script>

<template>
  <div class="env-editor">
    <div v-for="(row, i) in rows" :key="i" class="env-row" data-test="env-row">
      <input v-model="row.key" class="env-input" placeholder="KEY" @input="emitRows" />
      <span class="env-eq">=</span>
      <input v-model="row.value" class="env-input" placeholder="VALUE" @input="emitRows" />
      <button type="button" class="env-del" data-test="env-del" @click="delRow(i)">✕</button>
    </div>
    <button type="button" class="env-add" data-test="env-add" @click="addRow">+ 添加变量</button>
  </div>
</template>

<style scoped>
.env-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
}
.env-input {
  flex: 1;
  padding: 3px 6px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
}
.env-eq {
  color: var(--text-tertiary);
}
.env-del {
  padding: 2px 6px;
  background: transparent;
  border: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  cursor: pointer;
}
.env-add {
  margin-top: 4px;
  padding: 3px 8px;
  font-size: 11px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
}
</style>
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/EnvKeyValueEditor.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/components/Settings/EnvKeyValueEditor.vue desktop/src/components/Settings/__tests__/EnvKeyValueEditor.test.ts
git commit -m "feat(config): EnvKeyValueEditor 环境变量行编辑器"
```

---

### Task 8: PipelineEditor —— 有序 Step 列表编辑器

**Files:**
- Create: `desktop/src/components/Settings/PipelineEditor.vue`
- Test: `desktop/src/components/Settings/__tests__/PipelineEditor.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/PipelineEditor.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PipelineEditor from '@/components/Settings/PipelineEditor.vue'
import type { Pipeline } from '@/api/agent'

describe('PipelineEditor', () => {
  it('无 pipeline 时展示「配置流水线」入口', () => {
    const wrapper = mount(PipelineEditor, { props: { modelValue: undefined } })
    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(true)
  })

  it('点击启用 emit 含空 steps 的 pipeline', async () => {
    const wrapper = mount(PipelineEditor, { props: { modelValue: undefined } })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted![0][0]).toEqual({ steps: [] })
  })

  it('有 steps 时渲染步骤卡片，添加步骤增加一行', async () => {
    const pipeline: Pipeline = { steps: [{ id: 's1', name: 'build', scope: 'local', action: 'run', command: 'make' }] }
    const wrapper = mount(PipelineEditor, { props: { modelValue: pipeline } })
    expect(wrapper.findAll('[data-test="step-card"]')).toHaveLength(1)
    await wrapper.find('[data-test="step-add"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Pipeline
    expect(last.steps).toHaveLength(2)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/PipelineEditor.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 PipelineEditor.vue**

新建 `desktop/src/components/Settings/PipelineEditor.vue`：

```vue
<!--
PipelineEditor：deployment 流水线（有序 Step 列表）编辑器。

职责：
  - 无 pipeline 时提供「配置流水线」入口（emit { steps: [] }）
  - 有 pipeline 时渲染可增删、可上下移的步骤卡片
  - 按 action 切换显示 run（command/work_dir）或 sync（from/to）字段
边界：
  - 不做校验（校验在 configDraft.validateDraft）
  - Step ID 为空时本地补 step-{n}
-->
<script setup lang="ts">
import type { Pipeline, PipelineStep } from '@/api/agent'

const props = defineProps<{ modelValue?: Pipeline }>()
const emit = defineEmits<{ 'update:modelValue': [Pipeline | undefined] }>()

function enable() {
  emit('update:modelValue', { steps: [] })
}

function update(steps: PipelineStep[]) {
  emit('update:modelValue', { steps })
}

function addStep() {
  const steps = [...(props.modelValue?.steps ?? [])]
  steps.push({ id: `step-${steps.length + 1}`, name: '', scope: 'local', action: 'run', command: '' })
  update(steps)
}

function delStep(i: number) {
  const steps = [...(props.modelValue!.steps)]
  steps.splice(i, 1)
  update(steps)
}

function move(i: number, delta: number) {
  const steps = [...(props.modelValue!.steps)]
  const j = i + delta
  if (j < 0 || j >= steps.length) return
  ;[steps[i], steps[j]] = [steps[j], steps[i]]
  update(steps)
}

function patch(i: number, field: keyof PipelineStep, value: string) {
  const steps = props.modelValue!.steps.map((s, k) => (k === i ? { ...s, [field]: value } : s))
  update(steps)
}

function disable() {
  emit('update:modelValue', undefined)
}
</script>

<template>
  <div class="pipeline-editor">
    <button v-if="!modelValue" type="button" class="pl-enable" data-test="pipeline-enable" @click="enable">
      + 配置流水线
    </button>
    <template v-else>
      <div class="pl-head">
        <span>流水线步骤</span>
        <button type="button" class="pl-disable" @click="disable">移除流水线</button>
      </div>
      <div v-for="(step, i) in modelValue.steps" :key="i" class="step-card" data-test="step-card">
        <div class="step-toolbar">
          <button type="button" @click="move(i, -1)">▲</button>
          <button type="button" @click="move(i, 1)">▼</button>
          <button type="button" data-test="step-del" @click="delStep(i)">✕</button>
        </div>
        <input
          class="step-input" placeholder="步骤名"
          :value="step.name" @input="patch(i, 'name', ($event.target as HTMLInputElement).value)"
        />
        <div class="step-radios">
          <label><input type="radio" :checked="step.scope === 'local'" @change="patch(i, 'scope', 'local')" /> local</label>
          <label><input type="radio" :checked="step.scope === 'fan-out'" @change="patch(i, 'scope', 'fan-out')" /> fan-out</label>
          <label><input type="radio" :checked="step.action === 'run'" @change="patch(i, 'action', 'run')" /> run</label>
          <label><input type="radio" :checked="step.action === 'sync'" @change="patch(i, 'action', 'sync')" /> sync</label>
        </div>
        <template v-if="step.action === 'run'">
          <input class="step-input" placeholder="命令" :value="step.command" @input="patch(i, 'command', ($event.target as HTMLInputElement).value)" />
          <input class="step-input" placeholder="工作目录" :value="step.work_dir" @input="patch(i, 'work_dir', ($event.target as HTMLInputElement).value)" />
        </template>
        <template v-else>
          <input class="step-input" placeholder="同步源路径" :value="step.sync_from" @input="patch(i, 'sync_from', ($event.target as HTMLInputElement).value)" />
          <input class="step-input" placeholder="同步目标路径" :value="step.sync_to" @input="patch(i, 'sync_to', ($event.target as HTMLInputElement).value)" />
        </template>
      </div>
      <button type="button" class="step-add" data-test="step-add" @click="addStep">+ 添加步骤</button>
    </template>
  </div>
</template>

<style scoped>
.pl-enable, .step-add {
  padding: 3px 8px;
  font-size: 11px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
}
.pl-head {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
.pl-disable {
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.step-card {
  border: 1px solid var(--border-secondary);
  padding: 8px;
  margin-bottom: 6px;
}
.step-toolbar {
  display: flex;
  gap: 4px;
  margin-bottom: 4px;
}
.step-toolbar button {
  padding: 1px 6px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}
.step-input {
  display: block;
  width: 100%;
  margin-bottom: 4px;
  padding: 3px 6px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.step-radios {
  display: flex;
  gap: 10px;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
</style>
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/PipelineEditor.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/components/Settings/PipelineEditor.vue desktop/src/components/Settings/__tests__/PipelineEditor.test.ts
git commit -m "feat(config): PipelineEditor 有序步骤列表编辑器"
```

---

## 前端：组合组件

### Task 9: DeploymentForm —— 一份 deployment 的编辑

**Files:**
- Create: `desktop/src/components/Settings/DeploymentForm.vue`
- Test: `desktop/src/components/Settings/__tests__/DeploymentForm.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/DeploymentForm.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import type { Deployment } from '@/api/agent'

function localDep(): Deployment {
  return { id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '/tmp', status: '' }
}

describe('DeploymentForm', () => {
  it('local 时展示命令/工作目录输入', () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    expect(wrapper.find('[data-test="dep-command"]').exists()).toBe(true)
  })

  it('切到 remote emit location=remote', async () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    await wrapper.find('[data-test="dep-location-remote"]').setValue()
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.location).toBe('remote')
  })

  it('修改命令 emit 新值', async () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    await wrapper.find('[data-test="dep-command"]').setValue('npm run dev')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.command).toBe('npm run dev')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/DeploymentForm.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 DeploymentForm.vue**

`modelValue` 是一份 `Deployment`，`hosts` 是可选主机列表（`{id,name}[]`）。所有变更通过 emit 整份新 deployment。新建 `desktop/src/components/Settings/DeploymentForm.vue`：

```vue
<!--
DeploymentForm：单份 deployment 的编辑表单（最大组件，职责单一）。

职责：
  - location 切换 local/remote
  - local：命令 / 工作目录 / 环境变量（EnvKeyValueEditor）
  - remote：主机多选 / 日志类型 / 启停命令
  - pipeline：折叠的 PipelineEditor
边界：
  - 不做校验、不发请求；变更整份 emit 给父层草稿
-->
<script setup lang="ts">
import type { Deployment, LogSourceType, Pipeline } from '@/api/agent'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'
import PipelineEditor from './PipelineEditor.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()

function patch(partial: Partial<Deployment>) {
  emit('update:modelValue', { ...props.modelValue, ...partial })
}

function toggleHost(id: string, checked: boolean) {
  const set = new Set(props.modelValue.host_ids ?? [])
  if (checked) set.add(id)
  else set.delete(id)
  patch({ host_ids: [...set] })
}

function setEnv(env: Record<string, string>) {
  patch({ env })
}

function setPipeline(pipeline: Pipeline | undefined) {
  patch({ pipeline })
}
</script>

<template>
  <div class="dep-form">
    <div class="dep-location">
      <label>
        <input
          type="radio" data-test="dep-location-local"
          :checked="modelValue.location === 'local'" @change="patch({ location: 'local' })"
        /> 本地
      </label>
      <label>
        <input
          type="radio" data-test="dep-location-remote"
          :checked="modelValue.location === 'remote'" @change="patch({ location: 'remote' })"
        /> 远程
      </label>
    </div>

    <template v-if="modelValue.location === 'local'">
      <input
        class="dep-input" data-test="dep-command" placeholder="启动命令"
        :value="modelValue.command" @input="patch({ command: ($event.target as HTMLInputElement).value })"
      />
      <input
        class="dep-input" placeholder="工作目录"
        :value="modelValue.work_dir" @input="patch({ work_dir: ($event.target as HTMLInputElement).value })"
      />
      <div class="dep-label">环境变量</div>
      <EnvKeyValueEditor :model-value="modelValue.env ?? {}" @update:model-value="setEnv" />
    </template>

    <template v-else>
      <div class="dep-label">目标主机</div>
      <div v-if="hosts.length === 0" class="dep-hint">还没有主机，请先在「主机管理」添加</div>
      <label v-for="h in hosts" :key="h.id" class="dep-host">
        <input
          type="checkbox" :checked="(modelValue.host_ids ?? []).includes(h.id)"
          @change="toggleHost(h.id, ($event.target as HTMLInputElement).checked)"
        /> {{ h.name }}
      </label>
      <select
        class="dep-input" :value="modelValue.log_type ?? 'journalctl'"
        @change="patch({ log_type: ($event.target as HTMLSelectElement).value as LogSourceType })"
      >
        <option value="journalctl">journalctl</option>
        <option value="docker">docker</option>
      </select>
      <input
        class="dep-input" placeholder="日志目标（服务名/容器名）"
        :value="modelValue.log_target" @input="patch({ log_target: ($event.target as HTMLInputElement).value })"
      />
      <input
        class="dep-input" placeholder="启动命令（可选）"
        :value="modelValue.start_command" @input="patch({ start_command: ($event.target as HTMLInputElement).value })"
      />
      <input
        class="dep-input" placeholder="停止命令（可选）"
        :value="modelValue.stop_command" @input="patch({ stop_command: ($event.target as HTMLInputElement).value })"
      />
    </template>

    <div class="dep-label">部署流水线（可选）</div>
    <PipelineEditor :model-value="modelValue.pipeline" @update:model-value="setPipeline" />
  </div>
</template>

<style scoped>
.dep-form {
  padding: 8px 0;
}
.dep-location {
  display: flex;
  gap: 14px;
  margin-bottom: 8px;
  font-size: 12px;
  color: var(--text-secondary);
}
.dep-input {
  display: block;
  width: 100%;
  margin-bottom: 6px;
  padding: 4px 8px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.dep-label {
  font-size: 11px;
  color: var(--text-tertiary);
  margin: 8px 0 4px;
}
.dep-hint {
  font-size: 11px;
  color: var(--status-failed);
  margin-bottom: 6px;
}
.dep-host {
  display: block;
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 3px;
}
</style>
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/DeploymentForm.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/components/Settings/DeploymentForm.vue desktop/src/components/Settings/__tests__/DeploymentForm.test.ts
git commit -m "feat(config): DeploymentForm 单份 deployment 编辑表单"
```

---

### Task 10: ServiceCard + ServiceList —— 当前 env 下的服务列表

**Files:**
- Create: `desktop/src/components/Settings/ServiceCard.vue`
- Create: `desktop/src/components/Settings/ServiceList.vue`
- Test: `desktop/src/components/Settings/__tests__/ServiceList.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/ServiceList.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ServiceList from '@/components/Settings/ServiceList.vue'
import type { ConfigDraftService } from '@/lib/configDraft'

function svc(): ConfigDraftService {
  return { id: 's1', name: 'web', required: false, order: 0, deployments: [] }
}

describe('ServiceList', () => {
  it('渲染服务卡片', () => {
    const wrapper = mount(ServiceList, { props: { services: [svc()], envName: 'dev', hosts: [] } })
    expect(wrapper.findAll('[data-test="service-card"]')).toHaveLength(1)
  })

  it('点击新增服务 emit add-service', async () => {
    const wrapper = mount(ServiceList, { props: { services: [], envName: 'dev', hosts: [] } })
    await wrapper.find('[data-test="add-service"]').trigger('click')
    expect(wrapper.emitted('add-service')).toBeTruthy()
  })

  it('当前 env 无 deployment 时展示启用占位', () => {
    const wrapper = mount(ServiceList, { props: { services: [svc()], envName: 'dev', hosts: [] } })
    expect(wrapper.find('[data-test="enable-dep"]').exists()).toBe(true)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/ServiceList.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 ServiceCard.vue**

新建 `desktop/src/components/Settings/ServiceCard.vue`。负责单个 service：名称/required + 该 env 的 deployment（无则占位「启用」）。deployment 变更/删除/新增向上 emit。

```vue
<!--
ServiceCard：单个 service 在某个 env 下的配置卡片。

职责：
  - 编辑 service 名称 / required
  - 展示该 env 下的 deployment（DeploymentForm）；无则显示「启用」占位
  - 删除服务
边界：
  - 一个 service 在一个 env 下至多一份 deployment（按 env_name 匹配）
  - 变更整份 service 草稿向上 emit
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { Deployment } from '@/api/agent'
import type { ConfigDraftService } from '@/lib/configDraft'
import DeploymentForm from './DeploymentForm.vue'

const props = defineProps<{
  service: ConfigDraftService
  envName: string
  hosts: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{
  'update:service': [ConfigDraftService]
  'remove': []
}>()

const dep = computed(() => props.service.deployments.find(d => d.env_name === props.envName))

function patchService(partial: Partial<ConfigDraftService>) {
  emit('update:service', { ...props.service, ...partial })
}

function enableDep() {
  const newDep: Deployment = { id: '', env_name: props.envName, location: 'local', command: '', work_dir: '', status: '' }
  patchService({ deployments: [...props.service.deployments, newDep] })
}

function updateDep(updated: Deployment) {
  patchService({
    deployments: props.service.deployments.map(d => (d.env_name === props.envName ? updated : d)),
  })
}

function removeDep() {
  patchService({ deployments: props.service.deployments.filter(d => d.env_name !== props.envName) })
}
</script>

<template>
  <article class="service-card" data-test="service-card">
    <header class="svc-header">
      <input
        class="svc-name" placeholder="服务名"
        :value="service.name" @input="patchService({ name: ($event.target as HTMLInputElement).value })"
      />
      <label class="svc-required">
        <input
          type="checkbox" :checked="service.required"
          @change="patchService({ required: ($event.target as HTMLInputElement).checked })"
        /> 必选
      </label>
      <button type="button" class="svc-remove" data-test="remove-service" @click="emit('remove')">删除</button>
    </header>

    <div v-if="!dep" class="svc-empty">
      该环境下未配置
      <button type="button" class="enable-btn" data-test="enable-dep" @click="enableDep">启用</button>
    </div>
    <div v-else class="svc-dep">
      <DeploymentForm :model-value="dep" :hosts="hosts" @update:model-value="updateDep" />
      <button type="button" class="dep-remove" @click="removeDep">移除该环境配置</button>
    </div>
  </article>
</template>

<style scoped>
.service-card {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 10px;
}
.svc-header {
  display: flex;
  align-items: center;
  gap: 10px;
}
.svc-name {
  flex: 1;
  padding: 4px 8px;
  font-size: 13px;
  font-weight: 600;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
}
.svc-required {
  font-size: 11px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.svc-remove {
  padding: 3px 9px;
  background: transparent;
  border: 1px solid var(--border-secondary);
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.svc-empty {
  margin-top: 8px;
  font-size: 12px;
  color: var(--text-tertiary);
}
.enable-btn {
  margin-left: 8px;
  padding: 2px 10px;
  background: var(--accent);
  border: none;
  color: #fff;
  cursor: pointer;
  font-size: 11px;
}
.dep-remove {
  padding: 2px 8px;
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
</style>
```

- [ ] **Step 4: 实现 ServiceList.vue**

新建 `desktop/src/components/Settings/ServiceList.vue`：

```vue
<!--
ServiceList：当前 env 下的服务列表 + 新增服务入口。

职责：
  - 渲染每个 service 的 ServiceCard
  - 服务草稿变更 / 删除 / 新增向上 emit
边界：
  - 不持有草稿，纯受控组件
-->
<script setup lang="ts">
import type { ConfigDraftService } from '@/lib/configDraft'
import ServiceCard from './ServiceCard.vue'

defineProps<{
  services: ConfigDraftService[]
  envName: string
  hosts: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{
  'update-service': [number, ConfigDraftService]
  'remove-service': [number]
  'add-service': []
}>()
</script>

<template>
  <div class="service-list">
    <ServiceCard
      v-for="(svc, i) in services" :key="svc.id || i"
      :service="svc" :env-name="envName" :hosts="hosts"
      @update:service="emit('update-service', i, $event)"
      @remove="emit('remove-service', i)"
    />
    <button type="button" class="add-service" data-test="add-service" @click="emit('add-service')">
      + 新增服务
    </button>
  </div>
</template>

<style scoped>
.add-service {
  padding: 6px 12px;
  font-size: 12px;
  background: var(--bg-overlay);
  border: 1px dashed var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  width: 100%;
}
</style>
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/ServiceList.test.ts`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add desktop/src/components/Settings/ServiceCard.vue desktop/src/components/Settings/ServiceList.vue desktop/src/components/Settings/__tests__/ServiceList.test.ts
git commit -m "feat(config): ServiceCard + ServiceList 服务列表组件"
```

---

### Task 11: EnvTabBar —— 环境横向 tab

**Files:**
- Create: `desktop/src/components/Settings/EnvTabBar.vue`
- Test: `desktop/src/components/Settings/__tests__/EnvTabBar.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/EnvTabBar.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EnvTabBar from '@/components/Settings/EnvTabBar.vue'
import type { Environment } from '@/api/agent'

const envs: Environment[] = [
  { id: 'e1', name: 'dev', is_dev: true, order: 0 },
  { id: 'e2', name: 'prod', is_dev: false, order: 1 },
]

describe('EnvTabBar', () => {
  it('渲染每个 env 的 tab', () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    expect(wrapper.findAll('[data-test="env-tab"]')).toHaveLength(2)
  })

  it('点击 tab emit update:active', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    await wrapper.findAll('[data-test="env-tab"]')[1].trigger('click')
    expect(wrapper.emitted('update:active')![0][0]).toBe('prod')
  })

  it('点击新增 emit add-env', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    await wrapper.find('[data-test="add-env"]').trigger('click')
    expect(wrapper.emitted('add-env')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/EnvTabBar.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 EnvTabBar.vue**

新建 `desktop/src/components/Settings/EnvTabBar.vue`：

```vue
<!--
EnvTabBar：环境横向 tab，切换 / 新增 / 删除 / is_dev 标记。

职责：
  - 渲染所有 env tab，高亮 active
  - 切换 emit update:active；新增 emit add-env；删除 emit remove-env
边界：
  - 不持有草稿，受控组件
-->
<script setup lang="ts">
import type { Environment } from '@/api/agent'

defineProps<{ environments: Environment[]; active: string }>()
const emit = defineEmits<{
  'update:active': [string]
  'add-env': []
  'remove-env': [string]
}>()
</script>

<template>
  <div class="env-tabbar">
    <button
      v-for="env in environments" :key="env.id || env.name"
      type="button" class="env-tab" data-test="env-tab"
      :class="{ active: env.name === active }"
      @click="emit('update:active', env.name)"
    >
      {{ env.name }}
      <span v-if="env.is_dev" class="dev-dot" title="开发环境">·dev</span>
      <span class="env-x" @click.stop="emit('remove-env', env.name)">✕</span>
    </button>
    <button type="button" class="add-env" data-test="add-env" @click="emit('add-env')">+ 新增环境</button>
  </div>
</template>

<style scoped>
.env-tabbar {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--border-secondary);
  margin-bottom: 12px;
}
.env-tab {
  padding: 6px 12px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
}
.env-tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--accent);
}
.dev-dot {
  color: var(--accent);
  font-size: 10px;
}
.env-x {
  margin-left: 6px;
  color: var(--text-tertiary);
}
.env-x:hover {
  color: var(--status-failed);
}
.add-env {
  padding: 6px 10px;
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 12px;
}
</style>
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/EnvTabBar.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/components/Settings/EnvTabBar.vue desktop/src/components/Settings/__tests__/EnvTabBar.test.ts
git commit -m "feat(config): EnvTabBar 环境横向 tab"
```

---

### Task 12: ProjectConfigEditor —— 编辑器外壳

**Files:**
- Create: `desktop/src/components/Settings/ProjectConfigEditor.vue`
- Test: `desktop/src/components/Settings/__tests__/ProjectConfigEditor.test.ts`

- [ ] **Step 1: 写失败测试**

新建 `desktop/src/components/Settings/__tests__/ProjectConfigEditor.test.ts`：

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import type { Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      putProjectSetup: vi.fn().mockResolvedValue({}),
      listProjects: vi.fn().mockResolvedValue([]),
    },
  }
})

function project(): Project {
  return {
    id: 'p1', name: 'demo', root_path: '/tmp/demo', selected_service_ids: [],
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    services: [{ id: 's1', project_id: 'p1', name: 'web', status: '', command: '', work_dir: '', required: false, order: 0, deployments: [] }],
  }
}

describe('ProjectConfigEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('渲染 env tab 与服务列表', async () => {
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))
    expect(wrapper.find('[data-test="env-tab"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="service-card"]').exists()).toBe(true)
  })

  it('校验失败时阻止保存并展示错误', async () => {
    const { api } = await import('@/api/agent')
    const p = project()
    p.environments![0].name = ''
    const wrapper = mount(ProjectConfigEditor, { props: { project: p } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-save"]').trigger('click')
    expect(api.putProjectSetup).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('环境名称不能为空')
  })

  it('点击取消 emit cancel', async () => {
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-cancel"]').trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/ProjectConfigEditor.test.ts`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现 ProjectConfigEditor.vue**

外壳：持有草稿、env tab 切换、增删 env/service、保存（校验→拍平→putProjectSetup→reloadProject）/取消。新建 `desktop/src/components/Settings/ProjectConfigEditor.vue`：

```vue
<!--
ProjectConfigEditor：项目配置编辑器外壳（配置唯一编辑入口）。

职责：
  - 持有项目配置草稿（深拷贝自 project），全程本地编辑
  - env 横向 tab 切换，增删 env / service
  - 保存：校验 → 拍平为 SetupPayload → PUT /setup → reloadProject → emit saved
  - 取消：丢弃草稿 → emit cancel
边界：
  - 不负责新建项目的落地（由父层在 saved 后处理 registry）
  - 删除运行中 service 的最终守卫在后端
-->
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { api, type Project } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { projectToDraft, draftToPayload, validateDraft, type ConfigDraftService } from '@/lib/configDraft'
import EnvTabBar from './EnvTabBar.vue'
import ServiceList from './ServiceList.vue'

const props = defineProps<{ project: Project; isNew?: boolean }>()
const emit = defineEmits<{ saved: [Project]; cancel: [] }>()

const agentStore = useAgentStore()
const draft = ref(projectToDraft(props.project))
const activeEnv = ref('')
const hosts = ref<Array<{ id: string; name: string }>>([])
const errors = ref<string[]>([])
const saving = ref(false)
const saveError = ref<string | null>(null)

onMounted(async () => {
  // 默认选中第一个 is_dev 的 env，否则第一个
  const envs = draft.value.environments
  activeEnv.value = (envs.find(e => e.is_dev) ?? envs[0])?.name ?? ''
  try {
    const list = await api.listHosts()
    hosts.value = list.map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

const currentServices = computed(() => draft.value.services)

function addEnv() {
  const base = 'env'
  let name = base
  let n = 1
  const taken = new Set(draft.value.environments.map(e => e.name))
  while (taken.has(name)) name = `${base}${n++}`
  draft.value.environments.push({ id: '', name, is_dev: false, order: draft.value.environments.length })
  activeEnv.value = name
}

function removeEnv(name: string) {
  draft.value.environments = draft.value.environments.filter(e => e.name !== name)
  // 同时移除各 service 在该 env 的 deployment
  for (const s of draft.value.services) {
    s.deployments = s.deployments.filter(d => d.env_name !== name)
  }
  if (activeEnv.value === name) {
    activeEnv.value = draft.value.environments[0]?.name ?? ''
  }
}

function addService() {
  draft.value.services.push({ id: '', name: '', required: false, order: draft.value.services.length, deployments: [] })
}

function updateService(i: number, svc: ConfigDraftService) {
  draft.value.services[i] = svc
}

function removeService(i: number) {
  draft.value.services.splice(i, 1)
}

async function save() {
  errors.value = validateDraft(draft.value)
  if (errors.value.length) return
  saving.value = true
  saveError.value = null
  try {
    const updated = await api.putProjectSetup(props.project.id, draftToPayload(draft.value))
    await agentStore.reloadProject(props.project.id)
    emit('saved', updated)
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="editor-backdrop" @click.self="emit('cancel')">
    <div class="editor-body">
      <div class="editor-title">配置项目 · {{ project.name }}</div>

      <ul v-if="errors.length" class="err-list">
        <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
      </ul>
      <div v-if="saveError" class="err-list">{{ saveError }}</div>

      <EnvTabBar
        :environments="draft.environments" :active="activeEnv"
        @update:active="activeEnv = $event" @add-env="addEnv" @remove-env="removeEnv"
      />

      <ServiceList
        :services="currentServices" :env-name="activeEnv" :hosts="hosts"
        @update-service="updateService" @remove-service="removeService" @add-service="addService"
      />

      <div class="editor-actions">
        <button type="button" data-test="config-cancel" @click="emit('cancel')">取消</button>
        <button type="button" class="primary" data-test="config-save" :disabled="saving" @click="save">
          {{ saving ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.editor-body {
  width: min(680px, calc(100vw - 32px));
  max-height: 88vh;
  overflow-y: auto;
  padding: 20px 22px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.editor-title {
  margin-bottom: 14px;
  font-size: 14px;
  font-weight: 600;
}
.err-list {
  margin: 0 0 12px;
  padding: 8px 12px;
  list-style: none;
  background: var(--bg-secondary);
  border-left: 2px solid var(--status-failed);
  color: var(--status-failed);
  font-size: 12px;
}
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
}
.editor-actions button {
  padding: 5px 14px;
  font-size: 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
}
.editor-actions button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.editor-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/ProjectConfigEditor.test.ts`
Expected: PASS（3 个用例）。

- [ ] **Step 5: 提交**

```bash
git add desktop/src/components/Settings/ProjectConfigEditor.vue desktop/src/components/Settings/__tests__/ProjectConfigEditor.test.ts
git commit -m "feat(config): ProjectConfigEditor 编辑器外壳（草稿+校验+保存）"
```

---

## 前端：接入页面与新建流程

### Task 13: SettingsPage 接入编辑器 + store 新建流程 + 移除旧 Modal

**Files:**
- Modify: `desktop/src/stores/agent.ts`（addProject 拆分为 probe+create）
- Modify: `desktop/src/pages/SettingsPage.vue`（入口按钮、新建流程、用 ProjectConfigEditor）
- Modify: `desktop/src/components/Sidebar/SidebarView.vue`（添加项目走新流程）
- Delete: `desktop/src/components/Settings/ProjectSetupModal.vue` 及其测试

- [ ] **Step 1: store 新增 probeProject 透传**

在 `desktop/src/stores/agent.ts` 的 `addProject` 之后加 `probeProject`，并加进 return 块（`reloadProject` 之后）：

```ts
  async function probeProject(rootPath: string) {
    return api.probeProject(rootPath)
  }
```

return 块加 `probeProject,`（在 `addProject,` 附近）。

- [ ] **Step 2: SettingsPage 改造**

在 `desktop/src/pages/SettingsPage.vue`：
1. import 改为 `ProjectConfigEditor`：把 `import ProjectSetupModal from '@/components/Settings/ProjectSetupModal.vue'` 改成 `import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'`。
2. 新建流程：`addProject` 函数改为「探测目录 → 打开编辑器（空状态）」，保存成功后才落地。替换 `addProject`（行 37-49）：

```ts
const editorProject = ref<Project | null>(null)
const editorIsNew = ref(false)

async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
  if (!selected || Array.isArray(selected)) return
  try {
    const probed = await agentStore.probeProject(selected)
    editorProject.value = probed
    editorIsNew.value = true
  } catch (e) {
    const msg = e instanceof Error ? e.message : '读取项目失败'
    await message(msg, { title: '无法读取项目', kind: 'error' })
  }
}

async function onEditorSaved() {
  // 新建项目：保存成功后落地（addProject 写注册表+内存）
  if (editorIsNew.value && editorProject.value) {
    try {
      await agentStore.addProject(editorProject.value.root_path)
    } catch (e) {
      const msg = e instanceof Error ? e.message : '创建项目失败'
      await message(msg, { title: '创建失败', kind: 'error' })
    }
  }
  editorProject.value = null
  editorIsNew.value = false
}
```

注意：`addProject` 落地空目录后，config.yaml 还没有用户配的内容——这里有时序问题。改为：新建时**先**用 probe 的 root_path 调用 `addProject` 落地（拿到带 ID 的 project），再打开编辑器编辑它。这样保存走 `putProjectSetup` 即可。把上面 `addProject` 函数体改为：

```ts
async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
  if (!selected || Array.isArray(selected)) return
  try {
    // 落地项目（空目录返回空骨架，已有 config 则解析），再进编辑器
    const created = await agentStore.addProject(selected)
    editorProject.value = created
    editorIsNew.value = true
  } catch (e) {
    const msg = e instanceof Error ? e.message : '添加项目失败'
    await message(msg, { title: '无法添加项目', kind: 'error' })
  }
}

function onEditorSaved() {
  editorProject.value = null
  editorIsNew.value = false
}
```

> 决策记录：spec 方案 B「保存才落地」的初衷是「取消不留空壳」。但 `putProjectSetup` 需要 project ID（要先有项目）。权衡后采用：新建时立即 `addProject` 落地（空骨架，无副作用进程），用户取消则需手动删除该空项目。这是对方案 B 的务实调整——保留了「无 config.yaml 也能新建」的核心价值，代价是空目录取消会留一个空项目卡片。若严格要求取消无残留，需后端加「未保存项目」临时态，超出本期范围。

3. 把旧的 `setupProject`/`openSetup`/`onSetupDone` 相关 ref 和函数（行 76-84）删除，`openSetup(project)` 的调用点改为 `openEditor`：

```ts
function openEditor(project: Project) {
  editorProject.value = project
  editorIsNew.value = false
}
```

4. 模板里「配置环境」按钮（行 189-196）改为常驻「编辑配置」：

```vue
                <button
                  class="ghost-btn"
                  :data-test="`setup-project-${project.id}`"
                  @click="openEditor(project)"
                >
                  编辑配置
                </button>
```

（移除 `v-if="!project.environments?.length"`）

5. 模板底部把 `<ProjectSetupModal .../>`（行 234-240）替换为：

```vue
    <ProjectConfigEditor
      v-if="editorProject"
      :project="editorProject"
      :is-new="editorIsNew"
      @saved="onEditorSaved"
      @cancel="editorProject = null; editorIsNew = false"
    />
```

- [ ] **Step 3: SidebarView 添加项目走相同流程**

`desktop/src/components/Sidebar/SidebarView.vue` 的 `addProject`（行 45-）当前直接 `agentStore.addProject(selected)`。保持不变即可（落地后用户去设置页编辑），无需改动；确认它不引用 ProjectSetupModal。

Run: `grep -n "ProjectSetupModal" desktop/src/components/Sidebar/SidebarView.vue`
Expected: 无输出（不引用）。

- [ ] **Step 4: 删除旧 Modal 及测试**

```bash
git rm desktop/src/components/Settings/ProjectSetupModal.vue desktop/src/components/Settings/__tests__/ProjectSetupModal.test.ts
```

- [ ] **Step 5: 类型检查 + 全量前端测试**

Run: `cd desktop && pnpm vue-tsc --noEmit && pnpm vitest run`
Expected: PASS（无类型错误；所有测试通过；无对已删除 ProjectSetupModal 的引用）。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "feat(config): SettingsPage 接入 ProjectConfigEditor，移除一次性 ProjectSetupModal"
```

---

### Task 14: 端到端回归与文档收尾

**Files:**
- 全仓库

- [ ] **Step 1: 后端全量测试**

Run: `cd agent && go test ./...`
Expected: PASS。

- [ ] **Step 2: 前端全量测试 + 类型检查 + 构建**

Run: `cd desktop && pnpm vue-tsc --noEmit && pnpm vitest run && pnpm build`
Expected: PASS（构建产出 dist）。

- [ ] **Step 3: 手动 smoke（由用户在真实 app 验证）**

清单（在 settings 项目页）：
1. 「+ 添加项目」选一个**空目录** → 进编辑器空状态 → 新增 env「dev」→ 新增 service 填命令 → 保存 → 项目卡片出现，目录生成 `.superdev/config.yaml`。
2. 已有项目点「编辑配置」→ 新增第二个 env「prod」→ 切到 prod → 给某 service「启用」并填 remote 配置 → 保存 → 重新打开能看到。
3. 编辑某 deployment 的环境变量（加一行 KEY=VALUE）→ 保存 → 重新打开仍在。
4. 删除一个正在运行的 service → 后端返回「请先停止服务…」toast。

- [ ] **Step 4: 提交（如有文档/CHANGELOG 更新）**

```bash
git add -A
git commit -m "test: 项目配置编辑器端到端回归通过"
```

---

## 备注

- **方案 B 调整**：见 Task 13 Step 2 的决策记录——新建项目改为「立即落地空骨架 + 编辑器编辑」，而非「保存才落地」。原因是 `putProjectSetup` 需要 project ID。代价：空目录取消会留一个空项目，用户可手动删除。
- **Pipeline 编辑**：本期覆盖线性 Step 列表（增删/上下移/run-sync 字段），不做 DAG 可视化。
- **未做（YAGNI）**：扫描 package.json 推断命令、LaunchImportPanel（launch.json 预填在本期降级——见下）、.env 导入、配置导出。

> **LaunchImportPanel 调整**：spec 第 6 列了 `LaunchImportPanel.vue`（新建空状态从 launch.json 预填）。本计划 Task 未实现它——因为新建项目现在直接进编辑器手动添加 service 已可用，launch.json 预填是增强项。如需保留该能力，在 Task 12 编辑器空状态（services 为空且 isNew）时加一个「从 launch.json 导入」按钮，调用已有 `api.getVscodeLaunch` 把匹配项填入草稿。建议作为后续独立任务，避免本期膨胀。
