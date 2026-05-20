# SuperDev Tauri 桌面客户端实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Tauri 2 + Vue 3 实现跨平台桌面客户端，复用已有 Go agent HTTP/WebSocket API，功能与现有 macOS Swift 版对等。

**Architecture:** Rust 壳负责启动/关闭 Go agent sidecar 和系统托盘；Vue 3 前端通过 HTTP/WebSocket 直连 agent，5 个 Pinia store 分层管理状态；分栏布局用递归 PanelNode 树实现，日志过滤完全在客户端完成。

**Tech Stack:** Tauri 2、Vue 3、TypeScript、Pinia、shadcn-vue、Tailwind CSS、Vite、pnpm

---

## 背景与约定

### Go agent API（已实现，运行在 `http://localhost:27017`）

```
GET    /api/projects                   → Project[]
POST   /api/projects                   → Project（body: { root_path }）
DELETE /api/projects/{id}
GET    /api/projects/{id}/rules        → LogRule[]
PUT    /api/projects/{id}/rules        → LogRule[]（body: LogRule[]）
GET    /api/services?project_id={id}   → Service[]
POST   /api/services/{id}/start
POST   /api/services/{id}/stop
POST   /api/services/{id}/restart
POST   /api/projects/{id}/start-selected
GET    /api/logs?service={id}&run={runId}&limit={n}&before={cursor}  → LogEntry[]
WS     /ws/logs?service={id}           先推最近 200 条历史，之后实时流
```

### 关键数据类型

```typescript
// 与 Go agent JSON 字段一一对应
interface Service {
  id: string
  project_id: string
  name: string
  status: '' | 'starting' | 'running' | 'failed'  // '' = stopped
  pid?: number
  command: string
  work_dir: string
  required: boolean
  order: number
  env_file?: string
  env?: Record<string, string>
}

interface Project {
  id: string
  name: string
  root_path: string
  services: Service[]
  selected_service_ids: string[]  // 存服务 name，不是 id
}

interface LogEntry {
  id: number
  service_id: string
  run_id: string
  timestamp: string   // ISO 8601
  level: string       // INFO / WARN / ERROR / DEBUG
  message: string
  stream: string      // stdout / stderr
}

interface LogRule {
  id: string
  name: string
  type: 'include' | 'exclude'
  keywords: string[]
  logic: 'and' | 'or'
  enabled: boolean
}
```

---

## 文件结构

```
desktop/
├── src-tauri/
│   ├── Cargo.toml
│   ├── tauri.conf.json
│   ├── capabilities/default.json
│   └── src/
│       ├── main.rs          # Tauri 入口
│       └── agent.rs         # agent 进程生命周期（spawn/kill）
└── src/
    ├── main.ts
    ├── App.vue
    ├── api/
    │   └── agent.ts         # HTTP 请求封装（fetch）
    ├── stores/
    │   ├── agent.ts         # 服务状态轮询
    │   ├── log.ts           # 日志缓冲 + WebSocket 管理
    │   ├── panel.ts         # 面板布局树
    │   ├── bookmark.ts      # 书签和同步组
    │   └── filter.ts        # chip 过滤 + LogRule 缓存
    ├── composables/
    │   └── useDragDrop.ts   # 面板拖放分栏
    └── components/
        ├── Sidebar/
        │   ├── SidebarView.vue
        │   ├── ProjectHeader.vue
        │   └── ServiceRow.vue
        ├── Panel/
        │   ├── PanelLayout.vue
        │   ├── PanelLeaf.vue
        │   ├── LogPanel.vue
        │   ├── PanelToolbar.vue
        │   └── LogRow.vue
        └── BottomBar.vue
```

---

## Task 1: 脚手架 — 创建 Tauri + Vue 3 项目

**Files:**
- Create: `desktop/` 整个目录

- [ ] **Step 1: 安装 Tauri CLI 并初始化项目**

```bash
cd /Users/xushixin/workspace/super-debug
cargo install tauri-cli --version "^2"
cargo tauri init --ci \
  --app-name "SuperDev" \
  --window-title "SuperDev" \
  --dist-dir "../dist" \
  --dev-url "http://localhost:5173" \
  --before-dev-command "pnpm dev" \
  --before-build-command "pnpm build"
# 注意：tauri init 会在当前目录创建 src-tauri/，需要先 cd desktop/
```

正确做法：

```bash
mkdir -p /Users/xushixin/workspace/super-debug/desktop
cd /Users/xushixin/workspace/super-debug/desktop

# 用 Vite 创建 Vue 3 + TypeScript 前端
pnpm create vite . --template vue-ts
# 选择：不要覆盖已有文件（目录为空，直接回车）
pnpm install

# 再初始化 Tauri
pnpm add -D @tauri-apps/cli@^2
pnpm tauri init
# 交互提示按如下填写：
# App name: SuperDev
# Window title: SuperDev
# Web assets relative path: ../dist
# Dev server URL: http://localhost:5173
# Frontend dev command: pnpm dev
# Frontend build command: pnpm build
```

- [ ] **Step 2: 安装前端依赖**

```bash
cd /Users/xushixin/workspace/super-debug/desktop

# Pinia
pnpm add pinia

# Tauri API
pnpm add @tauri-apps/api@^2
pnpm add @tauri-apps/plugin-dialog@^2
pnpm add @tauri-apps/plugin-shell@^2

# Tailwind CSS
pnpm add -D tailwindcss @tailwindcss/vite

# shadcn-vue（基于 Radix Vue）
pnpm add radix-vue class-variance-authority clsx tailwind-merge
pnpm add -D @iconify/vue

# UUID
pnpm add uuid
pnpm add -D @types/uuid
```

- [ ] **Step 3: 配置 Tailwind CSS**

编辑 `desktop/vite.config.ts`：

```typescript
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: { '@': resolve(__dirname, 'src') },
  },
  clearScreen: false,
  server: { port: 5173, strictPort: true },
})
```

创建 `desktop/src/style.css`：

```css
@import "tailwindcss";

@layer base {
  :root {
    --bg-primary: #0d1117;
    --bg-elevated: #161b22;
    --bg-overlay: #21262d;
    --border: #30363d;
    --border-secondary: #21262d;
    --text-primary: #e6edf3;
    --text-secondary: #8b949e;
    --text-tertiary: #6e7681;
    --accent: #1f6feb;
    --status-running: #3fb950;
    --status-starting: #d29922;
    --status-failed: #f85149;
  }

  * { box-sizing: border-box; }
  body {
    background: var(--bg-primary);
    color: var(--text-primary);
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    font-size: 13px;
    margin: 0;
    user-select: none;
    -webkit-user-select: none;
  }
}
```

- [ ] **Step 4: 配置 Tauri manifest**

编辑 `desktop/src-tauri/tauri.conf.json`：

```json
{
  "productName": "SuperDev",
  "version": "0.1.0",
  "identifier": "dev.superdev.app",
  "build": {
    "beforeDevCommand": "pnpm dev",
    "beforeBuildCommand": "pnpm build",
    "devUrl": "http://localhost:5173",
    "frontendDist": "../dist"
  },
  "app": {
    "windows": [
      {
        "title": "SuperDev",
        "width": 1200,
        "height": 750,
        "minWidth": 800,
        "minHeight": 500,
        "resizable": true,
        "decorations": true
      }
    ],
    "security": { "csp": null },
    "trayIcon": {
      "iconPath": "icons/icon.png",
      "iconAsTemplate": true
    }
  },
  "bundle": {
    "active": true,
    "targets": "all",
    "externalBin": ["binaries/superdev-agent"]
  }
}
```

创建 `desktop/src-tauri/capabilities/default.json`：

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "Default capabilities",
  "windows": ["main"],
  "permissions": [
    "core:default",
    "dialog:default",
    "shell:default"
  ]
}
```

- [ ] **Step 5: 编写 agent.rs（Rust 侧 agent 生命周期）**

创建 `desktop/src-tauri/src/agent.rs`：

```rust
use std::process::{Child, Command};
use std::sync::Mutex;

pub struct AgentProcess(pub Mutex<Option<Child>>);

impl AgentProcess {
    pub fn new() -> Self {
        AgentProcess(Mutex::new(None))
    }

    pub fn start(&self, sidecar_path: &str) {
        let mut guard = self.0.lock().unwrap();
        if guard.is_some() {
            return;
        }
        match Command::new(sidecar_path)
            .args(["--addr", ":27017"])
            .spawn()
        {
            Ok(child) => {
                *guard = Some(child);
                println!("[SuperDev] agent started, pid={}", guard.as_ref().unwrap().id());
            }
            Err(e) => eprintln!("[SuperDev] failed to start agent: {e}"),
        }
    }

    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap();
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
            println!("[SuperDev] agent stopped");
        }
    }
}
```

- [ ] **Step 6: 编写 main.rs**

编辑 `desktop/src-tauri/src/main.rs`：

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
use agent::AgentProcess;
use tauri::Manager;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let agent = AgentProcess::new();
            // 开发模式下不启动 sidecar，依赖手动启动的 agent
            #[cfg(not(debug_assertions))]
            {
                let resource_path = app
                    .path()
                    .resource_dir()
                    .unwrap()
                    .join("binaries/superdev-agent");
                agent.start(resource_path.to_str().unwrap());
            }
            app.manage(agent);
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                let agent = window.app_handle().state::<AgentProcess>();
                agent.stop();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

- [ ] **Step 7: 更新 Cargo.toml 依赖**

编辑 `desktop/src-tauri/Cargo.toml`，在 `[dependencies]` 中添加：

```toml
[dependencies]
tauri = { version = "2", features = [] }
tauri-plugin-dialog = "2"
tauri-plugin-shell = "2"
serde = { version = "1", features = ["derive"] }
serde_json = "1"
```

- [ ] **Step 8: 设置 App.vue 基础结构**

编辑 `desktop/src/main.ts`：

```typescript
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './style.css'

const app = createApp(App)
app.use(createPinia())
app.mount('#app')
```

编辑 `desktop/src/App.vue`：

```vue
<script setup lang="ts">
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import PanelLayout from '@/components/Panel/PanelLayout.vue'
import BottomBar from '@/components/BottomBar.vue'
import { useAgentStore } from '@/stores/agent'

const agentStore = useAgentStore()
agentStore.startPolling()
</script>

<template>
  <div class="flex h-screen overflow-hidden" style="background: var(--bg-primary)">
    <SidebarView />
    <div class="flex flex-col flex-1 overflow-hidden">
      <PanelLayout />
      <BottomBar />
    </div>
  </div>
</template>
```

- [ ] **Step 9: 验证项目启动**

```bash
cd /Users/xushixin/workspace/super-debug/desktop

# 先单独跑前端（不启动 Tauri 壳）
pnpm dev
```

预期：浏览器打开 `http://localhost:5173`，看到空白页面无报错。

- [ ] **Step 10: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/
git commit -m "feat(desktop): Tauri + Vue 3 项目脚手架"
```

---

## Task 2: HTTP API 封装 + agentStore（服务状态轮询）

**Files:**
- Create: `desktop/src/api/agent.ts`
- Create: `desktop/src/stores/agent.ts`

- [ ] **Step 1: 创建 HTTP 客户端封装**

创建 `desktop/src/api/agent.ts`：

```typescript
// Package api 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。
const BASE = 'http://localhost:27017'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

export interface Service {
  id: string
  project_id: string
  name: string
  status: '' | 'starting' | 'running' | 'failed'
  pid?: number
  command: string
  work_dir: string
  required: boolean
  order: number
  env_file?: string
  env?: Record<string, string>
}

export interface Project {
  id: string
  name: string
  root_path: string
  services: Service[]
  selected_service_ids: string[]
}

export interface LogEntry {
  id: number
  service_id: string
  run_id: string
  timestamp: string
  level: string
  message: string
  stream: string
}

export interface LogRule {
  id: string
  name: string
  type: 'include' | 'exclude'
  keywords: string[]
  logic: 'and' | 'or'
  enabled: boolean
}

export interface FetchLogsParams {
  service?: string
  run?: string
  limit?: number
  before?: number
}

export const api = {
  // 项目
  listProjects: () => request<Project[]>('/api/projects'),
  addProject: (root_path: string) =>
    request<Project>('/api/projects', { method: 'POST', body: JSON.stringify({ root_path }) }),
  deleteProject: (id: string) =>
    request<void>(`/api/projects/${id}`, { method: 'DELETE' }),
  getProjectRules: (id: string) => request<LogRule[]>(`/api/projects/${id}/rules`),
  putProjectRules: (id: string, rules: LogRule[]) =>
    request<LogRule[]>(`/api/projects/${id}/rules`, { method: 'PUT', body: JSON.stringify(rules) }),

  // 服务
  listServices: (projectId?: string) => {
    const qs = projectId ? `?project_id=${projectId}` : ''
    return request<Service[]>(`/api/services${qs}`)
  },
  startService: (id: string) =>
    request<void>(`/api/services/${id}/start`, { method: 'POST' }),
  stopService: (id: string) =>
    request<void>(`/api/services/${id}/stop`, { method: 'POST' }),
  restartService: (id: string) =>
    request<void>(`/api/services/${id}/restart`, { method: 'POST' }),
  startSelected: (projectId: string) =>
    request<void>(`/api/projects/${projectId}/start-selected`, { method: 'POST' }),

  // 日志
  fetchLogs: (params: FetchLogsParams) => {
    const qs = new URLSearchParams()
    if (params.service) qs.set('service', params.service)
    if (params.run) qs.set('run', params.run)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before) qs.set('before', String(params.before))
    return request<LogEntry[]>(`/api/logs?${qs}`)
  },
}
```

- [ ] **Step 2: 创建 agentStore**

创建 `desktop/src/stores/agent.ts`：

```typescript
// agentStore 负责轮询 agent 获取项目和服务列表，维护连接状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Project, type Service } from '@/api/agent'

export const useAgentStore = defineStore('agent', () => {
  const projects = ref<Project[]>([])
  const connected = ref(false)
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function fetchProjects() {
    try {
      projects.value = await api.listProjects()
      connected.value = true
    } catch {
      connected.value = false
    }
  }

  async function refreshServices() {
    if (!connected.value) return
    try {
      const services = await api.listServices()
      // 用最新状态更新 projects 里的 services，保留其他字段
      for (const project of projects.value) {
        const updated = services.filter(s => s.project_id === project.id)
        if (updated.length > 0) {
          project.services = updated
        }
      }
    } catch {
      connected.value = false
    }
  }

  function startPolling() {
    fetchProjects()
    pollTimer = setInterval(refreshServices, 2000)
  }

  function stopPolling() {
    if (pollTimer) clearInterval(pollTimer)
  }

  async function addProject(rootPath: string) {
    const project = await api.addProject(rootPath)
    projects.value.push(project)
    return project
  }

  async function deleteProject(id: string) {
    await api.deleteProject(id)
    projects.value = projects.value.filter(p => p.id !== id)
  }

  async function startService(id: string) {
    await api.startService(id)
  }

  async function stopService(id: string) {
    await api.stopService(id)
  }

  async function restartService(id: string) {
    await api.restartService(id)
  }

  async function startSelected(projectId: string) {
    await api.startSelected(projectId)
  }

  const allServices = computed<Service[]>(() =>
    projects.value.flatMap(p => p.services)
  )

  function serviceById(id: string): Service | undefined {
    return allServices.value.find(s => s.id === id)
  }

  function projectById(id: string): Project | undefined {
    return projects.value.find(p => p.id === id)
  }

  return {
    projects,
    connected,
    allServices,
    startPolling,
    stopPolling,
    fetchProjects,
    addProject,
    deleteProject,
    startService,
    stopService,
    restartService,
    startSelected,
    serviceById,
    projectById,
  }
})
```

- [ ] **Step 3: 验证（手动）**

确保 agent 在本地运行：

```bash
cd /Users/xushixin/workspace/super-debug/agent
go run . --addr :27017 --data /tmp/superdev-test
```

在另一个终端：

```bash
cd /Users/xushixin/workspace/super-debug/desktop
pnpm dev
```

打开浏览器控制台，执行：

```javascript
// 在 Vue Devtools 或控制台中验证 store
const { useAgentStore } = await import('/src/stores/agent.ts')
// 或用 Vue Devtools 查看 Pinia stores
```

预期：`agentStore.projects` 有数据，`connected` 为 `true`。

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/api/ desktop/src/stores/agent.ts
git commit -m "feat(desktop): HTTP API 封装和 agentStore 状态轮询"
```

---

## Task 3: panelStore — 面板布局树

**Files:**
- Create: `desktop/src/stores/panel.ts`

面板布局树是整个 UI 的核心数据结构，其他所有组件都依赖它。

- [ ] **Step 1: 创建 panelStore**

创建 `desktop/src/stores/panel.ts`：

```typescript
// panelStore 维护面板布局树（递归 PanelNode 结构）和焦点状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { v4 as uuidv4 } from 'uuid'

export type PanelAxis = 'h' | 'v'

export interface PanelLeafNode {
  type: 'leaf'
  id: string
  serviceId: string | null
  projectId: string | null
}

export interface PanelSplitNode {
  type: 'split'
  id: string
  axis: PanelAxis
  ratio: number  // 0~1，first 面板占比
  first: PanelNode
  second: PanelNode
}

export type PanelNode = PanelLeafNode | PanelSplitNode

function makeLeaf(serviceId: string | null = null, projectId: string | null = null): PanelLeafNode {
  return { type: 'leaf', id: uuidv4(), serviceId, projectId }
}

function getAllLeaves(node: PanelNode): PanelLeafNode[] {
  if (node.type === 'leaf') return [node]
  return [...getAllLeaves(node.first), ...getAllLeaves(node.second)]
}

// 在树中找到 id 对应的叶子节点，替换为 split 节点
function splitLeafById(
  node: PanelNode,
  leafId: string,
  axis: PanelAxis,
  newServiceId: string | null,
  newProjectId: string | null,
  newSide: 'first' | 'second'
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    const newLeaf = makeLeaf(newServiceId, newProjectId)
    const split: PanelSplitNode = {
      type: 'split',
      id: uuidv4(),
      axis,
      ratio: 0.5,
      first: newSide === 'first' ? newLeaf : node,
      second: newSide === 'second' ? newLeaf : node,
    }
    return split
  }
  return {
    ...node,
    first: splitLeafById(node.first, leafId, axis, newServiceId, newProjectId, newSide),
    second: splitLeafById(node.second, leafId, axis, newServiceId, newProjectId, newSide),
  }
}

// 替换指定叶子的 scope（serviceId/projectId）
function replaceScopeById(
  node: PanelNode,
  leafId: string,
  serviceId: string | null,
  projectId: string | null
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    return { ...node, serviceId, projectId }
  }
  return {
    ...node,
    first: replaceScopeById(node.first, leafId, serviceId, projectId),
    second: replaceScopeById(node.second, leafId, serviceId, projectId),
  }
}

// 删除指定叶子，用兄弟节点替代父 split 节点
function removeLeafById(node: PanelNode, leafId: string): PanelNode | null {
  if (node.type === 'leaf') {
    return node.id === leafId ? null : node
  }
  const newFirst = removeLeafById(node.first, leafId)
  const newSecond = removeLeafById(node.second, leafId)
  if (!newFirst) return newSecond
  if (!newSecond) return newFirst
  return { ...node, first: newFirst, second: newSecond }
}

const STORAGE_KEY = 'superdev:panel-layout'

function loadLayout(): PanelNode {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw) as PanelNode
  } catch {}
  return makeLeaf()
}

export const usePanelStore = defineStore('panel', () => {
  const root = ref<PanelNode>(loadLayout())
  const focusedPanelId = ref<string | null>(null)

  const allLeaves = computed(() => getAllLeaves(root.value))

  function save() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(root.value))
  }

  function ensureFocused() {
    const leaves = allLeaves.value
    if (!leaves.length) return
    if (focusedPanelId.value && leaves.some(l => l.id === focusedPanelId.value)) return
    focusedPanelId.value = leaves[0].id
  }

  function setFocus(panelId: string) {
    focusedPanelId.value = panelId
  }

  function splitLeaf(
    leafId: string,
    axis: PanelAxis,
    newServiceId: string | null,
    newProjectId: string | null,
    newSide: 'first' | 'second'
  ) {
    root.value = splitLeafById(root.value, leafId, axis, newServiceId, newProjectId, newSide)
    save()
    ensureFocused()
  }

  function replaceScope(leafId: string, serviceId: string | null, projectId: string | null) {
    root.value = replaceScopeById(root.value, leafId, serviceId, projectId)
    save()
  }

  function removeLeaf(leafId: string) {
    const newRoot = removeLeafById(root.value, leafId)
    root.value = newRoot ?? makeLeaf()
    save()
    ensureFocused()
  }

  // 当前焦点面板的目标 panelId（有焦点用焦点，否则用第一个）
  function targetPanelId(): string | null {
    const leaves = allLeaves.value
    if (!leaves.length) return null
    const focused = focusedPanelId.value
    if (focused && leaves.some(l => l.id === focused)) return focused
    return leaves[0].id
  }

  ensureFocused()

  return {
    root,
    focusedPanelId,
    allLeaves,
    setFocus,
    splitLeaf,
    replaceScope,
    removeLeaf,
    targetPanelId,
  }
})
```

- [ ] **Step 2: 验证（单元测试）**

创建 `desktop/src/stores/__tests__/panel.test.ts`：

```typescript
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { usePanelStore } from '../panel'

describe('panelStore', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('初始状态：单个空叶子节点', () => {
    const store = usePanelStore()
    expect(store.root.type).toBe('leaf')
    expect(store.allLeaves).toHaveLength(1)
  })

  it('splitLeaf：叶子节点变为 split，包含 2 个叶子', () => {
    const store = usePanelStore()
    const leafId = store.root.id
    store.splitLeaf(leafId, 'h', 'svc-1', 'proj-1', 'second')
    expect(store.root.type).toBe('split')
    expect(store.allLeaves).toHaveLength(2)
  })

  it('removeLeaf：删除一个叶子后回到单面板', () => {
    const store = usePanelStore()
    const leafId = store.root.id
    store.splitLeaf(leafId, 'h', 'svc-1', 'proj-1', 'second')
    const [leaf1, leaf2] = store.allLeaves
    store.removeLeaf(leaf2.id)
    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].id).toBe(leaf1.id)
  })

  it('replaceScope：更新叶子的 serviceId', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceScope(leafId, 'svc-abc', 'proj-xyz')
    expect(store.allLeaves[0].serviceId).toBe('svc-abc')
  })
})
```

安装 Vitest：

```bash
cd /Users/xushixin/workspace/super-debug/desktop
pnpm add -D vitest @vue/test-utils jsdom @vitest/ui
```

在 `vite.config.ts` 的 `defineConfig` 中添加：

```typescript
  test: {
    environment: 'jsdom',
    globals: true,
  },
```

运行测试：

```bash
pnpm vitest run src/stores/__tests__/panel.test.ts
```

预期：4 个测试全部通过。

- [ ] **Step 3: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/stores/panel.ts desktop/src/stores/__tests__/
git commit -m "feat(desktop): panelStore 面板布局树和持久化"
```

---

## Task 4: filterStore + logStore（日志缓冲和 WebSocket）

**Files:**
- Create: `desktop/src/stores/filter.ts`
- Create: `desktop/src/stores/log.ts`

- [ ] **Step 1: 创建 filterStore**

创建 `desktop/src/stores/filter.ts`：

```typescript
// filterStore 按 panelId 维护临时 chip 过滤状态，按 projectId 缓存 LogRule。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogRule, type LogEntry } from '@/api/agent'

export type ChipType = 'include' | 'exclude'
export type ChipLogic = 'and' | 'or'

export interface FilterChip {
  id: string
  keyword: string
  type: ChipType
}

interface PanelFilter {
  chips: FilterChip[]
  logic: ChipLogic
  nextChipType: ChipType
}

export const useFilterStore = defineStore('filter', () => {
  // 每个面板的临时 chip 状态
  const panelFilters = ref<Record<string, PanelFilter>>({})
  // 每个项目的 LogRule 缓存
  const projectRules = ref<Record<string, LogRule[]>>({})

  function getPanel(panelId: string): PanelFilter {
    if (!panelFilters.value[panelId]) {
      panelFilters.value[panelId] = { chips: [], logic: 'or', nextChipType: 'include' }
    }
    return panelFilters.value[panelId]
  }

  function addChip(panelId: string, keyword: string, type: ChipType) {
    const trimmed = keyword.trim()
    if (!trimmed) return
    const panel = getPanel(panelId)
    if (panel.chips.some(c => c.keyword.toLowerCase() === trimmed.toLowerCase())) return
    panel.chips.push({ id: uuidv4(), keyword: trimmed, type })
  }

  function removeChip(panelId: string, chipId: string) {
    const panel = getPanel(panelId)
    panel.chips = panel.chips.filter(c => c.id !== chipId)
  }

  function toggleChipType(panelId: string, chipId: string) {
    const panel = getPanel(panelId)
    const chip = panel.chips.find(c => c.id === chipId)
    if (chip) chip.type = chip.type === 'include' ? 'exclude' : 'include'
  }

  function toggleLogic(panelId: string) {
    const panel = getPanel(panelId)
    panel.logic = panel.logic === 'and' ? 'or' : 'and'
  }

  function setNextChipType(panelId: string, type: ChipType) {
    getPanel(panelId).nextChipType = type
  }

  function clearChips(panelId: string) {
    if (panelFilters.value[panelId]) {
      panelFilters.value[panelId].chips = []
    }
  }

  function removePanel(panelId: string) {
    delete panelFilters.value[panelId]
  }

  async function loadProjectRules(projectId: string) {
    const rules = await api.getProjectRules(projectId)
    projectRules.value[projectId] = rules
  }

  async function saveProjectRules(projectId: string, rules: LogRule[]) {
    const saved = await api.putProjectRules(projectId, rules)
    projectRules.value[projectId] = saved
  }

  function toggleRule(projectId: string, ruleId: string) {
    const rules = projectRules.value[projectId]
    if (!rules) return
    const rule = rules.find(r => r.id === ruleId)
    if (rule) rule.enabled = !rule.enabled
    // 异步持久化，不阻塞 UI
    saveProjectRules(projectId, rules)
  }

  // 核心过滤函数：先应用 LogRule，再应用 chip
  function applyFilters(panelId: string, projectId: string | null, logs: LogEntry[]): LogEntry[] {
    let result = logs

    // 应用项目级 LogRule
    const rules = projectId ? (projectRules.value[projectId] ?? []) : []
    const enabledRules = rules.filter(r => r.enabled)

    for (const rule of enabledRules) {
      if (rule.type === 'include') {
        result = result.filter(log =>
          rule.logic === 'and'
            ? rule.keywords.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
            : rule.keywords.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
        )
      } else {
        result = result.filter(log =>
          rule.logic === 'and'
            ? !rule.keywords.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
            : !rule.keywords.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
        )
      }
    }

    // 应用面板临时 chip
    const panel = panelFilters.value[panelId]
    if (!panel || panel.chips.length === 0) return result

    const includes = panel.chips.filter(c => c.type === 'include').map(c => c.keyword)
    const excludes = panel.chips.filter(c => c.type === 'exclude').map(c => c.keyword)
    const logic = panel.logic

    if (includes.length > 0) {
      result = result.filter(log =>
        logic === 'and'
          ? includes.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
          : includes.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
      )
    }
    if (excludes.length > 0) {
      result = result.filter(log =>
        logic === 'and'
          ? !excludes.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
          : !excludes.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
      )
    }

    return result
  }

  return {
    panelFilters,
    projectRules,
    getPanel,
    addChip,
    removeChip,
    toggleChipType,
    toggleLogic,
    setNextChipType,
    clearChips,
    removePanel,
    loadProjectRules,
    saveProjectRules,
    toggleRule,
    applyFilters,
  }
})
```

- [ ] **Step 2: 创建 logStore**

创建 `desktop/src/stores/log.ts`：

```typescript
// logStore 按 serviceId 维护日志缓冲和 WebSocket 连接，多面板共享同一连接。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { type LogEntry } from '@/api/agent'

const WS_BASE = 'ws://localhost:27017'
const MAX_LOGS = 8000  // 内存最多保留 8000 条

interface ServiceLog {
  logs: LogEntry[]
  ws: WebSocket | null
  refCount: number  // 订阅该 serviceId 的面板数量
}

export const useLogStore = defineStore('log', () => {
  const serviceLogs = ref<Record<string, ServiceLog>>({})
  // 按 serviceId 存历史查看的日志（与实时分开）
  const historyLogs = ref<Record<string, LogEntry[]>>({})

  function getOrCreate(serviceId: string): ServiceLog {
    if (!serviceLogs.value[serviceId]) {
      serviceLogs.value[serviceId] = { logs: [], ws: null, refCount: 0 }
    }
    return serviceLogs.value[serviceId]
  }

  function subscribe(serviceId: string) {
    const entry = getOrCreate(serviceId)
    entry.refCount++
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) return
    // 建立 WebSocket 连接
    const ws = new WebSocket(`${WS_BASE}/ws/logs?service=${serviceId}`)
    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data) as LogEntry
        entry.logs.push(log)
        // 超出上限时从头部删除（环形缓冲效果）
        if (entry.logs.length > MAX_LOGS) {
          entry.logs.splice(0, entry.logs.length - MAX_LOGS)
        }
      } catch {}
    }
    ws.onclose = () => {
      entry.ws = null
    }
    entry.ws = ws
  }

  function unsubscribe(serviceId: string) {
    const entry = serviceLogs.value[serviceId]
    if (!entry) return
    entry.refCount = Math.max(0, entry.refCount - 1)
    if (entry.refCount === 0 && entry.ws) {
      entry.ws.close()
      entry.ws = null
    }
  }

  function getLogs(serviceId: string): LogEntry[] {
    return serviceLogs.value[serviceId]?.logs ?? []
  }

  async function loadHistoryLogs(serviceId: string, runId: string) {
    const { api } = await import('@/api/agent')
    const logs = await api.fetchLogs({ service: serviceId, run: runId, limit: 2000 })
    historyLogs.value[serviceId] = logs
  }

  function clearHistoryLogs(serviceId: string) {
    delete historyLogs.value[serviceId]
  }

  function getHistoryLogs(serviceId: string): LogEntry[] {
    return historyLogs.value[serviceId] ?? []
  }

  // 获取某服务的所有历史 runId（从已缓存日志推断）
  function getRunIds(serviceId: string): string[] {
    const logs = serviceLogs.value[serviceId]?.logs ?? []
    const seen = new Set<string>()
    const runs: string[] = []
    for (const log of logs) {
      if (!seen.has(log.run_id)) {
        seen.add(log.run_id)
        runs.push(log.run_id)
      }
    }
    return runs.reverse()  // 最新的在前
  }

  return {
    serviceLogs,
    historyLogs,
    subscribe,
    unsubscribe,
    getLogs,
    loadHistoryLogs,
    clearHistoryLogs,
    getHistoryLogs,
    getRunIds,
  }
})
```

- [ ] **Step 3: 单元测试 filterStore**

创建 `desktop/src/stores/__tests__/filter.test.ts`：

```typescript
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useFilterStore } from '../filter'
import type { LogEntry } from '@/api/agent'

function makeLog(message: string, id = 1): LogEntry {
  return { id, service_id: 'svc', run_id: 'run', timestamp: '', level: 'INFO', message, stream: 'stdout' }
}

describe('filterStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('addChip 添加不重复的 chip', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    store.addChip('p1', 'error', 'include')  // 重复，不添加
    expect(store.getPanel('p1').chips).toHaveLength(1)
  })

  it('applyFilters：include chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    const logs = [makeLog('error occurred'), makeLog('info message')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })

  it('applyFilters：exclude chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'debug', 'exclude')
    const logs = [makeLog('debug info'), makeLog('error occurred')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })
})
```

运行测试：

```bash
cd /Users/xushixin/workspace/super-debug/desktop
pnpm vitest run src/stores/__tests__/filter.test.ts
```

预期：3 个测试全部通过。

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/stores/filter.ts desktop/src/stores/log.ts desktop/src/stores/__tests__/filter.test.ts
git commit -m "feat(desktop): filterStore 过滤逻辑和 logStore WebSocket 管理"
```

---

## Task 5: bookmarkStore（书签和同步组）

**Files:**
- Create: `desktop/src/stores/bookmark.ts`

- [ ] **Step 1: 创建 bookmarkStore**

创建 `desktop/src/stores/bookmark.ts`：

```typescript
// bookmarkStore 按 panelId 维护书签状态，支持单面板独立录制和多面板同步录制。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { LogEntry } from '@/api/agent'

export type BookmarkState = 'idle' | 'recording' | 'done'

export interface Bookmark {
  panelId: string
  serviceId: string | null
  state: BookmarkState
  startTime: Date | null
  endTime: Date | null
  lockedLogs: LogEntry[]
}

export const useBookmarkStore = defineStore('bookmark', () => {
  const bookmarks = ref<Record<string, Bookmark>>({})
  // 同步组：panelId 集合
  const syncPanelIds = ref<Set<string>>(new Set())
  const syncRecording = ref(false)

  function getBookmark(panelId: string): Bookmark | null {
    return bookmarks.value[panelId] ?? null
  }

  function startBookmark(panelId: string, serviceId: string | null) {
    bookmarks.value[panelId] = {
      panelId,
      serviceId,
      state: 'recording',
      startTime: new Date(),
      endTime: null,
      lockedLogs: [],
    }
  }

  function endBookmark(panelId: string) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    bm.endTime = new Date()
    bm.state = 'done'
  }

  function clearBookmark(panelId: string) {
    delete bookmarks.value[panelId]
  }

  // 录制过程中追加过滤后的日志
  function appendToBookmark(panelId: string, log: LogEntry) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    if (!bm.startTime || new Date(log.timestamp) < bm.startTime) return
    bm.lockedLogs.push(log)
  }

  // 格式化书签日志为可导出文本
  function formatBookmark(panelId: string): string {
    const bm = bookmarks.value[panelId]
    if (!bm) return ''
    return bm.lockedLogs
      .map(l => {
        const t = new Date(l.timestamp).toLocaleTimeString('en-US', { hour12: false })
        return `${t} [${l.service_id}] ${l.level.padEnd(5)} ${l.message}`
      })
      .join('\n')
  }

  // 格式化同步组所有书签（按服务分块）
  function formatSyncBookmarks(): string {
    const parts: string[] = []
    for (const panelId of syncPanelIds.value) {
      const bm = bookmarks.value[panelId]
      if (!bm) continue
      const header = `=== ${bm.serviceId ?? 'unknown'} ===`
      const body = bm.lockedLogs
        .map(l => {
          const t = new Date(l.timestamp).toLocaleTimeString('en-US', { hour12: false })
          return `${t} [${l.service_id}] ${l.level.padEnd(5)} ${l.message}`
        })
        .join('\n')
      parts.push(`${header}\n${body}`)
    }
    return parts.join('\n\n')
  }

  // 同步组操作
  function toggleSyncPanel(panelId: string, serviceId: string | null) {
    if (syncPanelIds.value.has(panelId)) {
      syncPanelIds.value.delete(panelId)
    } else {
      syncPanelIds.value.add(panelId)
    }
  }

  function startSyncBookmark() {
    const now = new Date()
    for (const panelId of syncPanelIds.value) {
      bookmarks.value[panelId] = {
        panelId,
        serviceId: null,  // 由面板组件填入实际 serviceId
        state: 'recording',
        startTime: now,  // 所有面板使用同一时间戳，保证对齐
        endTime: null,
        lockedLogs: [],
      }
    }
    syncRecording.value = true
  }

  function endSyncBookmark() {
    const now = new Date()
    for (const panelId of syncPanelIds.value) {
      const bm = bookmarks.value[panelId]
      if (bm && bm.state === 'recording') {
        bm.endTime = now
        bm.state = 'done'
      }
    }
    syncRecording.value = false
  }

  return {
    bookmarks,
    syncPanelIds,
    syncRecording,
    getBookmark,
    startBookmark,
    endBookmark,
    clearBookmark,
    appendToBookmark,
    formatBookmark,
    formatSyncBookmarks,
    toggleSyncPanel,
    startSyncBookmark,
    endSyncBookmark,
  }
})
```

- [ ] **Step 2: 单元测试**

创建 `desktop/src/stores/__tests__/bookmark.test.ts`：

```typescript
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useBookmarkStore } from '../bookmark'
import type { LogEntry } from '@/api/agent'

function makeLog(message: string, ts: string): LogEntry {
  return { id: 1, service_id: 'svc', run_id: 'run', timestamp: ts, level: 'INFO', message, stream: 'stdout' }
}

describe('bookmarkStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('startBookmark 后状态为 recording', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    expect(store.getBookmark('p1')?.state).toBe('recording')
  })

  it('endBookmark 后状态为 done', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    store.endBookmark('p1')
    expect(store.getBookmark('p1')?.state).toBe('done')
  })

  it('appendToBookmark 追加录制期间的日志', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const startTs = store.getBookmark('p1')!.startTime!
    const afterTs = new Date(startTs.getTime() + 1000).toISOString()
    store.appendToBookmark('p1', makeLog('hello', afterTs))
    expect(store.getBookmark('p1')?.lockedLogs).toHaveLength(1)
  })

  it('clearBookmark 清除书签', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    store.clearBookmark('p1')
    expect(store.getBookmark('p1')).toBeNull()
  })
})
```

```bash
cd /Users/xushixin/workspace/super-debug/desktop
pnpm vitest run src/stores/__tests__/bookmark.test.ts
```

预期：4 个测试全部通过。

- [ ] **Step 3: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/stores/bookmark.ts desktop/src/stores/__tests__/bookmark.test.ts
git commit -m "feat(desktop): bookmarkStore 单面板书签和多面板同步录制"
```

---

## Task 6: 侧边栏组件（SidebarView + ProjectHeader + ServiceRow）

**Files:**
- Create: `desktop/src/components/Sidebar/SidebarView.vue`
- Create: `desktop/src/components/Sidebar/ProjectHeader.vue`
- Create: `desktop/src/components/Sidebar/ServiceRow.vue`

- [ ] **Step 1: 创建 ServiceRow.vue**

创建 `desktop/src/components/Sidebar/ServiceRow.vue`：

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Service } from '@/api/agent'

const props = defineProps<{
  service: Service
  selected: boolean
}>()

const emit = defineEmits<{
  click: []
  dragstart: [serviceId: string]
}>()

const agentStore = useAgentStore()
const hovered = ref(false)

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function onDragStart(e: DragEvent) {
  e.dataTransfer?.setData('text/plain', props.service.id)
  emit('dragstart', props.service.id)
}
</script>

<template>
  <div
    class="service-row"
    :class="{ selected }"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @click="emit('click')"
    draggable="true"
    @dragstart="onDragStart"
  >
    <input
      type="checkbox"
      :checked="service.required || undefined"
      :disabled="service.required"
      @click.stop
      @change.stop
      class="service-checkbox"
    />
    <span class="status-dot" :style="{ background: statusColor(service.status) }" />
    <span class="service-name">{{ service.name }}</span>

    <Transition name="fade">
      <div v-if="hovered" class="hover-actions" @click.stop>
        <template v-if="service.status === 'running' || service.status === 'starting'">
          <button title="重启" @click="agentStore.restartService(service.id)">↺</button>
          <button title="停止" class="stop-btn" @click="agentStore.stopService(service.id)">⏹</button>
        </template>
        <template v-else>
          <button title="启动" class="start-btn" @click="agentStore.startService(service.id)">▶</button>
        </template>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.service-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px 3px 10px;
  border-radius: 4px;
  margin: 1px 4px;
  cursor: pointer;
  transition: background 0.12s;
}
.service-row:hover { background: rgba(255,255,255,0.04); }
.service-row.selected { background: rgba(31,111,235,0.12); }

.service-checkbox {
  width: 12px; height: 12px;
  accent-color: #1f6feb;
  flex-shrink: 0;
  cursor: pointer;
}

.status-dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.service-name {
  flex: 1;
  font-size: 12px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hover-actions {
  display: flex;
  gap: 3px;
  align-items: center;
  background: linear-gradient(to right, transparent, var(--bg-elevated) 40%);
  padding-left: 16px;
  position: absolute;
  right: 6px;
}
.hover-actions button {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
}
.hover-actions .stop-btn {
  background: rgba(248,81,73,0.1);
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}
.hover-actions .start-btn {
  background: rgba(63,185,80,0.1);
  border-color: rgba(63,185,80,0.3);
  color: #3fb950;
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.15s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
```

- [ ] **Step 2: 创建 ProjectHeader.vue**

创建 `desktop/src/components/Sidebar/ProjectHeader.vue`：

```vue
<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'

const props = defineProps<{ project: Project }>()
const agentStore = useAgentStore()

async function startSelected() {
  await agentStore.startSelected(props.project.id)
}

async function stopAll() {
  const running = props.project.services.filter(
    s => s.status === 'running' || s.status === 'starting'
  )
  await Promise.all(running.map(s => agentStore.stopService(s.id)))
}
</script>

<template>
  <div class="project-header">
    <span class="project-name">{{ project.name }}</span>
    <div class="project-actions">
      <button title="启动选中" class="action-btn start" @click.stop="startSelected">▶</button>
      <button title="全部停止" class="action-btn stop" @click.stop="stopAll">⏹</button>
    </div>
  </div>
</template>

<style scoped>
.project-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 8px 4px 10px;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}
.project-actions { display: flex; gap: 4px; }
.action-btn {
  background: transparent;
  border: none;
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.12s;
}
.action-btn:hover { background: rgba(255,255,255,0.08); }
.action-btn.start { color: #3fb950; }
.action-btn.stop { color: var(--text-secondary); }
</style>
```

- [ ] **Step 3: 创建 SidebarView.vue**

创建 `desktop/src/components/Sidebar/SidebarView.vue`：

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { usePanelStore } from '@/stores/panel'
import ProjectHeader from './ProjectHeader.vue'
import ServiceRow from './ServiceRow.vue'
import { open } from '@tauri-apps/plugin-dialog'

const agentStore = useAgentStore()
const panelStore = usePanelStore()

const focusedLeaf = computed(() =>
  panelStore.allLeaves.find(l => l.id === panelStore.focusedPanelId)
)

function isServiceSelected(serviceId: string) {
  return focusedLeaf.value?.serviceId === serviceId
}

function selectService(serviceId: string, projectId: string) {
  const panelId = panelStore.targetPanelId()
  if (!panelId) return
  panelStore.replaceScope(panelId, serviceId, projectId)
  panelStore.setFocus(panelId)
}

async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
  if (!selected || Array.isArray(selected)) return
  await agentStore.addProject(selected)
}
</script>

<template>
  <div class="sidebar">
    <div class="sidebar-scroll">
      <template v-for="project in agentStore.projects" :key="project.id">
        <ProjectHeader :project="project" />
        <ServiceRow
          v-for="service in project.services"
          :key="service.id"
          :service="service"
          :selected="isServiceSelected(service.id)"
          @click="selectService(service.id, project.id)"
        />
      </template>
    </div>
    <div class="add-project" @click="addProject">+ 添加项目</div>
  </div>
</template>

<style scoped>
.sidebar {
  width: 185px;
  min-width: 160px;
  max-width: 200px;
  background: var(--bg-primary);
  border-right: 1px solid var(--border-secondary);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  overflow: hidden;
}
.sidebar-scroll {
  flex: 1;
  overflow-y: auto;
  padding-bottom: 8px;
}
.add-project {
  padding: 8px 12px;
  border-top: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 11px;
  cursor: pointer;
  transition: color 0.12s;
}
.add-project:hover { color: var(--text-secondary); }
</style>
```

- [ ] **Step 4: 手动验证**

启动 agent，运行 `pnpm dev`，在浏览器中检查：
- 侧边栏显示 agent 返回的项目和服务列表
- 服务行鼠标悬停时右侧出现操作按钮
- 点击服务名可选中（高亮）

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/
git commit -m "feat(desktop): 侧边栏组件（项目头部、服务行、添加项目）"
```

---

## Task 7: 面板布局系统（PanelLayout + PanelLeaf + 拖放）

**Files:**
- Create: `desktop/src/composables/useDragDrop.ts`
- Create: `desktop/src/components/Panel/PanelLayout.vue`
- Create: `desktop/src/components/Panel/PanelLeaf.vue`

- [ ] **Step 1: 创建 useDragDrop.ts**

创建 `desktop/src/composables/useDragDrop.ts`：

```typescript
// useDragDrop 封装面板拖放逻辑：根据落点位置决定分栏方向。
import { ref } from 'vue'
import type { PanelAxis } from '@/stores/panel'

export type DropEdge = 'left' | 'right' | 'top' | 'bottom' | 'center'

export function getDropEdge(location: { x: number; y: number }, size: { w: number; h: number }): DropEdge {
  const { x, y } = location
  const { w, h } = size
  if (w <= 0 || h <= 0) return 'center'

  const innerW = w * 0.6
  const innerH = h * 0.6
  const innerLeft = (w - innerW) / 2
  const innerTop = (h - innerH) / 2

  if (x >= innerLeft && x <= innerLeft + innerW && y >= innerTop && y <= innerTop + innerH) {
    return 'center'
  }

  const edgeFraction = 0.2
  if (x < w * edgeFraction) return 'left'
  if (x > w * (1 - edgeFraction)) return 'right'
  if (y < h * edgeFraction) return 'top'
  if (y > h * (1 - edgeFraction)) return 'bottom'
  return 'center'
}

export function edgeToAxis(edge: DropEdge): { axis: PanelAxis; side: 'first' | 'second' } | null {
  if (edge === 'left') return { axis: 'h', side: 'first' }
  if (edge === 'right') return { axis: 'h', side: 'second' }
  if (edge === 'top') return { axis: 'v', side: 'first' }
  if (edge === 'bottom') return { axis: 'v', side: 'second' }
  return null
}

export function useDragDrop() {
  const dropHighlight = ref<DropEdge | null>(null)

  return { dropHighlight, getDropEdge, edgeToAxis }
}
```

- [ ] **Step 2: 创建 PanelLeaf.vue**

创建 `desktop/src/components/Panel/PanelLeaf.vue`：

```vue
<script setup lang="ts">
import { ref, computed } from 'vue'
import { usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop, type DropEdge } from '@/composables/useDragDrop'
import LogPanel from './LogPanel.vue'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  canClose: boolean
}>()

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const { dropHighlight, getDropEdge, edgeToAxis } = useDragDrop()

const panelEl = ref<HTMLElement | null>(null)
const isFocused = computed(() => panelStore.focusedPanelId === props.panelId)

const service = computed(() =>
  props.serviceId ? agentStore.serviceById(props.serviceId) : null
)

const headerTitle = computed(() => {
  if (service.value) return service.value.name
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    return proj ? `${proj.name} · 全部` : '未选择'
  }
  return '未选择'
})

function onDragOver(e: DragEvent) {
  e.preventDefault()
  if (!panelEl.value) return
  const rect = panelEl.value.getBoundingClientRect()
  dropHighlight.value = getDropEdge(
    { x: e.clientX - rect.left, y: e.clientY - rect.top },
    { w: rect.width, h: rect.height }
  )
}

function onDragLeave() {
  dropHighlight.value = null
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  const serviceId = e.dataTransfer?.getData('text/plain')
  if (!serviceId || !dropHighlight.value) return

  const edge: DropEdge = dropHighlight.value
  dropHighlight.value = null

  const svc = agentStore.serviceById(serviceId)
  const projectId = svc?.project_id ?? null

  if (edge === 'center') {
    panelStore.replaceScope(props.panelId, serviceId, projectId)
    panelStore.setFocus(props.panelId)
  } else {
    const split = edgeToAxis(edge)
    if (split) {
      panelStore.splitLeaf(props.panelId, split.axis, serviceId, projectId, split.side)
    }
  }
}

function highlightStyle(edge: DropEdge | null) {
  if (!edge) return {}
  const styles: Record<DropEdge, object> = {
    left:   { left: 0, top: 0, width: '20%', height: '100%' },
    right:  { right: 0, top: 0, width: '20%', height: '100%' },
    top:    { left: 0, top: 0, width: '100%', height: '20%' },
    bottom: { left: 0, bottom: 0, width: '100%', height: '20%' },
    center: { left: '20%', top: '20%', width: '60%', height: '60%' },
  }
  return styles[edge]
}
</script>

<template>
  <div
    ref="panelEl"
    class="panel-leaf"
    :class="{ focused: isFocused }"
    @click="panelStore.setFocus(panelId)"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <!-- Panel header -->
    <div class="panel-header">
      <span class="panel-title">{{ headerTitle }}</span>
      <button v-if="canClose" class="close-btn" @click.stop="panelStore.removeLeaf(panelId)">✕</button>
    </div>

    <!-- Log panel -->
    <LogPanel :panel-id="panelId" :service-id="serviceId" :project-id="projectId" />

    <!-- Drop highlight overlay -->
    <div
      v-if="dropHighlight"
      class="drop-overlay"
      :style="highlightStyle(dropHighlight)"
    />
  </div>
</template>

<style scoped>
.panel-leaf {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  min-width: 200px;
}
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 3px 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.panel-title {
  font-size: 11px;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.close-btn {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 10px;
  cursor: pointer;
  padding: 0 2px;
  line-height: 1;
}
.close-btn:hover { color: var(--text-primary); }

.drop-overlay {
  position: absolute;
  border-radius: 4px;
  background: rgba(31,111,235,0.25);
  border: 2px solid #1f6feb;
  pointer-events: none;
}
</style>
```

- [ ] **Step 3: 创建 PanelLayout.vue（递归）**

创建 `desktop/src/components/Panel/PanelLayout.vue`：

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { usePanelStore, type PanelNode } from '@/stores/panel'
import PanelLeaf from './PanelLeaf.vue'

const props = defineProps<{
  node?: PanelNode
  isRoot?: boolean
}>()

const panelStore = usePanelStore()
const node = computed(() => props.node ?? panelStore.root)
const isRoot = computed(() => props.isRoot ?? true)

const rootLeafCount = computed(() => panelStore.allLeaves.length)
</script>

<template>
  <div
    v-if="node.type === 'leaf'"
    class="panel-leaf-wrapper"
  >
    <PanelLeaf
      :panel-id="node.id"
      :service-id="node.serviceId"
      :project-id="node.projectId"
      :can-close="rootLeafCount > 1"
    />
  </div>

  <div
    v-else
    class="panel-split"
    :class="node.axis === 'h' ? 'split-h' : 'split-v'"
  >
    <div class="split-first">
      <PanelLayout :node="node.first" :is-root="false" />
    </div>
    <div class="split-divider" :class="node.axis === 'h' ? 'divider-v' : 'divider-h'" />
    <div class="split-second">
      <PanelLayout :node="node.second" :is-root="false" />
    </div>
  </div>
</template>

<style scoped>
.panel-leaf-wrapper {
  display: flex;
  flex: 1;
  overflow: hidden;
}
.panel-split {
  display: flex;
  flex: 1;
  overflow: hidden;
}
.split-h { flex-direction: row; }
.split-v { flex-direction: column; }
.split-first, .split-second {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.split-divider {
  background: var(--border-secondary);
  flex-shrink: 0;
}
.divider-v { width: 1px; cursor: col-resize; }
.divider-h { height: 1px; cursor: row-resize; }
</style>
```

- [ ] **Step 4: 手动验证**

运行 `pnpm dev`，在浏览器中验证：
- 空白面板显示"未选择"
- 从侧边栏拖拽服务到面板中央 → 替换服务
- 拖拽到面板右边缘 → 分为左右两栏
- 关闭按钮在有多个面板时出现
- 点击面板设置焦点（点击侧边栏服务时替换焦点面板）

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/composables/ desktop/src/components/Panel/PanelLayout.vue desktop/src/components/Panel/PanelLeaf.vue
git commit -m "feat(desktop): 面板布局系统（递归分栏、拖放分栏）"
```

---

## Task 8: LogPanel + PanelToolbar + LogRow（日志展示和工具栏）

**Files:**
- Create: `desktop/src/components/Panel/LogRow.vue`
- Create: `desktop/src/components/Panel/PanelToolbar.vue`
- Create: `desktop/src/components/Panel/LogPanel.vue`

- [ ] **Step 1: 创建 LogRow.vue**

创建 `desktop/src/components/Panel/LogRow.vue`：

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{
  log: LogEntry
  highlighted: boolean  // 书签录制区间内高亮
}>()

const SERVICE_COLORS = ['#58a6ff','#bc8cff','#f78166','#ffa657','#7ce38b','#39d353','#a5d6ff','#ff7b72']
function serviceColor(serviceId: string) {
  let hash = 0
  for (const c of serviceId) hash = (hash * 31 + c.charCodeAt(0)) & 0xffffffff
  return SERVICE_COLORS[Math.abs(hash) % SERVICE_COLORS.length]
}

const levelColor = computed(() => {
  if (props.log.level === 'ERROR') return '#f85149'
  if (props.log.level === 'WARN') return '#d29922'
  if (props.log.level === 'DEBUG') return '#6e7681'
  return '#3fb950'
})

const rowBg = computed(() => {
  if (props.highlighted) {
    if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.18)'
    if (props.log.level === 'WARN') return 'rgba(210,153,34,0.12)'
    return 'rgba(30,25,10,0.5)'
  }
  if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.10)'
  if (props.log.level === 'WARN') return 'rgba(210,153,34,0.07)'
  return 'transparent'
})

const time = computed(() => {
  const d = new Date(props.log.timestamp)
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
})
</script>

<template>
  <div class="log-row" :style="{ background: rowBg }">
    <span class="ts">{{ time }}</span>
    <span class="svc" :style="{ color: serviceColor(log.service_id) }">[{{ log.service_id.slice(0, 12) }}]</span>
    <span class="level" :style="{ color: levelColor }">{{ log.level.padEnd(5) }}</span>
    <span class="msg">{{ log.message }}</span>
  </div>
</template>

<style scoped>
.log-row {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 1px 8px;
  border-radius: 2px;
  font-size: 11px;
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.ts { color: var(--text-tertiary); flex-shrink: 0; }
.svc { flex-shrink: 0; }
.level { flex-shrink: 0; width: 48px; }
.msg { flex: 1; color: var(--text-primary); }
</style>
```

- [ ] **Step 2: 创建 PanelToolbar.vue**

创建 `desktop/src/components/Panel/PanelToolbar.vue`：

```vue
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  historyRunIds: string[]
  viewingRunId: string | null
}>()

const emit = defineEmits<{
  selectRun: [runId: string | null]
}>()

const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()

const chipInput = ref('')
const panel = computed(() => filterStore.getPanel(props.panelId))
const bookmark = computed(() => bookmarkStore.getBookmark(props.panelId))
const rules = computed(() => props.projectId ? (filterStore.projectRules[props.projectId] ?? []) : [])

function submitChip() {
  const parts = chipInput.value.split(/[,;\t\n]+/).map(s => s.trim()).filter(Boolean)
  for (const p of parts) {
    filterStore.addChip(props.panelId, p, panel.value.nextChipType)
  }
  chipInput.value = ''
}

function startBookmark() {
  bookmarkStore.startBookmark(props.panelId, props.serviceId)
}
function endBookmark() {
  bookmarkStore.endBookmark(props.panelId)
}
function clearBookmark() {
  bookmarkStore.clearBookmark(props.panelId)
}
async function copyBookmark() {
  const text = bookmarkStore.formatBookmark(props.panelId)
  await navigator.clipboard.writeText(text)
}
async function exportBookmark() {
  const { save } = await import('@tauri-apps/plugin-dialog')
  const { writeTextFile } = await import('@tauri-apps/plugin-fs')
  const path = await save({ defaultPath: `superdev-log-${Date.now()}.log` })
  if (path) {
    const text = bookmarkStore.formatBookmark(props.panelId)
    await writeTextFile(path, text)
  }
}
</script>

<template>
  <div class="toolbar">
    <!-- 过滤区 -->
    <div class="filter-area">
      <svg class="search-icon" width="12" height="12" viewBox="0 0 16 16" fill="none">
        <circle cx="6.5" cy="6.5" r="4.5" stroke="#6e7681" stroke-width="1.5"/>
        <line x1="10.5" y1="10.5" x2="14" y2="14" stroke="#6e7681" stroke-width="1.5" stroke-linecap="round"/>
      </svg>

      <!-- include/exclude picker -->
      <div class="segmented">
        <button
          :class="{ active: panel.nextChipType === 'include' }"
          @click="filterStore.setNextChipType(panelId, 'include')"
        >包含</button>
        <button
          :class="{ active: panel.nextChipType === 'exclude' }"
          @click="filterStore.setNextChipType(panelId, 'exclude')"
        >排除</button>
      </div>

      <!-- 关键词输入 -->
      <input
        v-model="chipInput"
        class="chip-input"
        :placeholder="panel.chips.length ? '添加关键词…' : '关键词过滤，回车添加'"
        @keydown.enter="submitChip"
      />

      <!-- chips -->
      <div
        v-for="chip in panel.chips"
        :key="chip.id"
        class="chip"
        :class="chip.type"
      >
        <button class="chip-type" @click="filterStore.toggleChipType(panelId, chip.id)">
          {{ chip.type === 'include' ? '+' : '−' }}
        </button>
        <span>{{ chip.keyword }}</span>
        <button class="chip-remove" @click="filterStore.removeChip(panelId, chip.id)">✕</button>
      </div>

      <!-- AND/OR toggle -->
      <button v-if="panel.chips.length > 1" class="logic-btn" @click="filterStore.toggleLogic(panelId)">
        {{ panel.logic.toUpperCase() }}
      </button>

      <!-- 项目规则快捷开关 -->
      <template v-if="rules.length">
        <div class="divider" />
        <button
          v-for="rule in rules"
          :key="rule.id"
          class="rule-chip"
          :class="{ enabled: rule.enabled }"
          @click="filterStore.toggleRule(projectId!, rule.id)"
          :title="rule.enabled ? '点击禁用' : '点击启用'"
        >
          <span class="rule-arrow">{{ rule.type === 'include' ? '↑' : '↓' }}</span>
          <span :class="{ strikethrough: !rule.enabled }">{{ rule.name || rule.keywords[0] }}</span>
        </button>
      </template>
    </div>

    <div class="flex-1" />

    <!-- 操作区 -->
    <button class="icon-btn" title="历史记录" @click="emit('selectRun', null)">
      🕐 历史
    </button>
    <button class="icon-btn" title="过滤规则">⚙</button>
    <button v-if="panel.chips.length" class="icon-btn" title="保存为规则">↓</button>

    <div class="divider" />

    <!-- 书签区 -->
    <template v-if="!bookmark || bookmark.state === 'idle'">
      <button class="bookmark-btn start" title="开始书签录制" @click="startBookmark">⏺</button>
    </template>
    <template v-else-if="bookmark.state === 'recording'">
      <span class="record-count">● {{ bookmark.lockedLogs.length }} 条</span>
      <button class="bookmark-btn stop" title="结束录制" @click="endBookmark">⏹</button>
    </template>
    <template v-else>
      <span class="done-count">{{ bookmark.lockedLogs.length }} 条</span>
      <button class="icon-btn" title="复制" @click="copyBookmark">⎘</button>
      <button class="icon-btn" title="导出" @click="exportBookmark">↑</button>
      <button class="icon-btn" title="清除" @click="clearBookmark">✕</button>
      <button class="bookmark-btn start" title="重新开始" @click="startBookmark">⏺</button>
    </template>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
  overflow-x: auto;
  min-height: 32px;
}
.filter-area {
  display: flex;
  align-items: center;
  gap: 5px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}
.search-icon { flex-shrink: 0; }
.segmented {
  display: flex;
  border: 1px solid var(--border);
  border-radius: 4px;
  overflow: hidden;
  flex-shrink: 0;
}
.segmented button {
  padding: 2px 7px;
  font-size: 10px;
  background: transparent;
  border: none;
  color: var(--text-secondary);
  cursor: pointer;
}
.segmented button.active {
  background: rgba(31,111,235,0.2);
  color: #58a6ff;
}
.chip-input {
  flex: 1;
  min-width: 80px;
  max-width: 150px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 7px;
  font-size: 10px;
  color: var(--text-primary);
  outline: none;
}
.chip {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 5px;
  border-radius: 4px;
  font-size: 10px;
  flex-shrink: 0;
}
.chip.include { background: rgba(31,111,235,0.12); border: 1px solid rgba(31,111,235,0.3); }
.chip.exclude { background: rgba(210,153,34,0.12); border: 1px solid rgba(210,153,34,0.3); }
.chip-type {
  background: transparent;
  border: none;
  font-size: 9px;
  font-weight: 700;
  cursor: pointer;
  padding: 0;
}
.chip.include .chip-type { color: #58a6ff; }
.chip.exclude .chip-type { color: #d29922; }
.chip-remove {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 8px;
  cursor: pointer;
  padding: 0;
}
.logic-btn {
  padding: 2px 6px;
  background: var(--bg-overlay);
  border: none;
  border-radius: 4px;
  color: var(--text-secondary);
  font-size: 10px;
  font-weight: 700;
  cursor: pointer;
  flex-shrink: 0;
}
.rule-chip {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 10px;
  cursor: pointer;
  border: 1px solid transparent;
  background: rgba(255,255,255,0.04);
  color: var(--text-secondary);
  flex-shrink: 0;
}
.rule-chip.enabled {
  background: rgba(31,111,235,0.10);
  border-color: rgba(31,111,235,0.25);
}
.rule-arrow { font-size: 9px; }
.strikethrough { text-decoration: line-through; opacity: 0.5; }

.divider { width: 1px; height: 14px; background: var(--border); flex-shrink: 0; margin: 0 2px; }
.flex-1 { flex: 1; }

.icon-btn {
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 11px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 3px;
  flex-shrink: 0;
  white-space: nowrap;
}
.icon-btn:hover { color: var(--text-primary); background: var(--bg-overlay); }

.bookmark-btn {
  background: transparent;
  border: none;
  font-size: 15px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
}
.bookmark-btn.start { color: #3fb950; }
.bookmark-btn.stop { color: #f85149; }

.record-count {
  padding: 2px 8px;
  background: rgba(248,81,73,0.1);
  border: 1px solid rgba(248,81,73,0.3);
  border-radius: 4px;
  color: #f85149;
  font-size: 10px;
  font-weight: 700;
  flex-shrink: 0;
}
.done-count {
  color: var(--text-secondary);
  font-size: 10px;
  flex-shrink: 0;
}
</style>
```

- [ ] **Step 3: 创建 LogPanel.vue**

创建 `desktop/src/components/Panel/LogPanel.vue`：

```vue
<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useLogStore } from '@/stores/log'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useAgentStore } from '@/stores/agent'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
}>()

const logStore = useLogStore()
const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const agentStore = useAgentStore()

const viewingRunId = ref<string | null>(null)
const isFollowing = ref(true)
const newLogCount = ref(0)
const logListEl = ref<HTMLElement | null>(null)

// 订阅 WebSocket
onMounted(() => {
  if (props.serviceId) logStore.subscribe(props.serviceId)
  if (props.projectId) filterStore.loadProjectRules(props.projectId)
})
onUnmounted(() => {
  if (props.serviceId) logStore.unsubscribe(props.serviceId)
  filterStore.removePanel(props.panelId)
})
watch(() => props.serviceId, (newId, oldId) => {
  if (oldId) logStore.unsubscribe(oldId)
  if (newId) logStore.subscribe(newId)
  viewingRunId.value = null
  isFollowing.value = true
})

// 原始日志（实时或历史）
const rawLogs = computed<LogEntry[]>(() => {
  if (viewingRunId.value && props.serviceId) {
    return logStore.getHistoryLogs(props.serviceId)
  }
  if (props.serviceId) return logStore.getLogs(props.serviceId)
  // 项目视图：合并所有服务日志
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    if (!proj) return []
    return proj.services
      .flatMap(s => logStore.getLogs(s.id))
      .sort((a, b) => a.id - b.id)
  }
  return []
})

// 过滤后的日志
const filteredLogs = computed(() =>
  filterStore.applyFilters(props.panelId, props.projectId, rawLogs.value)
)

// 书签高亮判断
const bookmark = computed(() => bookmarkStore.getBookmark(props.panelId))
function isHighlighted(log: LogEntry): boolean {
  const bm = bookmark.value
  if (!bm || !bm.startTime) return false
  const ts = new Date(log.timestamp)
  if (bm.state === 'recording') return ts >= bm.startTime
  if (bm.state === 'done' && bm.endTime) return ts >= bm.startTime && ts <= bm.endTime
  return false
}

// 书签录制：追加过滤后的新日志
watch(filteredLogs, (newLogs, oldLogs) => {
  const bm = bookmark.value
  if (!bm || bm.state !== 'recording') return
  const added = newLogs.slice(oldLogs.length)
  for (const log of added) {
    bookmarkStore.appendToBookmark(props.panelId, log)
  }
})

// 自动跟随底部
async function scrollToBottom() {
  await nextTick()
  if (logListEl.value) {
    logListEl.value.scrollTop = logListEl.value.scrollHeight
  }
}

watch(filteredLogs, (newLogs, oldLogs) => {
  const added = newLogs.length - oldLogs.length
  if (added <= 0) return
  if (isFollowing.value) {
    scrollToBottom()
  } else {
    newLogCount.value += added
  }
})

function onScroll() {
  if (!logListEl.value) return
  const { scrollTop, scrollHeight, clientHeight } = logListEl.value
  const distFromBottom = scrollHeight - scrollTop - clientHeight
  if (distFromBottom < 50) {
    isFollowing.value = true
    newLogCount.value = 0
  } else {
    isFollowing.value = false
  }
}

function jumpToBottom() {
  isFollowing.value = true
  newLogCount.value = 0
  scrollToBottom()
}

// 历史记录
const historyRunIds = computed(() =>
  props.serviceId ? logStore.getRunIds(props.serviceId) : []
)

async function selectRun(runId: string | null) {
  viewingRunId.value = runId
  if (runId && props.serviceId) {
    await logStore.loadHistoryLogs(props.serviceId, runId)
  }
  isFollowing.value = false
  await nextTick()
  scrollToBottom()
}

// 统计
const stats = computed(() => {
  let errors = 0, warns = 0
  for (const log of filteredLogs.value) {
    if (log.level === 'ERROR') errors++
    else if (log.level === 'WARN') warns++
  }
  return { total: filteredLogs.value.length, errors, warns }
})

onMounted(scrollToBottom)
</script>

<template>
  <div class="log-panel">
    <PanelToolbar
      :panel-id="panelId"
      :service-id="serviceId"
      :project-id="projectId"
      :history-run-ids="historyRunIds"
      :viewing-run-id="viewingRunId"
      @select-run="selectRun"
    />

    <!-- 历史 banner -->
    <div v-if="viewingRunId" class="history-banner">
      <span>🕐 查看历史记录 · {{ stats.total }} 条</span>
      <button @click="selectRun(null)">返回实时</button>
    </div>

    <!-- 日志列表 -->
    <div
      ref="logListEl"
      class="log-list"
      @scroll="onScroll"
    >
      <LogRow
        v-for="log in filteredLogs"
        :key="log.id"
        :log="log"
        :highlighted="isHighlighted(log)"
      />
    </div>

    <!-- 新日志提示 -->
    <Transition name="fade">
      <button v-if="!isFollowing && newLogCount > 0" class="new-log-pill" @click="jumpToBottom">
        ↓ {{ newLogCount }} 条新日志
      </button>
    </Transition>

    <!-- 状态栏 -->
    <div class="status-bar">
      <span>{{ viewingRunId ? '历史' : '实时' }} · 显示 {{ stats.total }} 条</span>
      <div class="status-badges">
        <span v-if="stats.errors > 0" class="badge error">● {{ stats.errors }} 错误</span>
        <span v-if="stats.warns > 0" class="badge warn">● {{ stats.warns }} 警告</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  position: relative;
}
.history-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 12px;
  background: rgba(210,153,34,0.15);
  border-bottom: 1px solid var(--border-secondary);
  font-size: 12px;
  flex-shrink: 0;
}
.history-banner button {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-secondary);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
}
.log-list {
  flex: 1;
  overflow-y: auto;
  background: var(--bg-primary);
  padding: 4px 0;
}
.new-log-pill {
  position: absolute;
  bottom: 32px;
  right: 12px;
  background: #1f6feb;
  color: #fff;
  border: none;
  border-radius: 12px;
  padding: 4px 12px;
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0,0,0,0.4);
}
.status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 10px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border-secondary);
  font-size: 10px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}
.status-badges { display: flex; gap: 8px; }
.badge { font-size: 9px; padding: 1px 6px; border-radius: 3px; }
.badge.error { color: #f85149; background: rgba(248,81,73,0.1); }
.badge.warn { color: #d29922; background: rgba(210,153,34,0.1); }

.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
```

- [ ] **Step 4: 手动验证**

运行 `pnpm dev`，验证：
- 选择一个服务后，日志面板实时显示日志流
- 关键词 chip 过滤即时生效
- 滚动离开底部后出现"N 条新日志"提示
- 点击 ⏺ 开始书签，再点 ⏹ 停止，显示条数

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Panel/
git commit -m "feat(desktop): LogPanel 日志展示、过滤 chip、书签录制"
```

---

## Task 9: BottomBar（面板操作栏）

**Files:**
- Create: `desktop/src/components/BottomBar.vue`

- [ ] **Step 1: 创建 BottomBar.vue**

创建 `desktop/src/components/BottomBar.vue`：

```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()

// 所有面板中的服务（去重）
const panelServices = computed(() => {
  const seen = new Set<string>()
  const result = []
  for (const leaf of panelStore.allLeaves) {
    if (leaf.serviceId && !seen.has(leaf.serviceId)) {
      seen.add(leaf.serviceId)
      const svc = agentStore.serviceById(leaf.serviceId)
      if (svc) result.push(svc)
    }
  }
  return result
})

// 底部栏勾选状态（独立于侧边栏 selected_service_ids）
const checkedIds = ref<Set<string>>(new Set())

function toggleCheck(serviceId: string) {
  if (checkedIds.value.has(serviceId)) {
    checkedIds.value.delete(serviceId)
  } else {
    checkedIds.value.add(serviceId)
  }
}

async function restartChecked() {
  await Promise.all([...checkedIds.value].map(id => agentStore.restartService(id)))
}

async function stopChecked() {
  await Promise.all([...checkedIds.value].map(id => agentStore.stopService(id)))
}

// 同步录制
const syncEnabled = ref(false)
const syncRecording = computed(() => bookmarkStore.syncRecording)

function toggleSync() {
  syncEnabled.value = !syncEnabled.value
  if (syncEnabled.value) {
    // 把所有面板加入同步组
    for (const leaf of panelStore.allLeaves) {
      if (leaf.serviceId) {
        bookmarkStore.toggleSyncPanel(leaf.id, leaf.serviceId)
        bookmarkStore.syncPanelIds.add(leaf.id)
      }
    }
  } else {
    bookmarkStore.syncPanelIds.clear()
  }
}

function toggleSyncRecord() {
  if (syncRecording.value) {
    bookmarkStore.endSyncBookmark()
  } else {
    bookmarkStore.startSyncBookmark()
  }
}

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}
</script>

<template>
  <div class="bottom-bar">
    <span class="label">面板服务</span>

    <div class="service-chips">
      <div
        v-for="svc in panelServices"
        :key="svc.id"
        class="service-chip"
      >
        <input
          type="checkbox"
          :checked="checkedIds.has(svc.id)"
          @change="toggleCheck(svc.id)"
          style="accent-color: #1f6feb; width: 11px; height: 11px; cursor: pointer;"
        />
        <span class="dot" :style="{ background: statusColor(svc.status) }" />
        <span class="svc-name">{{ svc.name }}</span>
      </div>
    </div>

    <template v-if="checkedIds.size > 0">
      <div class="divider" />
      <button class="action-btn" @click="restartChecked">↺ 重启</button>
      <button class="action-btn danger" @click="stopChecked">⏹ 停止</button>
    </template>

    <div class="divider" />

    <!-- 同步录制 -->
    <label class="sync-label">
      <input type="checkbox" :checked="syncEnabled" @change="toggleSync" style="accent-color:#1f6feb;" />
      <span>同步录制</span>
    </label>
    <button
      v-if="syncEnabled"
      class="sync-record-btn"
      :class="{ recording: syncRecording }"
      @click="toggleSyncRecord"
    >
      {{ syncRecording ? '⏹' : '⏺' }}
    </button>

    <div class="flex-1" />

    <!-- Agent 状态 -->
    <div class="agent-status">
      <span class="agent-dot" :class="{ connected: agentStore.connected }" />
      <span>localhost:27017</span>
    </div>
  </div>
</template>

<style scoped>
.bottom-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  min-height: 30px;
  overflow-x: auto;
}
.label {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  flex-shrink: 0;
}
.service-chips { display: flex; gap: 10px; align-items: center; }
.service-chip { display: flex; align-items: center; gap: 4px; }
.dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.svc-name { font-size: 11px; color: var(--text-primary); white-space: nowrap; }

.divider { width: 1px; height: 14px; background: var(--border); flex-shrink: 0; }
.flex-1 { flex: 1; }

.action-btn {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.action-btn:hover { background: var(--bg-overlay); }
.action-btn.danger {
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}

.sync-label {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.sync-record-btn {
  background: transparent;
  border: none;
  font-size: 15px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  color: #3fb950;
}
.sync-record-btn.recording { color: #f85149; }

.agent-status {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 10px;
  color: var(--text-tertiary);
  white-space: nowrap;
  flex-shrink: 0;
}
.agent-dot {
  width: 6px; height: 6px;
  border-radius: 50%;
  background: #6e7681;
}
.agent-dot.connected { background: #3fb950; }
</style>
```

- [ ] **Step 2: 手动验证**

在浏览器中验证：
- 底部栏显示当前所有面板中的服务（去重）
- 勾选服务后"重启"/"停止"按钮出现
- 勾选"同步录制"后，⏺ 按钮出现，点击开始同步书签
- 右下角 agent 连接状态绿点正常显示

- [ ] **Step 3: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/BottomBar.vue
git commit -m "feat(desktop): 底部面板操作栏（批量启停、同步书签录制）"
```

---

## Task 10: 系统托盘 + Tauri 打包验证

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`
- Create: `desktop/src-tauri/icons/`（图标文件）

- [ ] **Step 1: 添加系统托盘**

编辑 `desktop/src-tauri/src/main.rs`，替换为：

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
use agent::AgentProcess;
use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, TrayIconBuilder, TrayIconEvent},
    Manager,
};

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            // 启动 agent（仅 release 模式）
            let agent = AgentProcess::new();
            #[cfg(not(debug_assertions))]
            {
                let resource_path = app
                    .path()
                    .resource_dir()
                    .unwrap()
                    .join("binaries/superdev-agent");
                agent.start(resource_path.to_str().unwrap());
            }
            app.manage(agent);

            // 系统托盘
            let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;

            TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
                .menu(&menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => {
                        if let Some(w) = app.get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
                    }
                    "quit" => {
                        app.state::<AgentProcess>().stop();
                        app.exit(0);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click { button: MouseButton::Left, .. } = event {
                        if let Some(w) = tray.app_handle().get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
                    }
                })
                .build(app)?;

            Ok(())
        })
        .on_window_event(|window, event| {
            // 关闭主窗口时隐藏到托盘，而非退出
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

更新 `Cargo.toml` 依赖，添加 tray 特性：

```toml
tauri = { version = "2", features = ["tray-icon"] }
```

- [ ] **Step 2: 生成应用图标**

```bash
cd /Users/xushixin/workspace/super-debug/desktop
# 用项目已有的 logo 生成各尺寸图标
cp ../superdev-logo-v5-launch.svg src-tauri/icons/icon.svg
pnpm tauri icon src-tauri/icons/icon.svg
# 生成后会在 src-tauri/icons/ 产生各平台所需的 png/ico/icns 文件
```

- [ ] **Step 3: 开发模式完整验证**

确保 agent 已运行，然后：

```bash
cd /Users/xushixin/workspace/super-debug/desktop
# 注意：开发模式不启动 sidecar，需手动启动 agent
pnpm tauri dev
```

验证清单：
- [ ] 主窗口打开，侧边栏显示 agent 项目列表
- [ ] 拖拽服务到面板分栏正常
- [ ] 日志实时滚动，chip 过滤即时生效
- [ ] 书签录制开始/停止
- [ ] 底部栏同步录制
- [ ] 系统托盘图标可见，菜单"显示主窗口"/"退出"正常工作
- [ ] 关闭主窗口后应用不退出（缩到托盘）

- [ ] **Step 4: Release 构建验证**

```bash
cd /Users/xushixin/workspace/super-debug/desktop

# 先把 agent 二进制复制到 sidecar 目录
mkdir -p src-tauri/binaries
cp ../agent/superdev-agent src-tauri/binaries/superdev-agent-aarch64-apple-darwin
# (Linux: superdev-agent-x86_64-unknown-linux-gnu)
# (Windows: superdev-agent-x86_64-pc-windows-msvc.exe)

pnpm tauri build
```

预期：`src-tauri/target/release/bundle/` 下生成对应平台的安装包，无构建错误。

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src-tauri/
git commit -m "feat(desktop): 系统托盘、关闭隐藏到托盘、打包验证"
```

---

## 验收清单

所有 Task 完成后，逐项确认：

| 功能 | 验收标准 |
|---|---|
| 侧边栏 | 显示所有项目/服务，状态圆点实时更新，悬停操作按钮正常 |
| 添加项目 | 文件夹选择后项目出现在侧边栏 |
| 分栏布局 | 拖拽服务到四边分栏，中央替换，关闭面板正常 |
| 布局持久化 | 刷新/重启后恢复上次布局 |
| 日志实时流 | WS 连接正常，新日志自动滚动到底部 |
| Chip 过滤 | 回车添加 chip，即时生效，AND/OR 切换正常 |
| 项目规则 | LogRule 开关即时生效，修改持久化到 agent |
| 单面板书签 | 开始/停止/复制/导出正常 |
| 同步录制 | 底部栏多面板同步开始/停止，导出按服务分块 |
| 历史记录 | 历史菜单列出 runId，切换后显示 banner，返回实时正常 |
| 系统托盘 | 托盘图标显示，菜单正常，关闭主窗口隐藏到托盘 |
| Agent 状态 | 右下角绿/红圆点反映连接状态 |
