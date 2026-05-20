# Tauri 菜单栏 Popover 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Tauri Desktop 版本中实现菜单栏 Popover，单击托盘图标弹出服务控制面板（精确定位在图标下方），右键弹出菜单（设置/退出），失焦自动关闭。

**Architecture:** 新建无边框 `WebviewWindow`（label: `popover`），加载同一 Vue 应用的 `/popover` 路由。Rust 侧通过 `tray.rect()` 获取图标坐标精确定位窗口，Vue 侧独立轮询 agentStore，复用已有 CSS 变量和 API 层。

**Tech Stack:** Tauri 2、Vue 3、Pinia、TypeScript、Rust

---

## 文件结构

| 文件 | 操作 | 职责 |
|------|------|------|
| `desktop/src/main.ts` | 修改 | 引入 vue-router，按路由挂载不同根组件 |
| `desktop/src/router/index.ts` | 新建 | 定义 `/`（主窗口）和 `/popover` 路由 |
| `desktop/src/pages/MainPage.vue` | 新建 | 把现有 App.vue 主窗口内容提取为页面组件 |
| `desktop/src/pages/PopoverPage.vue` | 新建 | Popover 根组件，双栏布局，管理轮询生命周期 |
| `desktop/src/components/Popover/PopoverProjectList.vue` | 新建 | 左栏：项目列表 + 搜索框，悬停高亮触发右栏 |
| `desktop/src/components/Popover/PopoverServicePanel.vue` | 新建 | 右栏：项目 header（启动选中/全停）+ 服务列表 + footer（查看日志） |
| `desktop/src/components/Popover/PopoverServiceRow.vue` | 新建 | 服务行：checkbox + 状态点 + 名称 + 启停/重启按钮 |
| `desktop/src/App.vue` | 修改 | 改为渲染 `<RouterView />`，移除原有布局 |
| `desktop/src-tauri/src/main.rs` | 修改 | 托盘左键创建/定位/toggle popover 窗口，右键菜单 |
| `desktop/src-tauri/capabilities/default.json` | 修改 | windows 数组加入 `"popover"` |

---

## Task 1：引入 vue-router，重构路由入口

**Files:**
- Modify: `desktop/src/main.ts`
- Create: `desktop/src/router/index.ts`
- Create: `desktop/src/pages/MainPage.vue`
- Modify: `desktop/src/App.vue`

- [ ] **Step 1: 安装 vue-router**

```bash
cd desktop && pnpm add vue-router@4
```

Expected: `package.json` 中出现 `"vue-router": "^4.x.x"`

- [ ] **Step 2: 新建 `desktop/src/router/index.ts`**

```typescript
import { createRouter, createWebHashHistory } from 'vue-router'
import MainPage from '@/pages/MainPage.vue'
import PopoverPage from '@/pages/PopoverPage.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: MainPage },
    { path: '/popover', component: PopoverPage },
  ],
})

export default router
```

- [ ] **Step 3: 新建 `desktop/src/pages/MainPage.vue`**

把现有 `App.vue` 的全部内容移入此文件（不做任何逻辑改动）：

```vue
<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import PanelLayout from '@/components/Panel/PanelLayout.vue'
import BottomBar from '@/components/BottomBar.vue'

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

- [ ] **Step 4: 修改 `desktop/src/App.vue`**

App.vue 改为只渲染 RouterView：

```vue
<template>
  <RouterView />
</template>
```

- [ ] **Step 5: 修改 `desktop/src/main.ts`**

```typescript
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import App from './App.vue'
import './style.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
```

- [ ] **Step 6: 验证主窗口正常**

```bash
cd desktop && pnpm dev
```

打开 http://localhost:6688，确认主窗口布局与之前完全一致（侧边栏 + 面板 + 底栏）。

- [ ] **Step 7: 提交**

```bash
git add desktop/src/main.ts desktop/src/router/index.ts desktop/src/pages/MainPage.vue desktop/src/App.vue desktop/package.json desktop/pnpm-lock.yaml
git commit -m "feat(desktop): 引入 vue-router，拆分主页面为 MainPage"
```

---

## Task 2：PopoverServiceRow 组件

**Files:**
- Create: `desktop/src/components/Popover/PopoverServiceRow.vue`

PopoverServiceRow 在 Popover 右栏的服务列表中使用，职责：checkbox 勾选（required 不可取消）、状态点、名称、悬停显示重启/启停按钮。

- [ ] **Step 1: 新建 `desktop/src/components/Popover/PopoverServiceRow.vue`**

```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Service } from '@/api/agent'

const props = defineProps<{
  service: Service
  projectId: string
}>()

const agentStore = useAgentStore()
const hovered = ref(false)

const isActive = computed(() =>
  props.service.status === 'running' || props.service.status === 'starting'
)

const statusColor = computed(() => {
  if (props.service.status === 'running') return '#3fb950'
  if (props.service.status === 'starting') return '#d29922'
  if (props.service.status === 'failed') return '#f85149'
  return '#6e7681'
})

const isChecked = computed(() => {
  if (props.service.required) return true
  const project = agentStore.projectById(props.projectId)
  return project?.selected_service_ids?.includes(props.service.name) ?? false
})

async function onCheckChange() {
  if (props.service.required) return
  const project = agentStore.projectById(props.projectId)
  if (!project) return
  const current = project.selected_service_ids ?? []
  const next = isChecked.value
    ? current.filter(n => n !== props.service.name)
    : [...current, props.service.name]
  await agentStore.updateSelected(props.projectId, next)
  await agentStore.refreshServices()
}

async function onToggle() {
  if (isActive.value) {
    await agentStore.stopService(props.service.id)
  } else {
    await agentStore.startService(props.service.id)
  }
  await agentStore.refreshServices()
}

async function onRestart() {
  await agentStore.restartService(props.service.id)
  await agentStore.refreshServices()
}
</script>

<template>
  <div
    class="popover-service-row"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
  >
    <input
      type="checkbox"
      :checked="isChecked"
      :disabled="service.required"
      @click.stop="onCheckChange"
      class="svc-checkbox"
    />
    <span class="status-dot" :style="{ background: statusColor }" />
    <span class="svc-name" :class="{ dimmed: !isActive && service.status !== '' }">
      {{ service.name }}
    </span>
    <span class="status-label" :style="{ color: statusColor }">
      {{ service.status === 'running' ? '运行中' : service.status === 'starting' ? '启动中…' : service.status === 'failed' ? '已退出' : '未启动' }}
    </span>
    <div class="row-actions" v-if="hovered">
      <button v-if="isActive" title="重启" class="btn" @click.stop="onRestart">↺</button>
      <button
        :title="isActive ? '停止' : '启动'"
        class="btn"
        :class="isActive ? 'btn-stop' : 'btn-start'"
        @click.stop="onToggle"
      >
        {{ isActive ? '⏹' : '▶' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.popover-service-row {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 12px;
  position: relative;
}
.popover-service-row:hover { background: rgba(255,255,255,0.04); }

.svc-checkbox {
  width: 12px; height: 12px;
  accent-color: var(--accent);
  flex-shrink: 0;
  cursor: pointer;
}
.svc-checkbox:disabled { opacity: 0.4; cursor: not-allowed; }

.status-dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.svc-name {
  flex: 1;
  font-size: 11px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.svc-name.dimmed { color: var(--text-tertiary); }

.status-label {
  font-size: 9px;
  white-space: nowrap;
  flex-shrink: 0;
}

.row-actions {
  display: flex;
  gap: 3px;
  flex-shrink: 0;
}
.btn {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
  line-height: 1.4;
}
.btn-stop { background: rgba(248,81,73,0.1); border-color: rgba(248,81,73,0.3); color: #f85149; }
.btn-start { background: rgba(63,185,80,0.1); border-color: rgba(63,185,80,0.3); color: #3fb950; }
</style>
```

- [ ] **Step 2: 提交**

```bash
git add desktop/src/components/Popover/PopoverServiceRow.vue
git commit -m "feat(desktop): PopoverServiceRow 组件"
```

---

## Task 3：PopoverProjectList 组件（左栏）

**Files:**
- Create: `desktop/src/components/Popover/PopoverProjectList.vue`

左栏：搜索框 + 项目分组 + 服务行（只显示名称+状态点），悬停项目时通过 emit 通知父组件切换右栏。

- [ ] **Step 1: 新建 `desktop/src/components/Popover/PopoverProjectList.vue`**

```vue
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'

const emit = defineEmits<{
  hover: [project: Project | null]
}>()

const agentStore = useAgentStore()
const searchText = ref('')
const hoveredProjectId = ref<string | null>(null)

function filteredServices(project: Project) {
  if (!searchText.value) return project.services
  return project.services.filter(s =>
    s.name.toLowerCase().includes(searchText.value.toLowerCase())
  )
}

function projectStatusColor(project: Project) {
  const services = project.services
  if (services.some(s => s.status === 'failed')) return '#f85149'
  if (services.some(s => s.status === 'running')) return '#3fb950'
  if (services.some(s => s.status === 'starting')) return '#d29922'
  return '#6e7681'
}

function serviceStatusColor(status: string) {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function onProjectHover(project: Project) {
  hoveredProjectId.value = project.id
  emit('hover', project)
}

function onLeave() {
  // 不在这里清除悬停，由父组件 PopoverPage 控制（防止移到右栏时闪烁）
}
</script>

<template>
  <div class="project-list">
    <!-- 搜索栏 -->
    <div class="search-bar">
      <span class="search-icon">⌕</span>
      <input
        v-model="searchText"
        placeholder="搜索服务…"
        class="search-input"
      />
    </div>

    <div class="divider" />

    <!-- 项目列表 -->
    <div class="list-scroll">
      <template v-for="project in agentStore.projects" :key="project.id">
        <div
          class="project-section"
          @mouseenter="onProjectHover(project)"
          @mouseleave="onLeave"
        >
          <!-- 项目 label -->
          <div class="project-label">
            <span class="project-name">{{ project.name.toUpperCase() }}</span>
            <span
              class="project-dot"
              :style="{ background: projectStatusColor(project) }"
            />
          </div>
          <!-- 服务行（左栏简化版：只有状态点 + 名称） -->
          <div
            v-for="svc in filteredServices(project)"
            :key="svc.id"
            class="left-service-row"
            :class="{ 'row-hovered': hoveredProjectId === project.id }"
          >
            <span
              class="status-dot"
              :style="{ background: serviceStatusColor(svc.status) }"
            />
            <span class="svc-name">{{ svc.name }}</span>
          </div>
        </div>
      </template>
    </div>

    <div class="divider" />

    <!-- 未连接提示 -->
    <div v-if="!agentStore.connected" class="disconnected">未连接</div>
  </div>
</template>

<style scoped>
.project-list {
  width: 170px;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  flex-shrink: 0;
}

.search-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 10px;
  padding: 5px 9px;
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
}
.search-icon { font-size: 12px; color: var(--text-tertiary); }
.search-input {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  font-size: 10px;
  color: var(--text-secondary);
}
.search-input::placeholder { color: var(--text-tertiary); }

.divider { height: 1px; background: var(--border); flex-shrink: 0; }

.list-scroll { flex: 1; overflow-y: auto; }

.project-section { cursor: default; }

.project-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px 3px;
}
.project-name {
  font-size: 9px;
  font-weight: 600;
  color: var(--text-tertiary);
  letter-spacing: 0.08em;
}
.project-dot {
  width: 6px; height: 6px;
  border-radius: 50%;
}

.left-service-row {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 10px;
  border-left: 2px solid transparent;
}
.left-service-row.row-hovered {
  background: var(--bg-elevated);
  border-left-color: var(--accent);
}

.status-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.svc-name { font-size: 11px; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-hovered .svc-name { color: var(--text-primary); }

.disconnected {
  padding: 8px 10px;
  font-size: 10px;
  color: var(--text-tertiary);
  text-align: center;
}
</style>
```

- [ ] **Step 2: 提交**

```bash
git add desktop/src/components/Popover/PopoverProjectList.vue
git commit -m "feat(desktop): PopoverProjectList 左栏组件"
```

---

## Task 4：PopoverServicePanel 组件（右栏）

**Files:**
- Create: `desktop/src/components/Popover/PopoverServicePanel.vue`

右栏：项目名称 + 状态徽章 + 全选/反选工具栏 + 服务列表（必须/可选分组）+ footer（查看日志按钮）。

- [ ] **Step 1: 新建 `desktop/src/components/Popover/PopoverServicePanel.vue`**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'
import PopoverServiceRow from './PopoverServiceRow.vue'

const props = defineProps<{ project: Project }>()
const agentStore = useAgentStore()

const requiredServices = computed(() =>
  props.project.services.filter(s => s.required)
)
const optionalServices = computed(() =>
  props.project.services.filter(s => !s.required)
)

const runningCount = computed(() =>
  props.project.services.filter(s => s.status === 'running').length
)
const startingCount = computed(() =>
  props.project.services.filter(s => s.status === 'starting').length
)
const stoppedCount = computed(() =>
  props.project.services.filter(s => s.status !== 'running' && s.status !== 'starting').length
)

const selectedNames = computed(() => props.project.selected_service_ids ?? [])

const allOptionalSelected = computed(() =>
  optionalServices.value.every(s => selectedNames.value.includes(s.name))
)
const someSelected = computed(() =>
  optionalServices.value.some(s => selectedNames.value.includes(s.name))
)

async function toggleSelectAll() {
  const requiredNames = requiredServices.value.map(s => s.name)
  if (allOptionalSelected.value) {
    await agentStore.updateSelected(props.project.id, requiredNames)
  } else {
    const all = props.project.services.map(s => s.name)
    await agentStore.updateSelected(props.project.id, all)
  }
  await agentStore.refreshServices()
}

async function invertSelection() {
  const requiredNames = requiredServices.value.map(s => s.name)
  const optionalNames = optionalServices.value.map(s => s.name)
  const currentOptionalSelected = optionalNames.filter(n => selectedNames.value.includes(n))
  const inverted = optionalNames.filter(n => !currentOptionalSelected.includes(n))
  await agentStore.updateSelected(props.project.id, [...requiredNames, ...inverted])
  await agentStore.refreshServices()
}

async function startSelected() {
  await agentStore.startSelected(props.project.id)
  await agentStore.refreshServices()
}

async function stopAll() {
  const active = props.project.services.filter(
    s => s.status === 'running' || s.status === 'starting'
  )
  await Promise.all(active.map(s => agentStore.stopService(s.id)))
  await agentStore.refreshServices()
}

// 打开主窗口查看日志（发送 Tauri event）
async function openMainWindow() {
  const { invoke } = await import('@tauri-apps/api/core')
  await invoke('show_main_window')
}
</script>

<template>
  <div class="service-panel">
    <!-- Header -->
    <div class="panel-header">
      <div class="header-top">
        <span class="proj-name">{{ project.name }}</span>
        <div class="header-actions">
          <button class="btn btn-secondary" @click="stopAll">全停</button>
          <button class="btn btn-primary" @click="startSelected">▶ 启动选中</button>
        </div>
      </div>
      <div class="status-badges">
        <span v-if="runningCount > 0" class="badge running">● {{ runningCount }} 运行中</span>
        <span v-if="startingCount > 0" class="badge starting">● {{ startingCount }} 启动中</span>
        <span v-if="stoppedCount > 0" class="badge stopped">● {{ stoppedCount }} 停止</span>
      </div>
    </div>

    <div class="divider" />

    <!-- 工具栏 -->
    <div class="toolbar">
      <button class="toolbar-btn" @click="toggleSelectAll">
        <span class="checkbox-glyph" :class="{ checked: allOptionalSelected, partial: !allOptionalSelected && someSelected }">
          <span v-if="allOptionalSelected">✓</span>
          <span v-else-if="someSelected">—</span>
        </span>
        全选
      </button>
      <span class="toolbar-divider" />
      <button class="toolbar-btn" @click="invertSelection">反选</button>
    </div>

    <div class="toolbar-separator" />

    <!-- 服务列表 -->
    <div class="service-list">
      <template v-if="requiredServices.length > 0">
        <div class="group-label">必须启动</div>
        <PopoverServiceRow
          v-for="svc in requiredServices"
          :key="svc.id"
          :service="svc"
          :projectId="project.id"
        />
      </template>
      <template v-if="optionalServices.length > 0">
        <div class="group-label">可选</div>
        <PopoverServiceRow
          v-for="svc in optionalServices"
          :key="svc.id"
          :service="svc"
          :projectId="project.id"
        />
      </template>
    </div>

    <div class="divider" />

    <!-- Footer -->
    <div class="panel-footer">
      <button class="footer-btn" @click="openMainWindow">
        ≡ 查看日志
      </button>
    </div>
  </div>
</template>

<style scoped>
.service-panel {
  width: 260px;
  display: flex;
  flex-direction: column;
  background: var(--bg-elevated);
  flex-shrink: 0;
}

.panel-header { padding: 9px 12px; }
.header-top { display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px; }
.proj-name { font-size: 13px; font-weight: 600; color: var(--text-primary); }

.header-actions { display: flex; gap: 5px; }
.btn {
  border-radius: 5px;
  padding: 3px 9px;
  font-size: 10px;
  cursor: pointer;
  border: none;
}
.btn-secondary {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  color: var(--text-secondary);
}
.btn-primary {
  background: var(--accent);
  color: #fff;
  font-weight: 500;
}

.status-badges { display: flex; gap: 5px; flex-wrap: wrap; }
.badge {
  font-size: 9px;
  padding: 1px 7px;
  border-radius: 4px;
}
.badge.running { color: #3fb950; background: rgba(63,185,80,0.1); border: 1px solid rgba(63,185,80,0.2); }
.badge.starting { color: #d29922; background: rgba(210,153,34,0.1); border: 1px solid rgba(210,153,34,0.2); }
.badge.stopped { color: var(--text-tertiary); background: rgba(110,118,129,0.1); border: 1px solid rgba(110,118,129,0.2); }

.divider { height: 1px; background: var(--border); flex-shrink: 0; }

.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 12px;
  background: var(--bg-overlay);
}
.toolbar-btn {
  display: flex; align-items: center; gap: 6px;
  background: transparent; border: none;
  font-size: 10px; color: var(--text-secondary);
  cursor: pointer; padding: 0;
}
.toolbar-divider { width: 1px; height: 12px; background: var(--border); }

.checkbox-glyph {
  width: 13px; height: 13px;
  border-radius: 2px;
  border: 1px solid var(--border);
  background: var(--bg-elevated);
  display: inline-flex; align-items: center; justify-content: center;
  font-size: 9px; font-weight: bold;
  color: var(--text-primary);
}
.checkbox-glyph.checked, .checkbox-glyph.partial {
  border-color: var(--accent);
  background: var(--accent);
  color: #fff;
}

.toolbar-separator { height: 1px; background: var(--bg-elevated); flex-shrink: 0; }

.service-list { flex: 1; overflow-y: auto; }

.group-label {
  font-size: 9px; font-weight: 600;
  color: var(--text-tertiary);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  padding: 8px 12px 3px;
}

.panel-footer { padding: 7px 12px; display: flex; justify-content: flex-end; }
.footer-btn {
  background: transparent; border: none;
  font-size: 10px; color: var(--accent);
  cursor: pointer;
}
</style>
```

- [ ] **Step 2: 提交**

```bash
git add desktop/src/components/Popover/PopoverServicePanel.vue
git commit -m "feat(desktop): PopoverServicePanel 右栏组件"
```

---

## Task 5：PopoverPage 根组件

**Files:**
- Create: `desktop/src/pages/PopoverPage.vue`

PopoverPage 是 `/popover` 路由的根组件，负责双栏布局、悬停状态管理、独立轮询生命周期。

- [ ] **Step 1: 新建 `desktop/src/pages/PopoverPage.vue`**

```vue
<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'
import PopoverProjectList from '@/components/Popover/PopoverProjectList.vue'
import PopoverServicePanel from '@/components/Popover/PopoverServicePanel.vue'

const agentStore = useAgentStore()
const hoveredProject = ref<Project | null>(null)

// Popover 窗口独立轮询
onMounted(() => agentStore.startPolling())
onUnmounted(() => agentStore.stopPolling())

function onProjectHover(project: Project | null) {
  hoveredProject.value = project
}
</script>

<template>
  <div
    class="popover-root"
    @mouseleave="hoveredProject = null"
  >
    <PopoverProjectList @hover="onProjectHover" />
    <div v-if="hoveredProject" class="panel-divider" />
    <PopoverServicePanel
      v-if="hoveredProject"
      :project="hoveredProject"
    />
  </div>
</template>

<style scoped>
.popover-root {
  display: flex;
  height: 100vh;
  background: var(--bg-primary);
  overflow: hidden;
}
.panel-divider {
  width: 1px;
  background: var(--border);
  flex-shrink: 0;
}
</style>
```

- [ ] **Step 2: 验证路由**

Dev server 运行中，浏览器访问 http://localhost:6688/#/popover，确认：
- 左栏项目列表可见
- 悬停项目后右栏出现
- 搜索框可输入过滤

- [ ] **Step 3: 提交**

```bash
git add desktop/src/pages/PopoverPage.vue
git commit -m "feat(desktop): PopoverPage 根组件，双栏布局"
```

---

## Task 6：Rust 侧 show_main_window 命令 + capabilities

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`
- Modify: `desktop/src-tauri/capabilities/default.json`

先加入 `show_main_window` Tauri command（供 Vue 调用），并更新 capabilities 将 `"popover"` 窗口纳入权限范围。

- [ ] **Step 1: 在 `main.rs` 添加 command**

在 `main.rs` 顶部 `mod agent;` 下方添加：

```rust
#[tauri::command]
fn show_main_window(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.set_focus();
    }
}
```

在 `tauri::Builder::default()` 链中，`.setup(|app| {` 之前加入：

```rust
.invoke_handler(tauri::generate_handler![show_main_window])
```

即完整结构为：

```rust
tauri::Builder::default()
    .plugin(tauri_plugin_dialog::init())
    .plugin(tauri_plugin_fs::init())
    .plugin(tauri_plugin_shell::init())
    .invoke_handler(tauri::generate_handler![show_main_window])
    .setup(|app| {
        // ... 原有内容不变
```

- [ ] **Step 2: 更新 `capabilities/default.json`**

将 `"windows"` 数组改为同时包含 `"main"` 和 `"popover"`：

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "Default capabilities",
  "windows": ["main", "popover"],
  "permissions": [
    "core:default",
    "dialog:default",
    "fs:default",
    "shell:default",
    {
      "identifier": "shell:allow-execute",
      "allow": [
        {
          "name": "superdev-agent",
          "sidecar": true,
          "args": ["--addr", { "validator": "^127\\.0\\.0\\.1:\\d+$" }]
        }
      ]
    }
  ]
}
```

- [ ] **Step 3: 提交**

```bash
git add desktop/src-tauri/src/main.rs desktop/src-tauri/capabilities/default.json
git commit -m "feat(desktop): show_main_window command，capabilities 加入 popover 窗口"
```

---

## Task 7：Rust 托盘事件——创建、定位、toggle Popover 窗口

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`

这是核心 Rust 任务：左键单击托盘 → 创建（或 toggle）无边框 popover 窗口，精确定位在图标下方；右键单击 → 弹出菜单（设置 / 退出）；popover 失焦 → 隐藏。

- [ ] **Step 1: 添加必要 use 声明**

在 `main.rs` 顶部现有 use 块中补充：

```rust
use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager,
    WebviewUrl, WebviewWindowBuilder,
    PhysicalPosition, PhysicalSize,
};
```

> 注意：`MouseButtonState` 用于区分按下/抬起，避免在 press 和 release 都触发。

- [ ] **Step 2: 抽取 `toggle_popover` 函数**

在 `show_main_window` command 下方添加：

```rust
fn toggle_popover(app: &tauri::AppHandle, tray_rect: Option<tauri::tray::TrayIconRect>) {
    // 若已存在且可见则隐藏（toggle）
    if let Some(w) = app.get_webview_window("popover") {
        if w.is_visible().unwrap_or(false) {
            let _ = w.hide();
            return;
        }
        // 已存在但隐藏：重新定位后显示
        position_and_show_popover(&w, tray_rect);
        return;
    }

    // 首次创建
    let popover_width: u32 = 440;
    let popover_height: u32 = 420;

    let win = WebviewWindowBuilder::new(
        app,
        "popover",
        WebviewUrl::App("index.html#/popover".into()),
    )
    .title("")
    .inner_size(popover_width as f64, popover_height as f64)
    .decorations(false)
    .always_on_top(true)
    .skip_taskbar(true)
    .visible(false)
    .build();

    match win {
        Ok(w) => {
            position_and_show_popover(&w, tray_rect);
        }
        Err(e) => eprintln!("[SuperDev] 创建 popover 窗口失败: {e}"),
    }
}

fn position_and_show_popover(
    window: &tauri::WebviewWindow,
    tray_rect: Option<tauri::tray::TrayIconRect>,
) {
    let popover_width: i32 = 440;
    let popover_height: i32 = 420;

    let (x, y) = if let Some(rect) = tray_rect {
        // 图标中心对齐，显示在图标正下方 8pt
        let cx = (rect.position.x + rect.size.width / 2.0) as i32;
        let bott = (rect.position.y + rect.size.height) as i32;
        let mut wx = cx - popover_width / 2;
        let mut wy = bott + 8;

        // 屏幕边界保护：若超出底部，显示在图标上方
        if let Ok(monitor) = window.current_monitor() {
            if let Some(m) = monitor {
                let screen_h = m.size().height as i32;
                let screen_w = m.size().width as i32;
                if wy + popover_height > screen_h {
                    wy = (rect.position.y as i32) - popover_height - 8;
                }
                // 防止超出右边
                if wx + popover_width > screen_w {
                    wx = screen_w - popover_width - 4;
                }
                if wx < 0 { wx = 4; }
            }
        }
        (wx, wy)
    } else {
        // fallback：右上角
        if let Ok(Some(m)) = window.current_monitor() {
            let sw = m.size().width as i32;
            (sw - popover_width - 4, 30)
        } else {
            (800, 30)
        }
    };

    let _ = window.set_position(PhysicalPosition::new(x, y));
    let _ = window.show();
    let _ = window.set_focus();
}
```

- [ ] **Step 3: 更新 `setup` 中的托盘事件处理**

把现有的 `TrayIconBuilder` 代码替换为：

```rust
let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, None::<&str>)?;
let menu = Menu::with_items(app, &[&show, &quit])?;

TrayIconBuilder::new()
    .icon(
        app.default_window_icon()
            .ok_or("未配置默认窗口图标")?
            .clone(),
    )
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
        match event {
            // 左键抬起 → toggle popover
            TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } => {
                let rect = tray.rect();
                toggle_popover(tray.app_handle(), rect);
            }
            // 右键抬起 → 菜单已由 .menu() 自动处理，无需手动
            _ => {}
        }
    })
    .build(app)?;
```

- [ ] **Step 4: 更新 `on_window_event`，popover 失焦时隐藏**

把现有的 `on_window_event` 替换为：

```rust
.on_window_event(|window, event| match event {
    tauri::WindowEvent::CloseRequested { api, .. } => {
        // 主窗口和 popover 关闭时均隐藏到托盘
        api.prevent_close();
        let _ = window.hide();
    }
    tauri::WindowEvent::Focused(false) => {
        // popover 失焦时自动隐藏
        if window.label() == "popover" {
            let _ = window.hide();
        }
    }
    _ => {}
})
```

- [ ] **Step 5: 编译验证**

```bash
cd desktop && cargo build --manifest-path src-tauri/Cargo.toml 2>&1 | tail -20
```

Expected: 编译成功，无 error（warning 可忽略）。

- [ ] **Step 6: 提交**

```bash
git add desktop/src-tauri/src/main.rs
git commit -m "feat(desktop): 托盘左键 toggle popover 窗口，精确贴图标定位，失焦关闭"
```

---

## Task 8：端到端验证

- [ ] **Step 1: 启动开发模式**

```bash
cd desktop && pnpm tauri dev
```

- [ ] **Step 2: 验证左键 toggle**

单击菜单栏图标 → Popover 窗口出现在图标正下方，居中对齐。  
再次单击 → Popover 消失。

- [ ] **Step 3: 验证右键菜单**

右键点击托盘图标 → 弹出"显示主窗口 / 退出 SuperDev"菜单，不弹 Popover。

- [ ] **Step 4: 验证失焦关闭**

打开 Popover 后，点击 Popover 之外的区域 → Popover 自动关闭。

- [ ] **Step 5: 验证服务操作**

悬停左栏项目 → 右栏出现该项目服务列表。  
点击服务行的启动/停止按钮 → 状态点实时更新。  
修改 checkbox 勾选 → 重新打开 Popover 后勾选状态保持。

- [ ] **Step 6: 验证"查看日志"**

点击 footer 的"查看日志"→ 主窗口弹出并获得焦点。

- [ ] **Step 7: 验证屏幕边界 fallback**

（仅在有 Dock 在底部的机器上）确认 Popover 不超出屏幕。

- [ ] **Step 8: 提交最终验证记录**

```bash
git add -A
git commit -m "feat(desktop): Tauri 菜单栏 Popover 完整实现"
```
