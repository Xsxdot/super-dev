# 远程日志监听 - 前端实现计划

> **配套文档**：
> - 设计：`docs/superpowers/specs/2026-05-21-remote-log-monitoring-design.md`
> - 后端：`docs/superpowers/plans/2026-05-21-remote-log-monitoring-backend-plan.md`
>
> 本计划覆盖 spec §6（前端展示）+ §10 步骤 5-8（前端各步），与后端计划并行编写但**执行需在后端 API 可用之后**。
>
> 测试栈：vitest + @vue/test-utils（沿用现有 `desktop/src/**/__tests__/`）。
> 组件风格：Composition API、无 UI 框架、复用现有 `var(--*)` CSS 变量。
> Pinia composition 风格 store。

---

## 文件结构概览

新建 / 修改的目录与文件：

```
desktop/src/
├── api/
│   └── agent.ts                          # 修改：新增 Host/LogSource/Tunnel/RemoteView 接口
├── stores/
│   ├── remote.ts                         # 新建：Pinia store, host/log-source/tunnel 状态
│   ├── remoteLog.ts                      # 新建：Pinia store, 多节点 WS 归并 + 历史拉取
│   └── __tests__/
│       ├── remote.test.ts
│       └── remoteLog.test.ts
├── components/
│   ├── Sidebar/
│   │   ├── SidebarView.vue               # 修改：追加"远程监听"块
│   │   ├── RemoteListenSection.vue       # 新建：远程监听标题 + 任务列表 + 齿轮按钮
│   │   ├── RemoteLogSourceRow.vue        # 新建：单个监听任务（含分组展开）
│   │   ├── LogSourceFormModal.vue        # 新建：监听任务新建/编辑表单
│   │   └── __tests__/
│   │       ├── RemoteListenSection.test.ts
│   │       └── LogSourceFormModal.test.ts
│   ├── Panel/
│   │   ├── LogPanel.vue                  # 修改：支持远程模式（log_source_id + group）
│   │   ├── RemoteHostChips.vue           # 新建：节点 chips 筛选条
│   │   ├── LogRow.vue                    # 修改：可选 host 前缀渲染
│   │   └── __tests__/
│   │       ├── RemoteHostChips.test.ts
│   │       └── LogPanel.remote.test.ts
│   ├── Search/
│   │   ├── SearchPage.vue                # 修改：支持远程搜索 props
│   │   └── __tests__/
│   │       └── SearchPage.remote.test.ts
│   └── Settings/
│       ├── HostManagerTab.vue            # 新建：设置页主机管理 tab
│       ├── HostFormModal.vue             # 新建：单 Host CRUD 表单
│       ├── SshConfigImportModal.vue      # 新建：从 ~/.ssh/config 导入对话框
│       └── __tests__/
│           ├── HostManagerTab.test.ts
│           └── SshConfigImportModal.test.ts
├── pages/
│   └── SettingsPage.vue                  # 修改：新增 hosts tab
├── lib/
│   ├── tagColor.ts                       # 新建：tag → CSS 颜色映射
│   └── __tests__/
│       └── tagColor.test.ts
└── router/
    └── index.ts                          # 修改：/settings?tab=hosts 支持 query
```

执行顺序遵循依赖关系：Task 1（API）→ Task 2（store）→ Task 3（设置页）→ Task 4（Sidebar）→ Task 5（WS store）→ Task 6（LogPanel）→ Task 7（SearchPage）→ Task 8（验证）。

---

## Task 1：扩展 `api/agent.ts`——新增远程接口类型与方法

**目标**：在现有 `api` 对象上扩展 `hosts / logSources / sshConfig / tunnels / remoteView / remoteSearch` 系列方法；新增类型定义。**不破坏现有调用**。

### 文件：`desktop/src/api/agent.ts`

在文件末尾（`api` 对象的最后一个方法之后）的 `}` 之前追加新方法，并在文件中部追加类型定义。

**新增类型（追加在 `FetchLogContextPageParams` 之后、`api = { ... }` 之前）**：

```typescript
// ===== 远程监听相关类型 =====

export interface Host {
  id: string
  name: string
  ssh_host: string
  ssh_port: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
  remote_agent_port: number
  tags: string[]
  created_at: string
  updated_at: string
}

export type LogSourceType = 'journalctl' | 'docker'

export interface LogSource {
  id: string
  name: string
  type: LogSourceType
  host_ids: string[]
  created_at: string
  updated_at: string
}

export interface SshConfigEntry {
  host: string
  hostname: string
  port: number
  user: string
  identity_file?: string
}

export type TunnelState = 'idle' | 'connecting' | 'open' | 'failed' | 'closed'

export interface TunnelStatus {
  host_id: string
  state: TunnelState
  local_port?: number
  error?: string
  last_active?: string
}

export interface RemoteLogEntry extends LogEntry {
  host_id: string
}

export interface RemoteViewGroup {
  group_key: string             // "all" | tag 名
  host_ids: string[]
}

export interface RemoteViewResponse {
  log_source: LogSource
  groups: RemoteViewGroup[]
  hosts: Host[]                 // 该 LogSource 关联的所有 Host
}

export interface RemoteSearchParams {
  log_source_id: string
  group: string                 // "all" | tag 名
  query: string
  limit?: number
  cursor?: string
  from?: string
  to?: string
}

export interface RemoteSearchResponse {
  entries: RemoteLogEntry[]
  total_by_host: Record<string, number>
  hosts_failed: string[]
  next_cursor: string
  has_more: boolean
}

export interface HostCreatePayload {
  name: string
  ssh_host: string
  ssh_port?: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
  remote_agent_port?: number
  tags?: string[]
}

export type HostUpdatePayload = Partial<HostCreatePayload>

export interface LogSourceCreatePayload {
  name: string
  type: LogSourceType
  host_ids: string[]
}

export type LogSourceUpdatePayload = Partial<LogSourceCreatePayload>
```

**新增方法（在 `api = { ... }` 内追加，紧跟现有方法）**：

```typescript
  // ===== 远程监听 =====
  // Host CRUD
  listHosts: () => request<Host[]>('/api/hosts'),
  createHost: (payload: HostCreatePayload) =>
    request<Host>('/api/hosts', { method: 'POST', body: JSON.stringify(payload) }),
  updateHost: (id: string, payload: HostUpdatePayload) =>
    request<Host>(`/api/hosts/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteHost: (id: string) =>
    request<void>(`/api/hosts/${id}`, { method: 'DELETE' }),

  // SSH config 导入
  listSshConfigHosts: () => request<SshConfigEntry[]>('/api/ssh-config/hosts'),

  // LogSource CRUD
  listLogSources: () => request<LogSource[]>('/api/log-sources'),
  createLogSource: (payload: LogSourceCreatePayload) =>
    request<LogSource>('/api/log-sources', { method: 'POST', body: JSON.stringify(payload) }),
  updateLogSource: (id: string, payload: LogSourceUpdatePayload) =>
    request<LogSource>(`/api/log-sources/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteLogSource: (id: string) =>
    request<void>(`/api/log-sources/${id}`, { method: 'DELETE' }),

  // 隧道
  listTunnels: () => request<TunnelStatus[]>('/api/tunnels'),
  openTunnel: (hostId: string) =>
    request<TunnelStatus>(`/api/tunnels/${hostId}`, { method: 'POST' }),
  closeTunnel: (hostId: string) =>
    request<void>(`/api/tunnels/${hostId}`, { method: 'DELETE' }),

  // 远程视图（解析 LogSource → 分组 → host 列表）
  getRemoteView: (logSourceId: string) =>
    request<RemoteViewResponse>(`/api/remote/view?log_source_id=${logSourceId}`),

  // 跨节点搜索
  remoteSearch: (params: RemoteSearchParams) => {
    const qs = new URLSearchParams()
    qs.set('log_source_id', params.log_source_id)
    qs.set('group', params.group)
    qs.set('query', params.query)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor) qs.set('cursor', params.cursor)
    if (params.from) qs.set('from', params.from)
    if (params.to) qs.set('to', params.to)
    return request<RemoteSearchResponse>(`/api/remote-log-search?${qs}`)
  },
```

### 验收

- `desktop/` 下 `npm run typecheck`（或 `vue-tsc --noEmit`）通过
- 现有 `api` 调用方无 TS 报错
- `RemoteLogEntry` 兼容 `LogEntry`（`extends LogEntry & { host_id: string }`）

无单独测试文件——API 层没有逻辑，类型即测试。后续 store / 组件 mock `api` 间接验证。

---

## Task 2：`stores/remote.ts`——Host / LogSource / Tunnel 状态

**目标**：Pinia store 集中管理远程域的内存状态；只暴露 actions 给组件，组件不直接调 `api`。

### 测试：`desktop/src/stores/__tests__/remote.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useRemoteStore } from '@/stores/remote'
import { api, type Host, type LogSource, type TunnelStatus } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      listHosts: vi.fn(),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
      listLogSources: vi.fn(),
      createLogSource: vi.fn(),
      updateLogSource: vi.fn(),
      deleteLogSource: vi.fn(),
      listTunnels: vi.fn(),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

function makeHost(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'host-01',
    ssh_host: '10.0.0.1',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    tags: ['prod'],
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

function makeLogSource(overrides: Partial<LogSource> = {}): LogSource {
  return {
    id: 'ls1',
    name: 'nova-api',
    type: 'journalctl',
    host_ids: ['h1'],
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('useRemoteStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  describe('hosts', () => {
    it('loadHosts 拉取并写入 state', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      expect(store.hosts).toHaveLength(1)
      expect(store.hosts[0].name).toBe('host-01')
    })

    it('createHost 成功后追加到 hosts', async () => {
      const created = makeHost({ id: 'h2', name: 'host-02' })
      mockedApi.createHost.mockResolvedValue(created)
      const store = useRemoteStore()
      await store.createHost({
        name: 'host-02',
        ssh_host: '10.0.0.2',
        ssh_user: 'root',
      })
      expect(store.hosts.some(h => h.id === 'h2')).toBe(true)
    })

    it('updateHost 替换对应 id', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      const updated = makeHost({ tags: ['prod', 'temp'] })
      mockedApi.updateHost.mockResolvedValue(updated)
      await store.updateHost('h1', { tags: ['prod', 'temp'] })
      expect(store.hosts[0].tags).toEqual(['prod', 'temp'])
    })

    it('deleteHost 从 hosts 移除', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      mockedApi.deleteHost.mockResolvedValue(undefined)
      await store.deleteHost('h1')
      expect(store.hosts).toHaveLength(0)
    })

    it('hostById getter 按 id 查找', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      expect(store.hostById('h1')?.name).toBe('host-01')
      expect(store.hostById('missing')).toBeUndefined()
    })
  })

  describe('log sources', () => {
    it('loadLogSources 拉取并写入', async () => {
      mockedApi.listLogSources.mockResolvedValue([makeLogSource()])
      const store = useRemoteStore()
      await store.loadLogSources()
      expect(store.logSources).toHaveLength(1)
    })

    it('groupsOf 按 host tag 并集分组', async () => {
      mockedApi.listHosts.mockResolvedValue([
        makeHost({ id: 'h1', tags: ['test'] }),
        makeHost({ id: 'h2', tags: ['prod'] }),
        makeHost({ id: 'h3', tags: ['prod', 'temp'] }),
        makeHost({ id: 'h4', tags: ['prod', 'temp'] }),
      ])
      mockedApi.listLogSources.mockResolvedValue([
        makeLogSource({ id: 'ls1', host_ids: ['h1', 'h2', 'h3', 'h4'] }),
      ])
      const store = useRemoteStore()
      await store.loadHosts()
      await store.loadLogSources()

      const groups = store.groupsOf('ls1')
      const map = Object.fromEntries(groups.map(g => [g.key, g.hostIds.sort()]))
      expect(map.all).toEqual(['h1', 'h2', 'h3', 'h4'])
      expect(map.test).toEqual(['h1'])
      expect(map.prod).toEqual(['h2', 'h3', 'h4'])
      expect(map.temp).toEqual(['h3', 'h4'])
    })

    it('groupsOf 不存在的 LogSource 返回空数组', () => {
      const store = useRemoteStore()
      expect(store.groupsOf('missing')).toEqual([])
    })
  })

  describe('tunnels', () => {
    it('loadTunnels 拉取并按 host_id 索引', async () => {
      const status: TunnelStatus = {
        host_id: 'h1',
        state: 'open',
        local_port: 57100,
      }
      mockedApi.listTunnels.mockResolvedValue([status])
      const store = useRemoteStore()
      await store.loadTunnels()
      expect(store.tunnelOf('h1')?.state).toBe('open')
      expect(store.tunnelOf('h1')?.local_port).toBe(57100)
    })

    it('applyTunnelUpdate 单条更新合并到 map', () => {
      const store = useRemoteStore()
      store.applyTunnelUpdate({ host_id: 'h1', state: 'connecting' })
      expect(store.tunnelOf('h1')?.state).toBe('connecting')
      store.applyTunnelUpdate({ host_id: 'h1', state: 'open', local_port: 57100 })
      expect(store.tunnelOf('h1')?.state).toBe('open')
      expect(store.tunnelOf('h1')?.local_port).toBe(57100)
    })
  })
})
```

### 实现：`desktop/src/stores/remote.ts`

```typescript
// remote store 集中管理远程域：Host / LogSource / Tunnel 的内存状态与同步。
//
// 职责：
//   - 拉取并缓存 Host / LogSource 列表
//   - 对 LogSource 按其关联 Host 的 tag 计算分组
//   - 缓存隧道状态（由 /ws/tunnels 推送或手动 refresh 更新）
//
// 边界：
//   - 不直接发起 WebSocket 连接（由 remoteLog store 负责）
//   - 不渲染任何 UI

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  api,
  type Host,
  type HostCreatePayload,
  type HostUpdatePayload,
  type LogSource,
  type LogSourceCreatePayload,
  type LogSourceUpdatePayload,
  type TunnelStatus,
} from '@/api/agent'

export interface Group {
  key: string         // "all" | tag 名
  hostIds: string[]
}

export const useRemoteStore = defineStore('remote', () => {
  const hosts = ref<Host[]>([])
  const logSources = ref<LogSource[]>([])
  const tunnels = ref<Map<string, TunnelStatus>>(new Map())

  // ===== Hosts =====
  async function loadHosts() {
    hosts.value = await api.listHosts()
  }

  async function createHost(payload: HostCreatePayload) {
    const created = await api.createHost(payload)
    hosts.value.push(created)
    return created
  }

  async function updateHost(id: string, payload: HostUpdatePayload) {
    const updated = await api.updateHost(id, payload)
    const idx = hosts.value.findIndex(h => h.id === id)
    if (idx >= 0) hosts.value[idx] = updated
    return updated
  }

  async function deleteHost(id: string) {
    await api.deleteHost(id)
    hosts.value = hosts.value.filter(h => h.id !== id)
  }

  function hostById(id: string): Host | undefined {
    return hosts.value.find(h => h.id === id)
  }

  // ===== Log Sources =====
  async function loadLogSources() {
    logSources.value = await api.listLogSources()
  }

  async function createLogSource(payload: LogSourceCreatePayload) {
    const created = await api.createLogSource(payload)
    logSources.value.push(created)
    return created
  }

  async function updateLogSource(id: string, payload: LogSourceUpdatePayload) {
    const updated = await api.updateLogSource(id, payload)
    const idx = logSources.value.findIndex(l => l.id === id)
    if (idx >= 0) logSources.value[idx] = updated
    return updated
  }

  async function deleteLogSource(id: string) {
    await api.deleteLogSource(id)
    logSources.value = logSources.value.filter(l => l.id !== id)
  }

  function logSourceById(id: string): LogSource | undefined {
    return logSources.value.find(l => l.id === id)
  }

  /**
   * 按 host tag 并集计算 LogSource 的分组。
   * 永远包含 "all" 分组（所有 host），其余按 tag 拆分。
   */
  function groupsOf(logSourceId: string): Group[] {
    const ls = logSourceById(logSourceId)
    if (!ls) return []
    const hostMap = new Map(hosts.value.map(h => [h.id, h]))
    const allHostIds = ls.host_ids.filter(id => hostMap.has(id))

    const byTag = new Map<string, string[]>()
    byTag.set('all', allHostIds)
    for (const hid of allHostIds) {
      const host = hostMap.get(hid)
      if (!host) continue
      for (const tag of host.tags) {
        if (!byTag.has(tag)) byTag.set(tag, [])
        byTag.get(tag)!.push(hid)
      }
    }
    return Array.from(byTag.entries()).map(([key, hostIds]) => ({ key, hostIds }))
  }

  // ===== Tunnels =====
  async function loadTunnels() {
    const list = await api.listTunnels()
    tunnels.value = new Map(list.map(t => [t.host_id, t]))
  }

  function applyTunnelUpdate(status: TunnelStatus) {
    const next = new Map(tunnels.value)
    const prev = next.get(status.host_id)
    next.set(status.host_id, { ...prev, ...status })
    tunnels.value = next
  }

  function tunnelOf(hostId: string): TunnelStatus | undefined {
    return tunnels.value.get(hostId)
  }

  // ===== Aggregations =====
  const tagsAcrossHosts = computed(() => {
    const set = new Set<string>()
    for (const h of hosts.value) for (const t of h.tags) set.add(t)
    return Array.from(set).sort()
  })

  return {
    hosts,
    logSources,
    tunnels,
    tagsAcrossHosts,
    loadHosts,
    createHost,
    updateHost,
    deleteHost,
    hostById,
    loadLogSources,
    createLogSource,
    updateLogSource,
    deleteLogSource,
    logSourceById,
    groupsOf,
    loadTunnels,
    applyTunnelUpdate,
    tunnelOf,
  }
})
```

### 验收

- `npm test -- remote.test` 全绿
- `npm run typecheck` 通过

---

## Task 3：设置页主机管理 tab

**目标**：在 `SettingsPage` 增加 `hosts` tab，包含 Host 列表、CRUD 表单、SSH config 导入对话框、密钥浏览。

### 3.1 `lib/tagColor.ts` + 测试

```typescript
// tagColor 提供 tag 到 CSS 颜色（var 或 hex）的稳定映射，供 chip 和 LogRow 着色。
//
// 边界：
//   - 不存储颜色，纯函数映射
//   - 内置 prod/test/temp 等常用 tag 的语义色；未知 tag 走 hash 调色板

const PRESET: Record<string, string> = {
  prod: '#d9534f',   // 红
  test: '#f0ad4e',   // 黄
  temp: '#ec843e',   // 橙
  dev: '#5bc0de',    // 蓝
  staging: '#9966cc' // 紫
}

const PALETTE = [
  '#4e79a7', '#f28e2b', '#e15759', '#76b7b2', '#59a14f',
  '#edc949', '#af7aa1', '#ff9da7', '#9c755f', '#bab0ab',
]

export function tagColor(tag: string): string {
  if (PRESET[tag]) return PRESET[tag]
  let hash = 0
  for (let i = 0; i < tag.length; i++) hash = (hash * 31 + tag.charCodeAt(i)) >>> 0
  return PALETTE[hash % PALETTE.length]
}
```

```typescript
// desktop/src/lib/__tests__/tagColor.test.ts
import { describe, it, expect } from 'vitest'
import { tagColor } from '@/lib/tagColor'

describe('tagColor', () => {
  it('预设 tag 走预设颜色', () => {
    expect(tagColor('prod')).toBe('#d9534f')
    expect(tagColor('test')).toBe('#f0ad4e')
  })

  it('同一 tag 总是返回同一颜色', () => {
    expect(tagColor('foo')).toBe(tagColor('foo'))
  })

  it('未知 tag 命中调色板（非空字符串）', () => {
    expect(tagColor('unknown-tag')).toMatch(/^#[0-9a-f]{6}$/i)
  })
})
```

### 3.2 `components/Settings/HostFormModal.vue`

单 Host 新增 / 编辑表单。表单字段顺序参照 spec §6.4。

```vue
<!--
HostFormModal：单 Host CRUD 表单。

职责：
  - 新建 / 编辑 Host 的所有字段
  - 提供 ssh_key_path 的"浏览..."按钮（调 Tauri 文件对话框）
  - 表单底部提示密码明文存储风险

边界：
  - 不直接调 api，通过 emit('submit', payload) 让父组件处理
  - 不负责 SSH config 导入（独立 modal）
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { open as openDialog } from '@tauri-apps/plugin-dialog'
import type { Host, HostCreatePayload } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: Host | null   // null = 新建；Host = 编辑
}>()

const emit = defineEmits<{
  (e: 'submit', payload: HostCreatePayload): void
  (e: 'cancel'): void
}>()

const form = ref<HostCreatePayload>(emptyForm())
const tagsText = ref('')

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: '',
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
      tagsText.value = initial.tags.join(',')
    } else {
      form.value = emptyForm()
      tagsText.value = ''
    }
  },
  { immediate: true },
)

async function browseKey() {
  const selected = await openDialog({
    multiple: false,
    title: '选择 SSH 私钥文件',
  })
  if (selected && !Array.isArray(selected)) {
    form.value.ssh_key_path = selected
  }
}

function submit() {
  const payload: HostCreatePayload = {
    ...form.value,
    tags: tagsText.value.split(',').map(t => t.trim()).filter(Boolean),
  }
  emit('submit', payload)
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑主机' : '新建主机' }}</div>
      <div class="hint">远端 agent 默认监听 <code>127.0.0.1:57017</code>，需要先在远端部署 agent 二进制</div>

      <div class="field">
        <label>name <span class="req">*</span></label>
        <input v-model="form.name" placeholder="例：nova-api-prod-01" data-test="host-form-name" />
      </div>

      <div class="row">
        <div class="field">
          <label>ssh_host <span class="req">*</span></label>
          <input v-model="form.ssh_host" placeholder="10.0.0.1" data-test="host-form-host" />
        </div>
        <div class="field small">
          <label>port</label>
          <input v-model.number="form.ssh_port" type="number" min="1" data-test="host-form-port" />
        </div>
      </div>

      <div class="field">
        <label>ssh_user <span class="req">*</span></label>
        <input v-model="form.ssh_user" placeholder="root" data-test="host-form-user" />
      </div>

      <div class="field">
        <label>ssh_password</label>
        <input v-model="form.ssh_password" type="password" placeholder="留空则用密钥" data-test="host-form-password" />
      </div>

      <div class="field">
        <label>ssh_key_path</label>
        <div class="row tight">
          <input v-model="form.ssh_key_path" placeholder="~/.ssh/id_ed25519" data-test="host-form-key" />
          <button type="button" @click="browseKey" data-test="host-form-browse">浏览...</button>
        </div>
      </div>

      <div class="field">
        <label>remote_agent_port</label>
        <input v-model.number="form.remote_agent_port" type="number" min="1" data-test="host-form-agent-port" />
      </div>

      <div class="field">
        <label>tags（逗号分隔）</label>
        <input v-model="tagsText" placeholder="prod,temp" data-test="host-form-tags" />
      </div>

      <div class="warn">⚠ 密码以明文存储在 <code>~/.superdev/hosts.json</code>（权限 0600）</div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button type="button" class="primary" @click="submit" data-test="host-form-submit">保存</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed; inset: 0; background: rgba(0,0,0,0.45);
  display: flex; align-items: center; justify-content: center; z-index: 100;
}
.modal-body {
  background: var(--bg-primary); border: 1px solid var(--border-secondary);
  padding: 16px 18px; width: 480px; max-height: 86vh; overflow-y: auto;
}
.modal-title { font-size: 14px; font-weight: 600; margin-bottom: 8px; }
.hint { font-size: 11px; color: var(--text-tertiary); margin-bottom: 12px; }
.field { margin-bottom: 10px; display: flex; flex-direction: column; }
.field label { font-size: 11px; color: var(--text-secondary); margin-bottom: 4px; }
.field .req { color: #d9534f; }
.field input { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 8px; font-size: 12px; }
.row { display: flex; gap: 8px; }
.row .field.small { width: 80px; }
.row.tight { display: flex; gap: 4px; align-items: stretch; }
.warn { font-size: 11px; color: #d9534f; margin: 12px 0; }
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions button { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 12px; font-size: 12px; cursor: pointer; }
.actions button.primary { background: var(--accent, #4e79a7); color: #fff; border-color: transparent; }
</style>
```

### 3.3 `components/Settings/SshConfigImportModal.vue`

```vue
<!--
SshConfigImportModal：列出 ~/.ssh/config 中的 Host 条目供多选导入。

职责：
  - 调 GET /api/ssh-config/hosts
  - 多选后 emit('import', entries[])，由父组件批量创建 Host

边界：
  - 仅作为创建入口，不与本地 SSH config 保持同步
  - 不补全 tags / remote_agent_port，由用户进入表单逐个补
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type SshConfigEntry } from '@/api/agent'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{
  (e: 'import', entries: SshConfigEntry[]): void
  (e: 'cancel'): void
}>()

const loading = ref(false)
const error = ref<string | null>(null)
const entries = ref<SshConfigEntry[]>([])
const selected = ref<Set<string>>(new Set())

watch(
  () => props.visible,
  async (visible) => {
    if (!visible) return
    loading.value = true
    error.value = null
    selected.value = new Set()
    try {
      entries.value = await api.listSshConfigHosts()
    } catch (e) {
      error.value = e instanceof Error ? e.message : '读取 SSH config 失败'
      entries.value = []
    } finally {
      loading.value = false
    }
  },
)

function toggle(host: string) {
  const next = new Set(selected.value)
  if (next.has(host)) next.delete(host)
  else next.add(host)
  selected.value = next
}

function confirm() {
  const chosen = entries.value.filter(e => selected.value.has(e.host))
  emit('import', chosen)
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">从 SSH config 导入</div>
      <div v-if="loading" class="state">读取中...</div>
      <div v-else-if="error" class="state err">{{ error }}</div>
      <div v-else-if="entries.length === 0" class="state">~/.ssh/config 中没有可导入的 Host</div>
      <ul v-else class="entry-list">
        <li
          v-for="entry in entries"
          :key="entry.host"
          :class="{ selected: selected.has(entry.host) }"
          @click="toggle(entry.host)"
          data-test="ssh-import-row"
        >
          <input type="checkbox" :checked="selected.has(entry.host)" @click.stop="toggle(entry.host)" />
          <span class="name">{{ entry.host }}</span>
          <span class="meta">{{ entry.user }}@{{ entry.hostname }}:{{ entry.port }}</span>
          <span v-if="entry.identity_file" class="meta key">🔑 {{ entry.identity_file }}</span>
        </li>
      </ul>
      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="selected.size === 0"
          @click="confirm"
          data-test="ssh-import-confirm"
        >导入 {{ selected.size }} 项</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 100; }
.modal-body { background: var(--bg-primary); border: 1px solid var(--border-secondary); padding: 16px 18px; width: 520px; max-height: 80vh; overflow-y: auto; }
.modal-title { font-size: 14px; font-weight: 600; margin-bottom: 10px; }
.state { padding: 16px; color: var(--text-tertiary); font-size: 12px; text-align: center; }
.state.err { color: #d9534f; }
.entry-list { list-style: none; padding: 0; margin: 0 0 12px; max-height: 360px; overflow-y: auto; }
.entry-list li { display: flex; align-items: center; gap: 8px; padding: 6px 8px; cursor: pointer; border-bottom: 1px solid var(--border-secondary); }
.entry-list li.selected { background: var(--bg-secondary); }
.entry-list .name { font-weight: 600; font-size: 12px; }
.entry-list .meta { font-size: 11px; color: var(--text-tertiary); }
.entry-list .meta.key { margin-left: 4px; }
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions button { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 12px; font-size: 12px; cursor: pointer; }
.actions button.primary { background: var(--accent, #4e79a7); color: #fff; border-color: transparent; }
.actions button[disabled] { opacity: 0.5; cursor: not-allowed; }
</style>
```

### 3.4 `components/Settings/HostManagerTab.vue`

```vue
<!--
HostManagerTab：设置页主机管理标签页。

职责：
  - 列出所有 Host（name / ssh / tags / 隧道状态）
  - 提供新建 / 编辑 / 删除入口
  - 提供"从 SSH config 导入"快捷入口

边界：
  - 不处理 LogSource（在 Sidebar 内联）
  - 不直接渲染日志面板
-->
<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import HostFormModal from './HostFormModal.vue'
import SshConfigImportModal from './SshConfigImportModal.vue'
import type { Host, SshConfigEntry, HostCreatePayload } from '@/api/agent'

const store = useRemoteStore()
const formVisible = ref(false)
const importVisible = ref(false)
const editing = ref<Host | null>(null)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    await Promise.all([store.loadHosts(), store.loadTunnels()])
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  }
})

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function openEdit(host: Host) {
  editing.value = host
  formVisible.value = true
}

async function handleSubmit(payload: HostCreatePayload) {
  try {
    if (editing.value) {
      await store.updateHost(editing.value.id, payload)
    } else {
      await store.createHost(payload)
    }
    formVisible.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  }
}

async function handleDelete(host: Host) {
  if (!confirm(`确认删除主机 "${host.name}"？`)) return
  try {
    await store.deleteHost(host.id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除失败'
  }
}

async function handleImport(entries: SshConfigEntry[]) {
  importVisible.value = false
  for (const entry of entries) {
    try {
      await store.createHost({
        name: entry.host,
        ssh_host: entry.hostname,
        ssh_port: entry.port,
        ssh_user: entry.user,
        ssh_key_path: entry.identity_file,
        remote_agent_port: 57017,
        tags: [],
      })
    } catch (e) {
      error.value = e instanceof Error ? e.message : `导入 ${entry.host} 失败`
      break
    }
  }
}

function tunnelLabel(hostId: string): string {
  const t = store.tunnelOf(hostId)
  if (!t) return '—'
  if (t.state === 'open' && t.local_port) return `open :${t.local_port}`
  return t.state
}

const sortedHosts = computed(() => [...store.hosts].sort((a, b) => a.name.localeCompare(b.name)))
</script>

<template>
  <section class="host-manager">
    <div class="toolbar">
      <button class="primary" @click="openCreate" data-test="host-add">+ 新建主机</button>
      <button @click="importVisible = true" data-test="host-import">从 SSH config 导入</button>
    </div>
    <div v-if="error" class="error">{{ error }}</div>
    <table v-if="sortedHosts.length > 0" class="host-table">
      <thead>
        <tr><th>name</th><th>ssh</th><th>tags</th><th>隧道</th><th></th></tr>
      </thead>
      <tbody>
        <tr v-for="host in sortedHosts" :key="host.id" data-test="host-row">
          <td>{{ host.name }}</td>
          <td class="mono">{{ host.ssh_user }}@{{ host.ssh_host }}:{{ host.ssh_port }}</td>
          <td>
            <span
              v-for="tag in host.tags"
              :key="tag"
              class="tag-chip"
              :style="{ background: tagColor(tag) }"
            >{{ tag }}</span>
          </td>
          <td class="mono">{{ tunnelLabel(host.id) }}</td>
          <td>
            <button @click="openEdit(host)">编辑</button>
            <button class="danger" @click="handleDelete(host)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">还没有主机，点击"新建主机"或"从 SSH config 导入"开始。</div>

    <HostFormModal
      :visible="formVisible"
      :initial="editing"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />
    <SshConfigImportModal
      :visible="importVisible"
      @import="handleImport"
      @cancel="importVisible = false"
    />
  </section>
</template>

<style scoped>
.host-manager { padding: 12px; }
.toolbar { display: flex; gap: 8px; margin-bottom: 12px; }
.toolbar button { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 12px; font-size: 12px; cursor: pointer; }
.toolbar button.primary { background: var(--accent, #4e79a7); color: #fff; border-color: transparent; }
.error { background: rgba(217,83,79,0.12); border: 1px solid #d9534f; color: #d9534f; padding: 6px 10px; font-size: 11px; margin-bottom: 8px; }
.host-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.host-table th, .host-table td { padding: 6px 8px; border-bottom: 1px solid var(--border-secondary); text-align: left; }
.host-table th { color: var(--text-tertiary); font-weight: normal; font-size: 11px; }
.mono { font-family: var(--font-mono, monospace); }
.tag-chip { color: #fff; padding: 1px 6px; border-radius: 2px; font-size: 10px; margin-right: 4px; }
.host-table button { background: transparent; border: none; color: var(--accent, #4e79a7); font-size: 11px; cursor: pointer; padding: 0 4px; }
.host-table button.danger { color: #d9534f; }
.empty { padding: 32px; color: var(--text-tertiary); text-align: center; font-size: 12px; }
</style>
```

### 3.5 修改 `pages/SettingsPage.vue`

在 `SettingsTab` 联合类型新增 `'hosts'`，sidebar 加入 hosts 按钮，主区域条件渲染 `HostManagerTab`，并支持 `?tab=hosts` query 直达。

修改片段（在 `selectedTab` 定义和模板对应位置追加）：

```typescript
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { useRoute } from 'vue-router'

type SettingsTab = 'general' | 'projects' | 'hosts'

const route = useRoute()
const selectedTab = ref<SettingsTab>(
  (route.query.tab as SettingsTab) === 'hosts' ? 'hosts' : 'general'
)
```

模板加 tab 按钮：

```vue
<button
  data-test="settings-tab-hosts"
  class="tab-btn"
  :class="{ active: selectedTab === 'hosts' }"
  @click="selectedTab = 'hosts'"
>
  <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
    <rect x="2" y="3" width="12" height="3" stroke="currentColor" stroke-width="1.4" fill="none"/>
    <rect x="2" y="10" width="12" height="3" stroke="currentColor" stroke-width="1.4" fill="none"/>
    <circle cx="4" cy="4.5" r="0.6" fill="currentColor"/>
    <circle cx="4" cy="11.5" r="0.6" fill="currentColor"/>
  </svg>
  主机管理
</button>
```

主区域：

```vue
<HostManagerTab v-if="selectedTab === 'hosts'" />
```

### 3.6 测试：`components/Settings/__tests__/HostManagerTab.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { useRemoteStore } from '@/stores/remote'

vi.mock('@/api/agent', () => ({
  api: {
    listHosts: vi.fn().mockResolvedValue([]),
    listTunnels: vi.fn().mockResolvedValue([]),
    createHost: vi.fn(),
    updateHost: vi.fn(),
    deleteHost: vi.fn(),
    listSshConfigHosts: vi.fn().mockResolvedValue([]),
  },
}))

describe('HostManagerTab', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('空态展示提示文案', async () => {
    const wrapper = mount(HostManagerTab)
    await new Promise(r => setTimeout(r))
    expect(wrapper.text()).toContain('还没有主机')
  })

  it('点击"新建主机"打开表单', async () => {
    const wrapper = mount(HostManagerTab)
    await wrapper.find('[data-test="host-add"]').trigger('click')
    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
  })

  it('点击"从 SSH config 导入"打开导入对话框', async () => {
    const wrapper = mount(HostManagerTab)
    await wrapper.find('[data-test="host-import"]').trigger('click')
    expect(wrapper.text()).toContain('从 SSH config 导入')
  })

  it('提交表单调用 store.createHost', async () => {
    const wrapper = mount(HostManagerTab)
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue({
      id: 'h1', name: 'x', ssh_host: '1.1.1.1', ssh_port: 22, ssh_user: 'r',
      remote_agent_port: 57017, tags: [], created_at: '', updated_at: '',
    })
    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-host"]').setValue('1.1.1.1')
    await wrapper.find('[data-test="host-form-user"]').setValue('root')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    expect(spy).toHaveBeenCalled()
    expect(spy.mock.calls[0][0]).toMatchObject({ name: 'host-test', ssh_host: '1.1.1.1', ssh_user: 'root' })
  })
})
```

### 3.7 测试：`components/Settings/__tests__/SshConfigImportModal.test.ts`

```typescript
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SshConfigImportModal from '@/components/Settings/SshConfigImportModal.vue'

vi.mock('@/api/agent', () => ({
  api: {
    listSshConfigHosts: vi.fn().mockResolvedValue([
      { host: 'prod-01', hostname: '10.0.0.1', port: 22, user: 'deploy', identity_file: '~/.ssh/id_ed25519' },
      { host: 'prod-02', hostname: '10.0.0.2', port: 22, user: 'deploy' },
    ]),
  },
}))

describe('SshConfigImportModal', () => {
  it('显示 SSH config 条目列表', async () => {
    const wrapper = mount(SshConfigImportModal, { props: { visible: true } })
    await new Promise(r => setTimeout(r))
    await nextTick()
    const rows = wrapper.findAll('[data-test="ssh-import-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('prod-01')
  })

  it('多选后 emit("import") 携带选中条目', async () => {
    const wrapper = mount(SshConfigImportModal, { props: { visible: true } })
    await new Promise(r => setTimeout(r))
    await nextTick()
    await wrapper.findAll('[data-test="ssh-import-row"]')[0].trigger('click')
    await wrapper.findAll('[data-test="ssh-import-row"]')[1].trigger('click')
    await wrapper.find('[data-test="ssh-import-confirm"]').trigger('click')
    const emitted = wrapper.emitted('import')!
    expect(emitted).toHaveLength(1)
    expect((emitted[0][0] as unknown[])).toHaveLength(2)
  })
})
```

### 验收

- `npm test -- HostManagerTab SshConfigImportModal tagColor` 全绿
- 手测：进 `/settings?tab=hosts`、能创建 / 编辑 / 删除 / 导入 / 浏览密钥

---

## Task 4：Sidebar 远程监听块 + 监听任务表单

**目标**：`SidebarView` 在本地项目下方增加"远程监听"块；标题栏齿轮按钮跳到 `/settings?tab=hosts`；列出所有 LogSource 和其分组；点击分组打开 Panel；提供新建 / 编辑 LogSource 的 inline 表单。

### 4.1 `components/Sidebar/RemoteLogSourceRow.vue`

```vue
<!--
RemoteLogSourceRow：单个监听任务（LogSource）及其分组列表。

职责：
  - 展示 LogSource 名称
  - 展开后按分组（all / tag 名）列出，每个分组显示节点数
  - 点击分组 → emit('open', { logSourceId, groupKey })
  - 编辑入口 → emit('edit', logSource)

边界：
  - 不直接发起 HTTP 调用
  - 不渲染日志面板
-->
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import type { LogSource } from '@/api/agent'

const props = defineProps<{ logSource: LogSource }>()
const emit = defineEmits<{
  (e: 'open', payload: { logSourceId: string; groupKey: string }): void
  (e: 'edit', logSource: LogSource): void
  (e: 'delete', logSource: LogSource): void
}>()

const store = useRemoteStore()
const expanded = ref(true)

const groups = computed(() => store.groupsOf(props.logSource.id))

function chipColor(key: string): string | undefined {
  if (key === 'all') return undefined
  return tagColor(key)
}
</script>

<template>
  <div class="log-source">
    <div class="header" @click="expanded = !expanded" data-test="logsource-header">
      <span class="caret">{{ expanded ? '▾' : '▸' }}</span>
      <span class="name">{{ logSource.name }}</span>
      <span class="type">[{{ logSource.type }}]</span>
      <span class="actions">
        <button class="icon" @click.stop="emit('edit', logSource)" data-test="logsource-edit">✎</button>
        <button class="icon" @click.stop="emit('delete', logSource)" data-test="logsource-delete">✕</button>
      </span>
    </div>
    <div v-if="expanded" class="groups">
      <div
        v-for="group in groups"
        :key="group.key"
        class="group-row"
        @click="emit('open', { logSourceId: logSource.id, groupKey: group.key })"
        data-test="logsource-group"
      >
        <span class="chip" :style="chipColor(group.key) ? { background: chipColor(group.key) } : undefined">{{ group.key }}</span>
        <span class="count">({{ group.hostIds.length }} 节点)</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-source { margin: 2px 0; }
.header { display: flex; align-items: center; gap: 4px; padding: 4px 8px; cursor: pointer; font-size: 12px; }
.header:hover { background: var(--bg-secondary); }
.header .caret { width: 10px; color: var(--text-tertiary); }
.header .name { font-weight: 600; flex: 1; }
.header .type { font-size: 10px; color: var(--text-tertiary); }
.header .actions { display: none; gap: 2px; }
.header:hover .actions { display: inline-flex; }
.header button.icon { background: transparent; border: none; color: var(--text-tertiary); cursor: pointer; padding: 0 2px; font-size: 11px; }
.groups { padding-left: 18px; }
.group-row { display: flex; gap: 6px; align-items: center; padding: 3px 8px; cursor: pointer; font-size: 11px; }
.group-row:hover { background: var(--bg-secondary); }
.chip { padding: 1px 6px; border-radius: 2px; font-size: 10px; background: var(--bg-secondary); color: #fff; }
.chip:not([style]) { background: var(--bg-secondary); color: var(--text-primary); }
.count { color: var(--text-tertiary); }
</style>
```

### 4.2 `components/Sidebar/LogSourceFormModal.vue`

```vue
<!--
LogSourceFormModal：监听任务（LogSource）新建/编辑表单。

字段：name / type 下拉 / host_ids 多选。

边界：
  - 不调 api，通过 emit('submit', payload)
-->
<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import type { LogSource, LogSourceCreatePayload, LogSourceType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: LogSource | null
}>()

const emit = defineEmits<{
  (e: 'submit', payload: LogSourceCreatePayload): void
  (e: 'cancel'): void
}>()

const store = useRemoteStore()

const name = ref('')
const type = ref<LogSourceType>('journalctl')
const hostIds = ref<Set<string>>(new Set())

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    if (initial) {
      name.value = initial.name
      type.value = initial.type
      hostIds.value = new Set(initial.host_ids)
    } else {
      name.value = ''
      type.value = 'journalctl'
      hostIds.value = new Set()
    }
  },
  { immediate: true },
)

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
  })
}

const canSubmit = computed(() => name.value.trim().length > 0 && hostIds.value.size > 0)
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑监听任务' : '新建监听任务' }}</div>

      <div class="field">
        <label>name <span class="req">*</span></label>
        <input v-model="name" placeholder="例：nova-api（与远端服务名一致）" data-test="logsource-form-name" />
      </div>

      <div class="field">
        <label>type</label>
        <select v-model="type" data-test="logsource-form-type">
          <option value="journalctl">journalctl</option>
          <option value="docker">docker</option>
        </select>
      </div>

      <div class="field">
        <label>关联主机 <span class="req">*</span></label>
        <div class="host-list" v-if="store.hosts.length > 0">
          <label v-for="host in store.hosts" :key="host.id" class="host-row" data-test="logsource-form-host">
            <input
              type="checkbox"
              :checked="hostIds.has(host.id)"
              @change="toggleHost(host.id)"
            />
            <span class="hname">{{ host.name }}</span>
            <span class="tags">{{ host.tags.join(', ') || '(无标签)' }}</span>
          </label>
        </div>
        <div v-else class="empty">还没有主机。请先到设置页 → 主机管理添加。</div>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="!canSubmit"
          @click="submit"
          data-test="logsource-form-submit"
        >保存</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 100; }
.modal-body { background: var(--bg-primary); border: 1px solid var(--border-secondary); padding: 16px 18px; width: 440px; max-height: 80vh; overflow-y: auto; }
.modal-title { font-size: 14px; font-weight: 600; margin-bottom: 10px; }
.field { margin-bottom: 12px; display: flex; flex-direction: column; }
.field label { font-size: 11px; color: var(--text-secondary); margin-bottom: 4px; }
.field .req { color: #d9534f; }
.field input, .field select { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 8px; font-size: 12px; }
.host-list { max-height: 240px; overflow-y: auto; border: 1px solid var(--border-secondary); }
.host-row { display: flex; gap: 8px; align-items: center; padding: 4px 8px; font-size: 12px; cursor: pointer; }
.host-row:hover { background: var(--bg-secondary); }
.host-row .hname { font-weight: 600; }
.host-row .tags { font-size: 10px; color: var(--text-tertiary); }
.empty { padding: 12px; color: var(--text-tertiary); font-size: 11px; text-align: center; }
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions button { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 4px 12px; font-size: 12px; cursor: pointer; }
.actions button.primary { background: var(--accent, #4e79a7); color: #fff; border-color: transparent; }
.actions button[disabled] { opacity: 0.5; cursor: not-allowed; }
</style>
```

### 4.3 `components/Sidebar/RemoteListenSection.vue`

```vue
<!--
RemoteListenSection：Sidebar 中的"远程监听"块。

职责：
  - 标题栏 + 齿轮按钮（跳 /settings?tab=hosts）
  - 列出所有 LogSource，调用 RemoteLogSourceRow 渲染
  - 提供"+ 新建监听任务"入口
  - 协调 LogSourceFormModal 的开关与提交

边界：
  - 不解析分组（委托给 store.groupsOf）
  - 不打开日志面板（emit('open') 由 SidebarView 调 workspace）
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useRemoteStore } from '@/stores/remote'
import RemoteLogSourceRow from './RemoteLogSourceRow.vue'
import LogSourceFormModal from './LogSourceFormModal.vue'
import type { LogSource, LogSourceCreatePayload } from '@/api/agent'

const emit = defineEmits<{
  (e: 'open', payload: { logSourceId: string; groupKey: string }): void
}>()

const router = useRouter()
const store = useRemoteStore()

const formVisible = ref(false)
const editing = ref<LogSource | null>(null)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    await Promise.all([store.loadHosts(), store.loadLogSources(), store.loadTunnels()])
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  }
})

function openSettings() {
  router.push({ path: '/settings', query: { tab: 'hosts' } })
}

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function handleEdit(logSource: LogSource) {
  editing.value = logSource
  formVisible.value = true
}

async function handleDelete(logSource: LogSource) {
  if (!confirm(`确认删除监听任务 "${logSource.name}"？`)) return
  try {
    await store.deleteLogSource(logSource.id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除失败'
  }
}

async function handleSubmit(payload: LogSourceCreatePayload) {
  try {
    if (editing.value) {
      await store.updateLogSource(editing.value.id, payload)
    } else {
      await store.createLogSource(payload)
    }
    formVisible.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  }
}
</script>

<template>
  <div class="remote-section">
    <div class="section-header">
      <span class="title">远程监听</span>
      <button class="gear" @click="openSettings" title="主机管理" data-test="remote-gear">⚙</button>
    </div>
    <div v-if="error" class="error">{{ error }}</div>
    <RemoteLogSourceRow
      v-for="ls in store.logSources"
      :key="ls.id"
      :log-source="ls"
      @open="payload => emit('open', payload)"
      @edit="handleEdit"
      @delete="handleDelete"
    />
    <div v-if="store.logSources.length === 0 && !error" class="empty">还没有监听任务</div>
    <div class="add-row" @click="openCreate" data-test="remote-add-logsource">+ 新建监听任务</div>

    <LogSourceFormModal
      :visible="formVisible"
      :initial="editing"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />
  </div>
</template>

<style scoped>
.remote-section { border-top: 1px solid var(--border-secondary); padding: 6px 0; }
.section-header { display: flex; align-items: center; padding: 4px 12px; }
.section-header .title { font-size: 11px; color: var(--text-tertiary); flex: 1; text-transform: uppercase; letter-spacing: 0.5px; }
.section-header .gear { background: transparent; border: none; color: var(--text-tertiary); cursor: pointer; font-size: 13px; padding: 0 4px; }
.section-header .gear:hover { color: var(--text-secondary); }
.error { font-size: 11px; color: #d9534f; padding: 4px 12px; }
.empty { padding: 8px 12px; font-size: 11px; color: var(--text-tertiary); }
.add-row { padding: 4px 12px; font-size: 11px; color: var(--text-tertiary); cursor: pointer; }
.add-row:hover { color: var(--text-secondary); }
</style>
```

### 4.4 修改 `components/Sidebar/SidebarView.vue`

新增 RemoteListenSection 引用 + workspace.openRemote 调用。

```typescript
// 在 import 区追加：
import RemoteListenSection from './RemoteListenSection.vue'

// 在 selectService 同级新增：
function openRemoteGroup(payload: { logSourceId: string; groupKey: string }) {
  workspace.openRemote(payload.logSourceId, payload.groupKey)
}
```

模板：在本地项目循环 + `</template>` 之间插入：

```vue
<RemoteListenSection @open="openRemoteGroup" />
```

注意：`workspace.openRemote` 需要在 `stores/workspace.ts` 新增。详见 Task 6 的 stores/workspace.ts 改动一节。

### 4.5 测试：`components/Sidebar/__tests__/RemoteListenSection.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import RemoteListenSection from '@/components/Sidebar/RemoteListenSection.vue'
import { useRemoteStore } from '@/stores/remote'

vi.mock('@/api/agent', () => ({
  api: {
    listHosts: vi.fn().mockResolvedValue([]),
    listLogSources: vi.fn().mockResolvedValue([]),
    listTunnels: vi.fn().mockResolvedValue([]),
    createLogSource: vi.fn(),
    deleteLogSource: vi.fn(),
  },
}))

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/settings', component: { template: '<div />' } },
    ],
  })
}

describe('RemoteListenSection', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('齿轮按钮跳到 /settings?tab=hosts', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await router.isReady()
    await wrapper.find('[data-test="remote-gear"]').trigger('click')
    await new Promise(r => setTimeout(r))
    expect(router.currentRoute.value.path).toBe('/settings')
    expect(router.currentRoute.value.query.tab).toBe('hosts')
  })

  it('空态展示提示', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await new Promise(r => setTimeout(r))
    expect(wrapper.text()).toContain('还没有监听任务')
  })

  it('点击"新建监听任务"打开表单', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="remote-add-logsource"]').trigger('click')
    expect(wrapper.find('[data-test="logsource-form-name"]').exists()).toBe(true)
  })
})
```

### 4.6 测试：`components/Sidebar/__tests__/LogSourceFormModal.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import LogSourceFormModal from '@/components/Sidebar/LogSourceFormModal.vue'
import { useRemoteStore } from '@/stores/remote'

vi.mock('@/api/agent', () => ({ api: {} }))

describe('LogSourceFormModal', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('未填名称或主机时提交按钮禁用', async () => {
    const wrapper = mount(LogSourceFormModal, { props: { visible: true } })
    const btn = wrapper.find('[data-test="logsource-form-submit"]')
    expect(btn.attributes('disabled')).toBeDefined()
  })

  it('填好后 submit 携带正确 payload', async () => {
    const store = useRemoteStore()
    store.hosts = [
      { id: 'h1', name: 'host-01', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 0, tags: [], created_at: '', updated_at: '' },
    ]
    const wrapper = mount(LogSourceFormModal, { props: { visible: true } })
    await wrapper.find('[data-test="logsource-form-name"]').setValue('nova-api')
    await wrapper.findAll('[data-test="logsource-form-host"] input[type=checkbox]')[0].setValue(true)
    await wrapper.find('[data-test="logsource-form-submit"]').trigger('click')
    const payload = wrapper.emitted('submit')![0][0]
    expect(payload).toMatchObject({ name: 'nova-api', type: 'journalctl', host_ids: ['h1'] })
  })
})
```

### 验收

- `npm test -- RemoteListenSection LogSourceFormModal` 全绿
- 手测：Sidebar 显示远程监听块、齿轮跳设置、新建监听任务

---

## Task 5：`stores/remoteLog.ts` + workspace 远程 tab——多节点 WS 归并

**目标**：新建 store 管理"为某 `(logSourceId, groupKey)` 订阅的多节点实时 + 历史日志"；为每个 Host 建一个 WS、把消息按时间戳归并到一个有序列表；提供 `subscribe / unsubscribe / loadHistory`。同时为 `workspace` store 加 `openRemote(logSourceId, groupKey)`。

### 5.1 修改 `stores/workspace.ts`

阅读现有 `workspace.ts` 后追加 `RemoteTab` 类型与 `openRemote` 方法。**因当前未读到 workspace.ts 全文，本节给出关键 diff，执行者需依据现有结构对齐**：

```typescript
// 类型扩展
export type WorkspaceTab =
  | { id: string; type: 'project'; projectId: string }
  | { id: string; type: 'search'; projectId: string }
  | { id: string; type: 'remote'; logSourceId: string; groupKey: string }     // 新增
  | { id: string; type: 'remote-search'; logSourceId: string; groupKey: string } // 新增

// actions 追加
function openRemote(logSourceId: string, groupKey: string) {
  const id = `remote:${logSourceId}:${groupKey}`
  const existing = tabs.value.find(t => t.id === id)
  if (existing) { activeTabId.value = id; return }
  tabs.value.push({ id, type: 'remote', logSourceId, groupKey })
  activeTabId.value = id
}

function openRemoteSearch(logSourceId: string, groupKey: string) {
  const id = `remote-search:${logSourceId}:${groupKey}`
  const existing = tabs.value.find(t => t.id === id)
  if (existing) { activeTabId.value = id; return }
  tabs.value.push({ id, type: 'remote-search', logSourceId, groupKey })
  activeTabId.value = id
}
```

并在 `return { ... }` 中导出 `openRemote / openRemoteSearch`。

### 5.2 测试：`stores/__tests__/remoteLog.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      openTunnel: vi.fn().mockResolvedValue({ host_id: 'h1', state: 'open', local_port: 57100 }),
      closeTunnel: vi.fn().mockResolvedValue(undefined),
      getRemoteView: vi.fn().mockResolvedValue({
        log_source: { id: 'ls1', name: 'nova-api', type: 'journalctl', host_ids: ['h1', 'h2'], created_at: '', updated_at: '' },
        groups: [
          { group_key: 'all', host_ids: ['h1', 'h2'] },
          { group_key: 'prod', host_ids: ['h1', 'h2'] },
        ],
        hosts: [
          { id: 'h1', name: 'host-01', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, tags: ['prod'], created_at: '', updated_at: '' },
          { id: 'h2', name: 'host-02', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, tags: ['prod'], created_at: '', updated_at: '' },
        ],
      }),
    },
    WS_BASE: 'ws://127.0.0.1:57018',
  }
})

class MockWebSocket {
  static instances: MockWebSocket[] = []
  static OPEN = 1
  url: string
  readyState = 0
  onopen: ((e?: unknown) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e?: unknown) => void) | null = null
  onclose: ((e?: unknown) => void) | null = null
  closed = false
  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
    setTimeout(() => {
      this.readyState = 1
      this.onopen?.()
    }, 0)
  }
  send() {}
  close() { this.closed = true; this.readyState = 3; this.onclose?.() }
  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  MockWebSocket.instances = []
  ;(globalThis as unknown as { WebSocket: typeof MockWebSocket }).WebSocket = MockWebSocket
  vi.clearAllMocks()
})

describe('useRemoteLogStore', () => {
  it('subscribe 为 group 内每个 host 建立隧道与 WS', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    expect((api.openTunnel as Mock).mock.calls.map(c => c[0]).sort()).toEqual(['h1', 'h2'])
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  it('多 host WS 消息按时间戳归并', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    const [ws1, ws2] = MockWebSocket.instances
    ws2.emit({ id: 100, service_id: 's', run_id: 'r', timestamp: '2026-05-21T12:00:01Z', level: 'INFO', message: 'B', stream: 'stdout' })
    ws1.emit({ id: 99,  service_id: 's', run_id: 'r', timestamp: '2026-05-21T12:00:00Z', level: 'INFO', message: 'A', stream: 'stdout' })
    ws2.emit({ id: 101, service_id: 's', run_id: 'r', timestamp: '2026-05-21T12:00:02Z', level: 'INFO', message: 'C', stream: 'stdout' })
    const logs = store.logsOf('ls1', 'all')
    expect(logs.map(l => l.message)).toEqual(['A', 'B', 'C'])
    expect(logs.map(l => l.host_id)).toEqual(['h1', 'h2', 'h2'])
  })

  it('unsubscribe 关闭所有 WS', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    store.unsubscribe('ls1', 'all')
    for (const ws of MockWebSocket.instances) expect(ws.closed).toBe(true)
  })

  it('参与同一 group 的重复 subscribe 共享连接（引用计数）', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    expect(MockWebSocket.instances).toHaveLength(2)
    store.unsubscribe('ls1', 'all')
    expect(MockWebSocket.instances.every(ws => !ws.closed)).toBe(true)
    store.unsubscribe('ls1', 'all')
    expect(MockWebSocket.instances.every(ws => ws.closed)).toBe(true)
  })

  it('host 隧道失败标记错误，其他 host 不受影响', async () => {
    (api.openTunnel as Mock).mockImplementation((hostId: string) => {
      if (hostId === 'h1') return Promise.reject(new Error('connect refused'))
      return Promise.resolve({ host_id: 'h2', state: 'open', local_port: 57101 })
    })
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(r => setTimeout(r, 5))
    expect(store.errorOf('ls1', 'all', 'h1')).toContain('connect refused')
    expect(MockWebSocket.instances).toHaveLength(1)
  })
})
```

### 5.3 实现：`stores/remoteLog.ts`

```typescript
// remoteLog store 管理"为某 (logSourceId, groupKey) 订阅的多节点实时日志"。
//
// 职责：
//   - 解析 LogSource 当前分组对应的 host 集合
//   - 为每个 host 经过 /api/tunnels/<host_id> 拿到本机端口，建立 WS
//   - 收到日志后按 timestamp 归并到统一列表（小堆/简单二分插入）
//   - 支持加载历史（HTTP /api/logs?host_id 经 tunnel 透传）
//   - 引用计数：多个 Panel 订阅同一 group 时只建一套连接
//
// 边界：
//   - 不渲染 UI
//   - 不做过滤 / 高亮（交给现有 logEngine / logDisplay）

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, WS_BASE, type RemoteLogEntry, type RemoteViewResponse } from '@/api/agent'

interface GroupSession {
  refCount: number
  view: RemoteViewResponse | null
  logs: RemoteLogEntry[]
  sockets: Map<string, WebSocket>
  errors: Map<string, string>
  loadingHistory: boolean
}

function key(logSourceId: string, groupKey: string): string {
  return `${logSourceId}::${groupKey}`
}

function insertSorted(arr: RemoteLogEntry[], entry: RemoteLogEntry) {
  if (arr.length === 0 || arr[arr.length - 1].timestamp <= entry.timestamp) {
    arr.push(entry)
    return
  }
  let lo = 0, hi = arr.length
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (arr[mid].timestamp <= entry.timestamp) lo = mid + 1
    else hi = mid
  }
  arr.splice(lo, 0, entry)
}

export const useRemoteLogStore = defineStore('remoteLog', () => {
  const sessions = ref<Map<string, GroupSession>>(new Map())

  async function subscribe(logSourceId: string, groupKey: string) {
    const k = key(logSourceId, groupKey)
    const existing = sessions.value.get(k)
    if (existing) {
      existing.refCount++
      return
    }

    const session: GroupSession = {
      refCount: 1,
      view: null,
      logs: [],
      sockets: new Map(),
      errors: new Map(),
      loadingHistory: false,
    }
    sessions.value.set(k, session)
    // 触发响应式更新
    sessions.value = new Map(sessions.value)

    let view: RemoteViewResponse
    try {
      view = await api.getRemoteView(logSourceId)
    } catch (e) {
      session.errors.set('__view__', e instanceof Error ? e.message : '加载视图失败')
      return
    }
    session.view = view

    const group = view.groups.find(g => g.group_key === groupKey)
    if (!group) {
      session.errors.set('__group__', `分组 ${groupKey} 不存在`)
      return
    }

    await Promise.all(group.host_ids.map(hostId => connectHost(session, view, hostId)))
    sessions.value = new Map(sessions.value)
  }

  async function connectHost(session: GroupSession, view: RemoteViewResponse, hostId: string) {
    try {
      const tunnel = await api.openTunnel(hostId)
      if (!tunnel.local_port) throw new Error(`隧道未就绪：${tunnel.state}`)
      const collectorName = view.log_source.name
      const url = `ws://127.0.0.1:${tunnel.local_port}/ws/logs?service=${encodeURIComponent(collectorName)}`
      const ws = new WebSocket(url)
      session.sockets.set(hostId, ws)
      ws.onmessage = (event) => {
        try {
          const raw = JSON.parse(event.data) as Omit<RemoteLogEntry, 'host_id'>
          insertSorted(session.logs, { ...raw, host_id: hostId })
          // 触发响应式
          session.logs = [...session.logs]
        } catch {
          /* 忽略坏帧 */
        }
      }
      ws.onerror = () => {
        session.errors.set(hostId, 'WebSocket 错误')
      }
      ws.onclose = () => {
        session.sockets.delete(hostId)
      }
    } catch (e) {
      session.errors.set(hostId, e instanceof Error ? e.message : '连接失败')
    }
  }

  function unsubscribe(logSourceId: string, groupKey: string) {
    const k = key(logSourceId, groupKey)
    const session = sessions.value.get(k)
    if (!session) return
    session.refCount--
    if (session.refCount > 0) return
    for (const ws of session.sockets.values()) {
      try { ws.close() } catch { /* ignore */ }
    }
    sessions.value.delete(k)
    sessions.value = new Map(sessions.value)
  }

  function logsOf(logSourceId: string, groupKey: string): RemoteLogEntry[] {
    return sessions.value.get(key(logSourceId, groupKey))?.logs ?? []
  }

  function errorOf(logSourceId: string, groupKey: string, hostId: string): string | undefined {
    return sessions.value.get(key(logSourceId, groupKey))?.errors.get(hostId)
  }

  function viewOf(logSourceId: string, groupKey: string): RemoteViewResponse | null {
    return sessions.value.get(key(logSourceId, groupKey))?.view ?? null
  }

  return {
    sessions,
    subscribe,
    unsubscribe,
    logsOf,
    errorOf,
    viewOf,
  }
})
```

### 验收

- `npm test -- remoteLog.test` 全绿
- 关键断言：引用计数、归并顺序、单 host 失败隔离

---

## Task 6：LogPanel 远程模式适配 + 节点 chips

**目标**：让 `LogPanel` 同时支持本地（`serviceId`）和远程（`logSourceId + groupKey`）两种模式；远程模式下日志来自 `remoteLog.logsOf`，每行展示 `[host-xx]` 前缀（颜色按 tag），顶部展示节点 chips 用于筛选。

### 6.1 `components/Panel/RemoteHostChips.vue`

```vue
<!--
RemoteHostChips：节点筛选 chips（host 维度）。

职责：
  - 显示该分组所有 host 的 chip，可单选/多选切换
  - 显示隧道状态（颜色：open=绿、connecting=灰、failed=红）
  - 显示错误（hover tooltip 或下方一行文字）

边界：
  - 不订阅 / 取消订阅（由 LogPanel 控制）
  - 只 emit selection 变更
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { tagColor } from '@/lib/tagColor'

const props = defineProps<{
  logSourceId: string
  groupKey: string
  selectedHostIds: Set<string>
}>()

const emit = defineEmits<{
  (e: 'update:selectedHostIds', value: Set<string>): void
}>()

const remote = useRemoteStore()
const remoteLog = useRemoteLogStore()

const groupHostIds = computed(() => {
  const view = remoteLog.viewOf(props.logSourceId, props.groupKey)
  if (!view) return [] as string[]
  return view.groups.find(g => g.group_key === props.groupKey)?.host_ids ?? []
})

function toggle(hostId: string) {
  const next = new Set(props.selectedHostIds)
  if (next.has(hostId)) next.delete(hostId)
  else next.add(hostId)
  emit('update:selectedHostIds', next)
}

function stateClass(hostId: string): string {
  const tunnel = remote.tunnelOf(hostId)
  if (!tunnel) return 'state-idle'
  return `state-${tunnel.state}`
}

function chipColor(hostId: string): string {
  const host = remote.hostById(hostId)
  if (!host || host.tags.length === 0) return 'var(--bg-secondary)'
  return tagColor(host.tags[0])
}

function hostError(hostId: string): string | undefined {
  return remoteLog.errorOf(props.logSourceId, props.groupKey, hostId)
}
</script>

<template>
  <div class="chips-bar">
    <button
      v-for="hostId in groupHostIds"
      :key="hostId"
      :class="['chip', stateClass(hostId), { active: selectedHostIds.has(hostId) }]"
      :style="{ borderColor: chipColor(hostId) }"
      :title="hostError(hostId) ?? ''"
      @click="toggle(hostId)"
      data-test="remote-host-chip"
    >
      <span class="dot" />
      {{ remote.hostById(hostId)?.name ?? hostId }}
    </button>
  </div>
</template>

<style scoped>
.chips-bar { display: flex; flex-wrap: wrap; gap: 4px; padding: 4px 8px; border-bottom: 1px solid var(--border-secondary); }
.chip { background: var(--bg-secondary); border: 1px solid var(--border-secondary); color: var(--text-primary); padding: 2px 8px; font-size: 11px; cursor: pointer; display: inline-flex; align-items: center; gap: 4px; border-left-width: 3px; }
.chip.active { background: var(--bg-tertiary, #2a2a2a); }
.chip .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--text-tertiary); }
.chip.state-open .dot { background: #5cb85c; }
.chip.state-connecting .dot { background: #f0ad4e; }
.chip.state-failed .dot { background: #d9534f; }
.chip.state-closed .dot { background: var(--text-tertiary); }
</style>
```

### 6.2 修改 `components/Panel/LogPanel.vue`

新增可选 props `logSourceId / groupKey`；当这两者存在时切换到远程模式：

```typescript
const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  logSourceId?: string | null      // 新增
  groupKey?: string | null         // 新增
}>()

const remoteLogStore = useRemoteLogStore()
const isRemote = computed(() => !!props.logSourceId && !!props.groupKey)

const selectedHostIds = ref<Set<string>>(new Set())

onMounted(() => {
  if (isRemote.value) {
    void remoteLogStore.subscribe(props.logSourceId!, props.groupKey!)
  } else {
    if (props.serviceId) void logStore.subscribe(props.serviceId)
  }
  // ...其余保持
})

onUnmounted(() => {
  if (isRemote.value) {
    remoteLogStore.unsubscribe(props.logSourceId!, props.groupKey!)
  } else {
    if (props.serviceId) logStore.unsubscribe(props.serviceId)
  }
  // ...其余保持
})

const rawLogs = computed<DisplayLogEntry[]>(() => {
  if (isRemote.value) {
    const all = remoteLogStore.logsOf(props.logSourceId!, props.groupKey!)
    if (selectedHostIds.value.size === 0) return all
    return all.filter(e => selectedHostIds.value.has(e.host_id))
  }
  if (props.serviceId) return logStore.getLogs(props.serviceId)
  // 现有 project 聚合逻辑保持
  // ...
})
```

模板顶部条件渲染 chips：

```vue
<RemoteHostChips
  v-if="isRemote && logSourceId && groupKey"
  :log-source-id="logSourceId"
  :group-key="groupKey"
  v-model:selected-host-ids="selectedHostIds"
/>
```

### 6.3 修改 `components/Panel/LogRow.vue`

新增可选 host 前缀渲染。增加 `hostName?: string` prop 和 `hostColor?: string`：

```typescript
const props = defineProps<{
  // ...existing
  hostName?: string | null
  hostColor?: string | null
}>()
```

模板在 timestamp 之前插入：

```vue
<span v-if="hostName" class="host-prefix" :style="{ color: hostColor ?? undefined }">[{{ hostName }}]</span>
```

`LogPanel` 渲染 LogRow 的位置传入 host 信息：

```typescript
function hostMetaFor(entry: DisplayLogEntry): { name: string; color: string } | null {
  if (!isRemote.value) return null
  const hostId = (entry as RemoteLogEntry).host_id
  const host = remote.hostById(hostId)
  if (!host) return { name: hostId, color: 'var(--text-tertiary)' }
  const tag = host.tags[0]
  return { name: host.name, color: tag ? tagColor(tag) : 'var(--text-tertiary)' }
}
```

模板：

```vue
<LogRow
  ...existing-props
  :host-name="hostMetaFor(item.entry)?.name"
  :host-color="hostMetaFor(item.entry)?.color"
/>
```

### 6.4 测试：`components/Panel/__tests__/RemoteHostChips.test.ts`

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import RemoteHostChips from '@/components/Panel/RemoteHostChips.vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'

describe('RemoteHostChips', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('渲染该 group 内每个 host 的 chip', async () => {
    const remote = useRemoteStore()
    const remoteLog = useRemoteLogStore()
    remote.hosts = [
      { id: 'h1', name: 'host-01', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 0, tags: ['prod'], created_at: '', updated_at: '' },
      { id: 'h2', name: 'host-02', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 0, tags: ['prod'], created_at: '', updated_at: '' },
    ]
    remoteLog.sessions.set('ls1::all', {
      refCount: 1,
      view: { log_source: { id: 'ls1', name: 'x', type: 'journalctl', host_ids: ['h1', 'h2'], created_at: '', updated_at: '' },
              groups: [{ group_key: 'all', host_ids: ['h1', 'h2'] }],
              hosts: [] },
      logs: [], sockets: new Map(), errors: new Map(), loadingHistory: false,
    } as unknown as never)
    remoteLog.sessions = new Map(remoteLog.sessions)
    const wrapper = mount(RemoteHostChips, {
      props: { logSourceId: 'ls1', groupKey: 'all', selectedHostIds: new Set() },
    })
    const chips = wrapper.findAll('[data-test="remote-host-chip"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].text()).toContain('host-01')
  })

  it('点击 chip emit 更新选中集合', async () => {
    const remote = useRemoteStore()
    const remoteLog = useRemoteLogStore()
    remote.hosts = [
      { id: 'h1', name: 'host-01', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 0, tags: [], created_at: '', updated_at: '' },
    ]
    remoteLog.sessions.set('ls1::all', {
      refCount: 1,
      view: { log_source: { id: 'ls1', name: 'x', type: 'journalctl', host_ids: ['h1'], created_at: '', updated_at: '' },
              groups: [{ group_key: 'all', host_ids: ['h1'] }], hosts: [] },
      logs: [], sockets: new Map(), errors: new Map(), loadingHistory: false,
    } as unknown as never)
    remoteLog.sessions = new Map(remoteLog.sessions)
    const wrapper = mount(RemoteHostChips, {
      props: { logSourceId: 'ls1', groupKey: 'all', selectedHostIds: new Set() },
    })
    await wrapper.find('[data-test="remote-host-chip"]').trigger('click')
    const emitted = wrapper.emitted('update:selectedHostIds')!
    const next = emitted[0][0] as Set<string>
    expect(next.has('h1')).toBe(true)
  })
})
```

### 6.5 测试：`components/Panel/__tests__/LogPanel.remote.test.ts`

只测远程模式分支，不重测本地分支。

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import LogPanel from '@/components/Panel/LogPanel.vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'

vi.mock('@/api/agent', () => ({ api: {}, WS_BASE: 'ws://127.0.0.1:57018' }))

describe('LogPanel 远程模式', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('挂载时调 remoteLog.subscribe；卸载时 unsubscribe', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      { id: 'h1', name: 'host-01', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 0, tags: ['prod'], created_at: '', updated_at: '' },
    ]
    const remoteLog = useRemoteLogStore()
    const sub = vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    const unsub = vi.spyOn(remoteLog, 'unsubscribe').mockReturnValue()

    const wrapper = mount(LogPanel, {
      props: { panelId: 'p1', serviceId: null, projectId: null, logSourceId: 'ls1', groupKey: 'all' },
    })
    expect(sub).toHaveBeenCalledWith('ls1', 'all')
    wrapper.unmount()
    expect(unsub).toHaveBeenCalledWith('ls1', 'all')
  })
})
```

### 验收

- `npm test -- RemoteHostChips LogPanel.remote` 全绿
- 手测：打开远程分组面板，能看到 chips、能切换、能看到 `[host-xx]` 前缀

---

## Task 7：SearchPage 远程模式适配——跨节点搜索

**目标**：让 `SearchPage` 支持远程模式（接收 `logSourceId + groupKey` 替代 `projectId`）；切换调用 `api.remoteSearch`；分页透传 cursor；结果按 host 前缀渲染；失败 host 列表展示。

### 7.1 修改 `components/Search/SearchPage.vue`

**新增 props**：

```typescript
const props = defineProps<{
  projectId?: string | null
  logSourceId?: string | null     // 新增
  groupKey?: string | null        // 新增
}>()

const isRemote = computed(() => !!props.logSourceId && !!props.groupKey)
```

**search 逻辑分叉**：

```typescript
const cursor = ref<string | null>(null)
const hostsFailed = ref<string[]>([])

async function runSearch(append = false) {
  if (isRemote.value) {
    const res = await api.remoteSearch({
      log_source_id: props.logSourceId!,
      group: props.groupKey!,
      query: queryInput.value,
      limit: 200,
      cursor: append ? cursor.value ?? undefined : undefined,
    })
    if (!append) {
      results.value = res.entries
    } else {
      results.value.push(...res.entries)
    }
    cursor.value = res.next_cursor
    hostsFailed.value = res.hosts_failed
    hasMore.value = res.has_more
    return
  }
  // ...现有本地 searchLogs 调用保持
}
```

**渲染失败 host 提示**：

```vue
<div v-if="isRemote && hostsFailed.length > 0" class="hosts-failed">
  ⚠ 以下节点超时或失败：{{ hostsFailed.join(', ') }}
</div>
```

**结果行的 host 前缀**：与 LogRow 共享 `hostName / hostColor` 逻辑（在 SearchPage 内部独立计算，因为 SearchPage 的渲染层级与 LogPanel 不同）。

### 7.2 workspace 远程搜索 tab

在 Task 5.1 中已加 `openRemoteSearch`。在 `WorkspaceShell.vue` 渲染时，对 `type === 'remote-search'` 渲染 `<SearchPage :log-source-id="tab.logSourceId" :group-key="tab.groupKey" />`。

在远程日志面板（Task 6）的工具栏新增一个按钮"搜索"，调 `workspace.openRemoteSearch(logSourceId, groupKey)`。

### 7.3 测试：`components/Search/__tests__/SearchPage.remote.test.ts`

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SearchPage from '@/components/Search/SearchPage.vue'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      remoteSearch: vi.fn().mockResolvedValue({
        entries: [
          { id: 1, service_id: 's', run_id: 'r', timestamp: '2026-05-21T12:00:00Z', level: 'INFO', message: 'hit-A', stream: 'stdout', host_id: 'h1' },
        ],
        total_by_host: { h1: 1 },
        hosts_failed: ['h2'],
        next_cursor: 'cur-2',
        has_more: true,
      }),
      // 防止本地搜索分支误触
      searchLogs: vi.fn(),
    },
  }
})

describe('SearchPage 远程模式', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('提交查询时调 api.remoteSearch', async () => {
    const wrapper = mount(SearchPage, {
      props: { projectId: null, logSourceId: 'ls1', groupKey: 'prod' },
    })
    // 触发查询的方式取决于 SearchPage 现有交互；这里调内部方法或填表单。
    // 执行者按现有 SearchPage 的 search 触发路径替换以下两行：
    await (wrapper.vm as unknown as { runSearch: (a?: boolean) => Promise<void> }).runSearch?.(false)
    await new Promise(r => setTimeout(r))
    expect(api.remoteSearch).toHaveBeenCalled()
    const arg = (api.remoteSearch as unknown as { mock: { calls: unknown[][] } }).mock.calls[0][0] as { log_source_id: string; group: string }
    expect(arg.log_source_id).toBe('ls1')
    expect(arg.group).toBe('prod')
  })

  it('展示失败 host 提示', async () => {
    const wrapper = mount(SearchPage, {
      props: { projectId: null, logSourceId: 'ls1', groupKey: 'prod' },
    })
    await (wrapper.vm as unknown as { runSearch: (a?: boolean) => Promise<void> }).runSearch?.(false)
    await new Promise(r => setTimeout(r))
    expect(wrapper.text()).toContain('以下节点超时或失败')
    expect(wrapper.text()).toContain('h2')
  })
})
```

### 验收

- `npm test -- SearchPage.remote` 全绿
- 手测：远程面板"搜索"按钮 → 跨节点搜索结果带 host 前缀；分页能取到下一页；失败 host 显示警告

---

## Task 8：端到端集成验证 + 类型 / lint 收尾

### 8.1 手测清单

依次完成（先确保后端 plan 1a + 1b 已经合并且本地 agent 跑起来）：

- [ ] 启动 desktop（`cd desktop && npm run tauri dev`）
- [ ] 设置页 → 主机管理
  - [ ] "新建主机"表单各字段可填，密钥"浏览..."调起 Tauri dialog 并填入路径
  - [ ] 保存后列表出现新行，隧道列显示 `idle`
  - [ ] "从 SSH config 导入" → 出现 ~/.ssh/config Host 列表 → 多选导入 → 列表追加
  - [ ] 编辑、删除按预期工作
- [ ] Sidebar 远程监听
  - [ ] 齿轮按钮跳到 `/settings?tab=hosts`
  - [ ] "+ 新建监听任务" → 填 name / type / 选 host → 保存 → 出现在 Sidebar
  - [ ] 展开监听任务，显示 `全部 / prod / test / temp` 等分组
- [ ] 实时日志
  - [ ] 点击某个分组 → 打开 Panel
  - [ ] 顶部出现 chips（host-01 / host-02 ...）
  - [ ] 各 chip 显示 open（绿点）/ failed（红点）状态
  - [ ] 日志流持续滚动，每行前缀 `[host-xx]`，按时间戳有序
  - [ ] 点击 chip 切换筛选，日志列表过滤正确
- [ ] 跨节点搜索
  - [ ] 远程面板工具栏"搜索"按钮 → 打开 search tab
  - [ ] 输入关键词、勾选 host → 显示结果，每行前缀
  - [ ] 失败 host 提示存在（人为关一台远端 agent 模拟）
  - [ ] 滚动到底加载下一页（cursor 正确透传）

### 8.2 自动化

```bash
cd desktop
npm run lint
npm run typecheck         # 若未配置则 npx vue-tsc --noEmit
npm test
```

### 8.3 待补 Issue（已知不足）

- WS 自动重连：当前实现 onclose 后不重连，留待后续
- 隧道空闲释放：依赖后端 10 分钟自动断（spec §6.3），前端不主动调 closeTunnel
- 时间窗筛选（from/to）：UI 不增加默认时间，但用户手动选择仍生效（透传到 API）

---

## 自检：Spec 覆盖确认

| Spec 章节 | 前端覆盖 task | 验证手段 |
|----------|-------------|---------|
| §6.1 Sidebar 远程监听 | Task 4 | 手测 Sidebar 显示 + 齿轮跳转 |
| §6.2 多节点混流面板 | Task 5 + 6 | LogPanel.remote.test + 手测前缀着色 |
| §6.3 隧道生命周期与 UI 反馈 | Task 6（chip 状态色） | RemoteHostChips.test |
| §6.4 主机管理 + SSH config 导入 + 密钥浏览 | Task 3 | HostManagerTab.test + 手测 |
| §7 跨节点搜索 + 复合游标 | Task 7 | SearchPage.remote.test + 手测分页 |
| §10 步骤 5 设置页 | Task 3 | 见 §6.4 行 |
| §10 步骤 6 Sidebar | Task 4 | 同 §6.1 行 |
| §10 步骤 7 日志面板远程 | Task 5 + 6 | 同 §6.2 行 |
| §10 步骤 8 跨节点搜索 | Task 7 | 同 §7 行 |

---

## 执行交接

执行顺序：Task 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8。

每个 Task：
1. 先写 / 复制测试文件并运行（应失败）
2. 再写实现，直到测试通过
3. `npm run lint && npm run typecheck` 通过
4. commit（信息按现有约定：`feat(desktop): ...` / `feat(desktop/remote): ...`）

依赖后端：
- Task 1 / 2 仅类型，可在后端就绪前完成
- Task 3 起需要后端 `/api/hosts`、`/api/ssh-config/hosts`、`/api/log-sources` 可用
- Task 5 / 6 / 7 需要后端 `/api/tunnels`、`/api/remote/view`、`/ws/logs`、`/api/remote-log-search` 可用

注意事项：
- 不写 `// 用于 X 流程`、`// added for issue #Y` 这类反 CLAUDE.md 的注释
- 所有 CSS 用现有 `var(--*)` 变量，禁止硬编码颜色（tagColor.ts 例外，因为它就是色彩库）
- modal 类组件统一用 `v-if="visible"` 控制；不用 `Teleport`（与现有风格一致）
