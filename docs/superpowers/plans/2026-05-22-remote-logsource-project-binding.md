# Remote LogSource Project/Service Binding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 允许远程监听任务（LogSource）可选绑定本地项目/服务，绑定后按服务聚合显示在项目下的"远程监听"子区块，未绑定的保持在底部独立区块。

**Architecture:** 后端 `model.LogSource` 新增 `ProjectID`/`ServiceID` 两个可选字段，CRUD 透传。前端 `remote.ts` store 新增聚合 computed，`SidebarView` 在每个项目下渲染绑定任务的聚合子区块，`LogPanel` 支持接收多个 `logSourceIds` 并行订阅后合并日志流。

**Tech Stack:** Go（后端模型/CRUD）、Vue 3 + Pinia（前端 store/组件）、TypeScript

---

## 文件变更清单

### 后端
| 文件 | 变更 |
|------|------|
| `agent/model/model.go` | `LogSource` 加 `ProjectID`、`ServiceID` 字段 |
| `agent/api/handler_remote_view.go` | `buildGroups` 签名不变，逻辑已正确（按 LogSource.Tags）|

### 前端
| 文件 | 变更 |
|------|------|
| `desktop/src/api/agent.ts` | `LogSource` 类型加字段；`LogSourceCreatePayload` 加字段 |
| `desktop/src/stores/remote.ts` | 新增 `remoteServiceGroupsOf(projectId)` computed helper |
| `desktop/src/stores/remoteLog.ts` | 不变（session 仍按单个 logSourceId+groupKey） |
| `desktop/src/stores/workspace.ts` | 新增 `RemoteAggregateTab` 类型；新增 `openRemoteAggregate` action |
| `desktop/src/components/Sidebar/LogSourceFormModal.vue` | 新增项目/服务下拉绑定字段 |
| `desktop/src/components/Sidebar/RemoteListenSection.vue` | 新增 `ProjectRemoteSection` 子组件入口（或内联） |
| `desktop/src/components/Sidebar/ProjectRemoteSection.vue` | 新建：项目下远程监听子区块 |
| `desktop/src/components/Sidebar/SidebarView.vue` | 每个项目下渲染 `ProjectRemoteSection` |
| `desktop/src/components/Panel/LogPanel.vue` | 支持 `logSourceIds: string[]` prop，并行订阅合并 |
| `desktop/src/components/Workspace/WorkspaceShell.vue` | 处理新 `remote-aggregate` tab 类型 |

---

## Task 1: 后端模型加字段

**Files:**
- Modify: `agent/model/model.go`
- Modify: `agent/api/handler_remote_view_test.go`（验证新字段可持久化）

- [ ] **Step 1: 修改 LogSource 模型**

打开 `agent/model/model.go`，在 `LogSource` struct 末尾加两个字段：

```go
type LogSource struct {
    ID        string        `json:"id"`
    Name      string        `json:"name"`
    Type      LogSourceType `json:"type"`
    HostIDs   []string      `json:"host_ids"`
    Tags      []string      `json:"tags"`
    ExtraArgs []string      `json:"extra_args"`
    ProjectID string        `json:"project_id,omitempty"`
    ServiceID string        `json:"service_id,omitempty"`
}
```

- [ ] **Step 2: 确认后端编译通过**

```bash
cd /Users/xushixin/workspace/super-debug/agent && go build ./...
```

Expected: 无错误输出

- [ ] **Step 3: 运行现有测试确认不破坏**

```bash
cd /Users/xushixin/workspace/super-debug/agent && go test ./...
```

Expected: 全部 PASS

- [ ] **Step 4: 写一个覆盖新字段的测试（加在 `handler_remote_view_test.go` 末尾）**

在 `TestRemoteViewAggregation` 之后新增：

```go
func TestLogSourceProjectBinding(t *testing.T) {
    srv, _ := newTestApp(t)

    // 创建 LogSource 并带 project_id/service_id
    body, _ := json.Marshal(map[string]any{
        "name":       "server",
        "type":       "journalctl",
        "host_ids":   []string{},
        "project_id": "proj-abc",
        "service_id": "svc-xyz",
    })
    resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
    require.NoError(t, err)
    defer resp.Body.Close()
    require.Equal(t, http.StatusOK, resp.StatusCode)

    var created model.LogSource
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
    assert.Equal(t, "proj-abc", created.ProjectID)
    assert.Equal(t, "svc-xyz", created.ServiceID)

    // 查询回来字段仍在
    listResp, _ := http.Get(srv.URL + "/api/log-sources")
    defer listResp.Body.Close()
    var list []model.LogSource
    require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
    require.Len(t, list, 1)
    assert.Equal(t, "proj-abc", list[0].ProjectID)
    assert.Equal(t, "svc-xyz", list[0].ServiceID)
}
```

- [ ] **Step 5: 运行新测试**

```bash
cd /Users/xushixin/workspace/super-debug/agent && go test ./api/... -run TestLogSourceProjectBinding -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add agent/model/model.go agent/api/handler_remote_view_test.go
git commit -m "feat(agent): add ProjectID/ServiceID to LogSource model"
```

---

## Task 2: 前端 API 类型和 Store 聚合 computed

**Files:**
- Modify: `desktop/src/api/agent.ts`
- Modify: `desktop/src/stores/remote.ts`
- Test: `desktop/src/stores/__tests__/remote.test.ts`

- [ ] **Step 1: 更新前端 LogSource 类型**

打开 `desktop/src/api/agent.ts`，找到 `LogSource` interface，加两个可选字段：

```typescript
export interface LogSource {
  id: string
  name: string
  type: LogSourceType
  host_ids: string[]
  tags: string[]
  extra_args: string[]
  project_id?: string
  service_id?: string
}
```

同时更新 `LogSourceCreatePayload`：

```typescript
export interface LogSourceCreatePayload {
  name: string
  type: LogSourceType
  host_ids: string[]
  tags?: string[]
  extra_args?: string[]
  project_id?: string
  service_id?: string
}
```

- [ ] **Step 2: 在 `remote.ts` 新增聚合数据结构和 computed**

打开 `desktop/src/stores/remote.ts`。

在文件顶部现有 `Group` interface 之后，新增：

```typescript
export interface RemoteServiceGroup {
  serviceId: string
  serviceName: string
  logSourceIds: string[]   // 参与聚合的 LogSource ID
  groups: Group[]          // all + tag 分组
}
```

在 `groupsOf` 函数之后，新增 `remoteServiceGroupsOf`：

```typescript
// remoteServiceGroupsOf 返回指定项目下按服务聚合的远程监听分组。
// 分组规则：
//   - all: 所有参与聚合的 LogSource 的 HostIDs 合集
//   - tag 分组: 只含打了该 tag 的 LogSource 对应的 HostIDs
function remoteServiceGroupsOf(projectId: string): RemoteServiceGroup[] {
  const bound = logSources.value.filter(ls => ls.project_id === projectId && ls.service_id)
  if (bound.length === 0) return []

  // 按 service_id 聚合
  const byService = new Map<string, LogSource[]>()
  for (const ls of bound) {
    const key = ls.service_id!
    if (!byService.has(key)) byService.set(key, [])
    byService.get(key)!.push(ls)
  }

  const hostMap = new Map(hosts.value.map(h => [h.id, h]))

  return Array.from(byService.entries()).map(([serviceId, sources]) => {
    // all: 合并所有 HostIDs
    const allHostIds = [...new Set(sources.flatMap(ls => ls.host_ids.filter(id => hostMap.has(id))))]

    // tag 分组: 按各 LogSource 的 tags 建立 tag→hostIds 映射
    const tagToHosts = new Map<string, string[]>()
    for (const ls of sources) {
      const validHosts = ls.host_ids.filter(id => hostMap.has(id))
      for (const tag of ls.tags ?? []) {
        if (!tagToHosts.has(tag)) tagToHosts.set(tag, [])
        const existing = tagToHosts.get(tag)!
        for (const h of validHosts) {
          if (!existing.includes(h)) existing.push(h)
        }
      }
    }
    const sortedTags = [...tagToHosts.keys()].sort((a, b) => a.localeCompare(b))

    const groups: Group[] = [{ key: 'all', hostIds: allHostIds }]
    for (const tag of sortedTags) {
      groups.push({ key: tag, hostIds: tagToHosts.get(tag)! })
    }

    // 服务名：从 agentStore 查，找不到则用 serviceId 截断
    const agentStore = useAgentStore()
    const project = agentStore.projectById(projectId)
    const svc = project?.services.find(s => s.id === serviceId)
    const serviceName = svc?.name ?? serviceId.slice(0, 16)

    return {
      serviceId,
      serviceName,
      logSourceIds: sources.map(ls => ls.id),
      groups,
    }
  })
}
```

在 `return { ... }` 里加上 `remoteServiceGroupsOf`：

```typescript
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
  remoteServiceGroupsOf,
  loadTunnels,
  applyTunnelUpdate,
  tunnelOf,
}
```

注意：`remote.ts` 顶部需要引入 `useAgentStore`：

```typescript
import { useAgentStore } from '@/stores/agent'
```

- [ ] **Step 3: 运行类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 4: 在 `remote.test.ts` 中新增聚合 computed 测试**

打开 `desktop/src/stores/__tests__/remote.test.ts`，在末尾追加：

```typescript
describe('remoteServiceGroupsOf', () => {
  it('returns empty when no bound logSources', () => {
    const store = useRemoteStore()
    store.hosts = []
    store.logSources = []
    expect(store.remoteServiceGroupsOf('proj-1')).toEqual([])
  })

  it('aggregates logSources by serviceId with correct tag grouping', () => {
    const store = useRemoteStore()
    store.hosts = [
      { id: 'h1', name: 'host1', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, local_tunnel_port: 0, tags: [] },
      { id: 'h2', name: 'host2', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, local_tunnel_port: 0, tags: [] },
      { id: 'h3', name: 'host3', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, local_tunnel_port: 0, tags: [] },
    ]
    store.logSources = [
      { id: 'ls-a', name: 'server', type: 'journalctl', host_ids: ['h1'], tags: ['prod'], extra_args: [], project_id: 'proj-1', service_id: 'svc-server' },
      { id: 'ls-b', name: 'tk-server', type: 'journalctl', host_ids: ['h2', 'h3'], tags: ['test'], extra_args: [], project_id: 'proj-1', service_id: 'svc-server' },
    ]

    const result = store.remoteServiceGroupsOf('proj-1')
    expect(result).toHaveLength(1)

    const svcGroup = result[0]
    expect(svcGroup.serviceId).toBe('svc-server')
    expect(svcGroup.logSourceIds).toEqual(['ls-a', 'ls-b'])

    const groupMap = Object.fromEntries(svcGroup.groups.map(g => [g.key, g.hostIds]))
    expect(groupMap['all']).toEqual(expect.arrayContaining(['h1', 'h2', 'h3']))
    expect(groupMap['all']).toHaveLength(3)
    // prod 只来自 ls-a → 只有 h1
    expect(groupMap['prod']).toEqual(['h1'])
    // test 只来自 ls-b → h2, h3
    expect(groupMap['test']).toEqual(expect.arrayContaining(['h2', 'h3']))
    expect(groupMap['test']).toHaveLength(2)
  })

  it('ignores logSources bound to other projects', () => {
    const store = useRemoteStore()
    store.hosts = [{ id: 'h1', name: 'host1', ssh_host: '', ssh_port: 22, ssh_user: '', remote_agent_port: 57017, local_tunnel_port: 0, tags: [] }]
    store.logSources = [
      { id: 'ls-a', name: 'server', type: 'journalctl', host_ids: ['h1'], tags: [], extra_args: [], project_id: 'proj-other', service_id: 'svc-server' },
    ]
    expect(store.remoteServiceGroupsOf('proj-1')).toEqual([])
  })
})
```

- [ ] **Step 5: 运行测试**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx vitest run src/stores/__tests__/remote.test.ts
```

Expected: 全部 PASS

- [ ] **Step 6: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/api/agent.ts desktop/src/stores/remote.ts desktop/src/stores/__tests__/remote.test.ts
git commit -m "feat(frontend): add project/service binding to LogSource type and store aggregation"
```

---

## Task 3: workspace store 新增聚合 tab 类型

**Files:**
- Modify: `desktop/src/stores/workspace.ts`

当项目下远程监听子区块点击某个分组时，需要打开一个聚合视图面板。面板展示多个 LogSource 绑定同一服务的合并日志。

- [ ] **Step 1: 新增 `RemoteAggregateTab` 类型**

打开 `desktop/src/stores/workspace.ts`，在 `RemoteSearchWorkspaceTab` 之后新增：

```typescript
export interface RemoteAggregateTab {
  id: string
  type: 'remote-aggregate'
  projectId: string
  serviceId: string
  serviceName: string
  logSourceIds: string[]   // 参与聚合的所有 LogSource ID
  groupKey: string         // 'all' 或具体 tag
  title: string
}
```

在 `WorkspaceTab` union 类型里加入 `RemoteAggregateTab`：

```typescript
export type WorkspaceTab =
  | ProjectWorkspaceTab
  | SearchWorkspaceTab
  | RemoteWorkspaceTab
  | RemoteSearchWorkspaceTab
  | RemoteAggregateTab
```

- [ ] **Step 2: 新增 `openRemoteAggregate` action**

在 `openRemoteSearch` 函数之后新增：

```typescript
function openRemoteAggregate(
  projectId: string,
  serviceId: string,
  serviceName: string,
  logSourceIds: string[],
  groupKey: string,
): RemoteAggregateTab {
  saveActiveProjectLayout()
  const id = `remote-aggregate:${serviceId}:${groupKey}`
  const existing = tabs.value.find(
    (tab): tab is RemoteAggregateTab => tab.type === 'remote-aggregate' && tab.id === id,
  )
  if (existing) {
    // 更新 logSourceIds 以防绑定关系发生变化
    existing.logSourceIds = logSourceIds
    activeTabId.value = existing.id
    return existing
  }
  const tab: RemoteAggregateTab = {
    id,
    type: 'remote-aggregate',
    projectId,
    serviceId,
    serviceName,
    logSourceIds,
    groupKey,
    title: `${serviceName} · ${groupKey}`,
  }
  tabs.value.push(tab)
  activeTabId.value = tab.id
  return tab
}
```

在 `return { ... }` 里加上 `openRemoteAggregate`。

- [ ] **Step 3: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/stores/workspace.ts
git commit -m "feat(workspace): add RemoteAggregateTab type and openRemoteAggregate action"
```

---

## Task 4: LogPanel 支持多 logSourceIds 聚合

**Files:**
- Modify: `desktop/src/components/Panel/LogPanel.vue`

当前 `LogPanel` 只接收单个 `logSourceId`。需要支持接收 `logSourceIds: string[]`（多个），对每个分别订阅 `remoteLog` session，再合并日志流显示。

- [ ] **Step 1: 更新 props 定义**

打开 `desktop/src/components/Panel/LogPanel.vue`，找到 `defineProps`，将 `logSourceId` 改为同时支持单个和多个：

```typescript
const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  logSourceId?: string | null       // 保留：兼容现有单任务 remote tab
  logSourceIds?: string[] | null    // 新增：聚合 tab 使用
  groupKey?: string | null
}>()
```

- [ ] **Step 2: 新增 effectiveLogSourceIds computed**

在 `const isRemote` 之前新增：

```typescript
// 聚合模式下使用 logSourceIds，单任务模式下使用 logSourceId 包装为数组
const effectiveLogSourceIds = computed<string[]>(() => {
  if (props.logSourceIds && props.logSourceIds.length > 0) return props.logSourceIds
  if (props.logSourceId) return [props.logSourceId]
  return []
})

const isRemote = computed(() => effectiveLogSourceIds.value.length > 0 && !!props.groupKey)
```

- [ ] **Step 3: 更新 onMounted / onUnmounted / watch 订阅逻辑**

将 `onMounted` 里的远程订阅改为：

```typescript
onMounted(() => {
  if (isRemote.value && props.groupKey) {
    for (const lsId of effectiveLogSourceIds.value) {
      void remoteLogStore.subscribe(lsId, props.groupKey)
    }
  } else if (props.serviceId) {
    void logStore.subscribe(props.serviceId)
  }
  if (props.projectId) filterStore.loadProjectRules(props.projectId)
  refreshDisplayImmediately()
  scrollToBottom()
})
```

将 `onUnmounted` 里的远程取消订阅改为：

```typescript
onUnmounted(() => {
  if (isRemote.value && props.groupKey) {
    for (const lsId of effectiveLogSourceIds.value) {
      remoteLogStore.unsubscribe(lsId, props.groupKey)
    }
  } else if (props.serviceId) {
    logStore.unsubscribe(props.serviceId)
  }
  filterStore.removePanel(props.panelId)
  if (displayRefreshTimer) clearTimeout(displayRefreshTimer)
  cancelScrollRetries()
})
```

将 `watch(() => [props.logSourceId, props.groupKey])` 改为：

```typescript
watch(
  () => [effectiveLogSourceIds.value, props.groupKey] as const,
  ([newIds, newGroupKey], [oldIds, oldGroupKey]) => {
    if (oldGroupKey) {
      for (const lsId of (oldIds as string[])) {
        remoteLogStore.unsubscribe(lsId, oldGroupKey)
      }
    }
    selectedHostIds.value = new Set()
    if (newGroupKey) {
      for (const lsId of (newIds as string[])) {
        void remoteLogStore.subscribe(lsId, newGroupKey)
      }
    }
    isFollowing.value = true
    refreshDisplayImmediately()
  },
  { deep: true },
)
```

- [ ] **Step 4: 更新 rawLogs computed 合并多个 logSource 的日志**

将 `rawLogs` 中的远程分支改为：

```typescript
const rawLogs = computed<DisplayLogEntry[]>(() => {
  if (isRemote.value && props.groupKey) {
    const selected = selectedHostIds.value
    const allLogs = effectiveLogSourceIds.value.flatMap(lsId =>
      remoteLogStore.logsOf(lsId, props.groupKey!)
    )
    // 去重：同一 host_id + id 只保留一条
    const seen = new Set<string>()
    const deduped = allLogs.filter(entry => {
      const key = `${entry.host_id}:${entry.id}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
    deduped.sort((a, b) => {
      const t = new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
      return t !== 0 ? t : a.id - b.id
    })
    return deduped
      .filter(entry => selected.size === 0 || selected.has(entry.host_id))
      .map(toRemoteDisplayEntry)
  }
  if (props.serviceId) return logStore.getLogs(props.serviceId)
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    if (!proj) return []
    return proj.services
      .flatMap((s: { id: string }) => logStore.getLogs(s.id))
      .sort((a: DisplayLogEntry, b: DisplayLogEntry) => a.id - b.id)
  }
  return []
})
```

- [ ] **Step 5: 更新 tryLoadMoreHistory 支持多 logSource**

将 `tryLoadMoreHistory` 中的远程分支改为：

```typescript
async function tryLoadMoreHistory() {
  if (isRemote.value && props.groupKey) {
    if (isLoadingHistory.value) return
    isLoadingHistory.value = true
    const el = logListEl.value
    const prevScrollHeight = el?.scrollHeight ?? 0
    await Promise.all(
      effectiveLogSourceIds.value.map(lsId =>
        remoteLogStore.loadHistory(lsId, props.groupKey!)
      )
    )
    await nextTick()
    if (el) {
      const added = el.scrollHeight - prevScrollHeight
      if (added > 0) {
        programmaticScroll = true
        el.scrollTop += added
        requestAnimationFrame(() => { programmaticScroll = false })
      }
    }
    isLoadingHistory.value = false
    return
  }
  // ... 本地日志历史加载逻辑不变 ...
}
```

- [ ] **Step 6: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 7: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Panel/LogPanel.vue
git commit -m "feat(LogPanel): support multiple logSourceIds for aggregate remote view"
```

---

## Task 5: WorkspaceShell 处理 remote-aggregate tab

**Files:**
- Modify: `desktop/src/components/Workspace/WorkspaceShell.vue`

- [ ] **Step 1: 新增 remote-aggregate 分支**

打开 `desktop/src/components/Workspace/WorkspaceShell.vue`，在 `v-else-if="workspace.activeTab.type === 'remote'"` 之后新增：

```html
<LogPanel
  v-else-if="workspace.activeTab.type === 'remote-aggregate'"
  :panel-id="workspace.activeTab.id"
  :service-id="null"
  :project-id="null"
  :log-source-ids="workspace.activeTab.logSourceIds"
  :group-key="workspace.activeTab.groupKey"
/>
```

- [ ] **Step 2: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 3: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Workspace/WorkspaceShell.vue
git commit -m "feat(WorkspaceShell): handle remote-aggregate tab type"
```

---

## Task 6: 新建 ProjectRemoteSection 组件

**Files:**
- Create: `desktop/src/components/Sidebar/ProjectRemoteSection.vue`

这是项目下"远程监听"子区块，展示该项目绑定的远程服务分组列表，点击分组打开聚合面板。

- [ ] **Step 1: 创建组件**

新建 `desktop/src/components/Sidebar/ProjectRemoteSection.vue`：

```vue
<!--
ProjectRemoteSection：项目下的远程监听子区块。

职责：
  - 展示绑定了该项目的远程监听任务，按服务聚合
  - 点击分组 emit open 事件，由 SidebarView 打开聚合面板

边界：
  - 不直接打开 tab，只 emit
  - 分组数据由 remote store 聚合计算
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'

const props = defineProps<{ projectId: string }>()

const emit = defineEmits<{
  open: [payload: { projectId: string; serviceId: string; serviceName: string; logSourceIds: string[]; groupKey: string }]
}>()

const remote = useRemoteStore()
const serviceGroups = computed(() => remote.remoteServiceGroupsOf(props.projectId))
</script>

<template>
  <div v-if="serviceGroups.length > 0" class="project-remote-section">
    <div class="section-label">远程监听</div>
    <div v-for="sg in serviceGroups" :key="sg.serviceId" class="service-block">
      <div class="service-name">{{ sg.serviceName }}</div>
      <div
        v-for="group in sg.groups"
        :key="group.key"
        class="group-row"
        @click="emit('open', {
          projectId,
          serviceId: sg.serviceId,
          serviceName: sg.serviceName,
          logSourceIds: sg.logSourceIds,
          groupKey: group.key,
        })"
      >
        <span class="chip">{{ group.key }}</span>
        <span class="count">({{ group.hostIds.length }} 节点)</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.project-remote-section {
  border-top: 1px solid var(--border-secondary);
  padding: 4px 0 2px;
}
.section-label {
  padding: 3px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.service-block {
  margin-bottom: 2px;
}
.service-name {
  padding: 2px 12px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
}
.group-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 20px;
  cursor: pointer;
  font-size: 11px;
}
.group-row:hover {
  background: var(--bg-secondary);
}
.chip {
  padding: 1px 6px;
  background: var(--bg-secondary);
  border-radius: 2px;
  font-size: 10px;
  color: var(--text-primary);
}
.count {
  color: var(--text-tertiary);
}
</style>
```

- [ ] **Step 2: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 3: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/ProjectRemoteSection.vue
git commit -m "feat(sidebar): add ProjectRemoteSection component for bound remote tasks"
```

---

## Task 7: SidebarView 集成 ProjectRemoteSection

**Files:**
- Modify: `desktop/src/components/Sidebar/SidebarView.vue`

- [ ] **Step 1: 引入组件并在每个项目下渲染**

打开 `desktop/src/components/Sidebar/SidebarView.vue`。

在 `<script setup>` 中引入：

```typescript
import ProjectRemoteSection from './ProjectRemoteSection.vue'
```

新增 `openRemoteAggregate` 处理函数：

```typescript
function openRemoteAggregate(payload: {
  projectId: string
  serviceId: string
  serviceName: string
  logSourceIds: string[]
  groupKey: string
}) {
  workspace.openRemoteAggregate(
    payload.projectId,
    payload.serviceId,
    payload.serviceName,
    payload.logSourceIds,
    payload.groupKey,
  )
}
```

- [ ] **Step 2: 在 template 的项目循环里加入子区块**

找到：

```html
<template v-for="project in agentStore.projects" :key="project.id">
  <ProjectHeader :project="project" @search="openProjectSearch(project.id)" />
  <ServiceRow
    v-for="service in project.services"
    :key="service.id"
    :service="service"
    :project-id="project.id"
    :selected="isServiceSelected(service.id)"
    @click="selectService(service.id, project.id)"
  />
</template>
```

改为：

```html
<template v-for="project in agentStore.projects" :key="project.id">
  <ProjectHeader :project="project" @search="openProjectSearch(project.id)" />
  <ServiceRow
    v-for="service in project.services"
    :key="service.id"
    :service="service"
    :project-id="project.id"
    :selected="isServiceSelected(service.id)"
    @click="selectService(service.id, project.id)"
  />
  <ProjectRemoteSection
    :project-id="project.id"
    @open="openRemoteAggregate"
  />
</template>
```

- [ ] **Step 3: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/SidebarView.vue
git commit -m "feat(sidebar): render ProjectRemoteSection under each project"
```

---

## Task 8: LogSourceFormModal 新增项目/服务绑定字段

**Files:**
- Modify: `desktop/src/components/Sidebar/LogSourceFormModal.vue`

- [ ] **Step 1: 更新 script 部分**

打开 `desktop/src/components/Sidebar/LogSourceFormModal.vue`。

在 `<script setup>` 中引入 `useAgentStore`：

```typescript
import { useAgentStore } from '@/stores/agent'
```

在 `const store = useRemoteStore()` 之后新增：

```typescript
const agentStore = useAgentStore()
const boundProjectId = ref('')
const boundServiceId = ref('')

const servicesOfBoundProject = computed(() => {
  if (!boundProjectId.value) return []
  return agentStore.projectById(boundProjectId.value)?.services ?? []
})
```

在 `watch` 的 `if (initial)` 分支里补充字段恢复：

```typescript
if (initial) {
  name.value = initial.name
  type.value = initial.type
  hostIds.value = new Set(initial.host_ids)
  tags.value = [...(initial.tags ?? [])]
  restoreFromExtraArgs(initial.extra_args ?? [])
  boundProjectId.value = initial.project_id ?? ''
  boundServiceId.value = initial.service_id ?? ''
  return
}
```

在 `else` 分支（新建时重置）里补充：

```typescript
boundProjectId.value = ''
boundServiceId.value = ''
```

在 `watch` 中监听 `boundProjectId` 变化，自动清空服务选择：

```typescript
watch(boundProjectId, () => {
  boundServiceId.value = ''
})
```

在 `submit()` 函数里把新字段加入 payload：

```typescript
function submit() {
  emit('submit', {
    name: name.value.trim(),
    type: type.value,
    host_ids: Array.from(hostIds.value),
    tags: tags.value,
    extra_args: extraArgs.value,
    project_id: boundProjectId.value || undefined,
    service_id: boundServiceId.value || undefined,
  })
}
```

- [ ] **Step 2: 更新 template，在"关联主机"字段之前插入绑定字段**

在 `<div class="field">` 采集类型之后、关联主机之前插入：

```html
<div class="field">
  <label>绑定项目（可选）</label>
  <select v-model="boundProjectId" data-test="logsource-form-project">
    <option value="">不绑定</option>
    <option v-for="proj in agentStore.projects" :key="proj.id" :value="proj.id">
      {{ proj.name }}
    </option>
  </select>
</div>

<div v-if="boundProjectId" class="field">
  <label>绑定服务（可选）</label>
  <select v-model="boundServiceId" data-test="logsource-form-service">
    <option value="">不绑定</option>
    <option v-for="svc in servicesOfBoundProject" :key="svc.id" :value="svc.id">
      {{ svc.name }}
    </option>
  </select>
</div>
```

- [ ] **Step 3: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/LogSourceFormModal.vue
git commit -m "feat(LogSourceFormModal): add optional project/service binding fields"
```

---

## Task 9: RemoteListenSection 过滤已绑定任务

**Files:**
- Modify: `desktop/src/components/Sidebar/RemoteListenSection.vue`

底部"远程监听"独立区块只展示**未绑定项目**的 LogSource，已绑定的已在项目下展示。

- [ ] **Step 1: 过滤已绑定的 LogSource**

打开 `desktop/src/components/Sidebar/RemoteListenSection.vue`，在 `<script setup>` 中新增 computed：

```typescript
const unboundLogSources = computed(() =>
  store.logSources.filter(ls => !ls.project_id)
)
```

- [ ] **Step 2: 更新 template 使用 unboundLogSources**

找到：

```html
<RemoteLogSourceRow
  v-for="logSource in store.logSources"
  :key="logSource.id"
  ...
/>
<div v-if="store.logSources.length === 0 && !error" class="empty">还没有监听任务</div>
```

改为：

```html
<RemoteLogSourceRow
  v-for="logSource in unboundLogSources"
  :key="logSource.id"
  :log-source="logSource"
  @open="payload => emit('open', payload)"
  @edit="handleEdit"
  @delete="handleDelete"
/>
<div v-if="unboundLogSources.length === 0 && !error" class="empty">还没有监听任务</div>
```

- [ ] **Step 3: 类型检查**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit
```

Expected: 无错误

- [ ] **Step 4: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add desktop/src/components/Sidebar/RemoteListenSection.vue
git commit -m "feat(RemoteListenSection): hide project-bound logSources from standalone section"
```

---

## Task 10: 端到端验证

- [ ] **Step 1: 编译前端**

```bash
cd /Users/xushixin/workspace/super-debug/desktop && npx tsc --noEmit && npx vitest run
```

Expected: 全部 PASS

- [ ] **Step 2: 编译后端**

```bash
cd /Users/xushixin/workspace/super-debug/agent && go test ./...
```

Expected: 全部 PASS

- [ ] **Step 3: 手动验证流程**

启动应用，按以下步骤验证：

1. 进入"远程监听" → 新建监听任务
2. 确认表单有"绑定项目"和"绑定服务"下拉
3. 选择一个本地项目和服务后保存
4. 观察侧边栏：该任务**不再出现**在底部"远程监听"区块
5. 对应项目下出现"远程监听"子区块，显示绑定的服务名和分组
6. 点击分组，确认打开聚合日志面板（tab 标题为 `服务名 · groupKey`）
7. 再新建一个任务绑定同一服务，确认两个任务的节点在侧边栏合并显示
8. 新建一个不绑定项目的任务，确认它出现在底部独立区块

- [ ] **Step 4: 最终提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add -A
git commit -m "feat: remote LogSource project/service binding complete"
```
