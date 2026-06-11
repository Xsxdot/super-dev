<!--
RuntimeWorkbenchHeader：运行态工作区顶部状态栏。

职责：
  - 展示当前 runtime 上下文、打开的 deployment 数、Evidence 同步状态和 panel 数
  - 提供工作区级布局动作入口的视觉位置

边界：
  - 不执行部署启停、日志订阅或 Evidence 导出
  - 不改变 panel 布局树，仅展示布局动作入口
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { AgentAPIError, api, type BrowserSession } from '@/api/agent'
import { MAX_PANEL_LEAVES, usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useWorkspaceStore } from '@/stores/workspace'

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()
const operationApprovalStore = useOperationApprovalStore()
const workspace = useWorkspaceStore()
const { t } = useI18n()
const appWindow = getCurrentWindow()
const browserSession = ref<BrowserSession | null>(null)
const browserError = ref<string | null>(null)

const openDeploymentIds = computed(() => {
  const ids = new Set<string>()
  for (const leaf of panelStore.allLeaves) {
    const deploymentId = leaf.source?.type === 'deployment' ? leaf.source.deploymentId : leaf.serviceId
    if (deploymentId) ids.add(deploymentId)
  }
  return [...ids]
})

const primaryDeploymentInfo = computed(() => {
  const tab = workspace.activeTab
  if (tab?.type === 'deployment') return agentStore.serviceForDeployment(tab.deploymentId)
  const firstDeploymentId = openDeploymentIds.value[0]
  return firstDeploymentId ? agentStore.serviceForDeployment(firstDeploymentId) : undefined
})

const runtimeLabel = computed(() => {
  const info = primaryDeploymentInfo.value
  if (info) return info.envName
  const tab = workspace.activeTab
  if (tab?.type === 'project') return agentStore.projectById(tab.projectId)?.name ?? tab.title
  if (tab?.type === 'deployment') return tab.title
  return ''
})

const evidenceLabel = computed(() => {
  if (bookmarkStore.syncStatus === 'recording') return t('runtimeWorkbench.evidenceRecording')
  if (bookmarkStore.syncStatus === 'captured') return t('runtimeWorkbench.evidenceCaptured')
  if (bookmarkStore.syncStatus === 'ready') return t('runtimeWorkbench.evidenceReady')
  return t('runtimeWorkbench.evidenceOff')
})

const maximizeLabel = computed(() =>
  workspace.isRuntimeWorkspaceMaximized
    ? t('runtimeWorkbench.restore')
    : t('runtimeWorkbench.maximize'),
)

function persistActiveLayout() {
  workspace.saveActiveLogWorkspaceLayout()
}

function balancePanels() {
  panelStore.balanceSplits()
  persistActiveLayout()
}

function arrangeColumns() {
  panelStore.arrangeLeavesInColumns()
  persistActiveLayout()
}

async function openBrowserDebug() {
  const deploymentId = primaryDeploymentInfo.value?.deployment.id
  if (!deploymentId) return
  browserError.value = null
  browserSession.value = null
  try {
    browserSession.value = await api.openBrowserSession({ deployment_id: deploymentId, open_devtools: true }, undefined)
  } catch (error) {
    const captured = await operationApprovalStore.captureApprovalRequired(error)
    browserError.value = captured
      ? t('runtimeWorkbench.browserDebugApprovalRequired')
      : browserDebugErrorMessage(error)
  }
}

function browserDebugErrorMessage(error: unknown): string {
  if (error instanceof AgentAPIError) {
    switch (error.code) {
      case 'debug_browser_not_configured':
        return t('runtimeWorkbench.browserDebugConfigureBrowser')
      case 'browser_executable_unavailable':
        return t('runtimeWorkbench.browserDebugChoosePath')
      case 'web_entrypoint_not_ready':
        return t('runtimeWorkbench.browserDebugServiceNotReady')
      case 'browser_cdp_connection_failed':
        return t('runtimeWorkbench.browserDebugCDPFailed')
    }
  }
  return error instanceof Error ? error.message : String(error)
}

function startWindowDrag(event: MouseEvent) {
  if (event.buttons !== 1) return
  void appWindow.startDragging().catch(() => undefined)
}
</script>

<template>
  <header class="runtime-workbench-header" data-test="runtime-workbench-header">
    <div
      class="runtime-context"
      data-test="runtime-drag-region"
      data-tauri-drag-region="deep"
      @mousedown="startWindowDrag"
    >
      <h1 data-test="runtime-title" data-tauri-drag-region>
        {{ t('runtimeWorkbench.title') }} <span data-tauri-drag-region>· {{ runtimeLabel }}</span>
      </h1>
      <span class="status-chip live" data-test="runtime-live" data-tauri-drag-region>
        <span class="dot" data-tauri-drag-region />
        {{ t('runtimeWorkbench.live') }}
      </span>
      <span class="status-chip" data-test="runtime-deployments" data-tauri-drag-region>
        {{ t('runtimeWorkbench.deploymentCount', { count: openDeploymentIds.length }) }}
      </span>
      <span class="status-chip evidence" data-test="runtime-evidence" data-tauri-drag-region>
        {{ evidenceLabel }}
      </span>
    </div>
    <div
      class="runtime-drag-spacer"
      data-test="runtime-drag-spacer"
      data-tauri-drag-region
      aria-hidden="true"
      @mousedown="startWindowDrag"
    />
    <div class="runtime-layout-actions">
      <span
        class="panel-count"
        data-test="runtime-panel-count"
        data-tauri-drag-region
        @mousedown="startWindowDrag"
      >
        {{ t('runtimeWorkbench.panelCount', { open: panelStore.allLeaves.length, max: MAX_PANEL_LEAVES }) }}
      </span>
      <template v-if="browserSession">
        <span class="status-chip browser-session" data-test="browser-debug-session">
          {{ browserSession.session_id }}
        </span>
        <a class="status-chip browser-session browser-link" data-test="browser-debug-target" :href="browserSession.target_url">
          {{ browserSession.target_url }}
        </a>
        <a class="status-chip browser-session browser-link" data-test="browser-debug-devtools" :href="browserSession.devtools_url">
          DevTools
        </a>
      </template>
      <span v-else-if="browserError" class="status-chip browser-error" data-test="browser-debug-error">
        {{ browserError }}
      </span>
      <button
        type="button"
        class="layout-btn"
        data-test="open-browser-debug"
        :title="t('runtimeWorkbench.openBrowserDebug')"
        :aria-label="t('runtimeWorkbench.openBrowserDebug')"
        :disabled="!primaryDeploymentInfo"
        @click="openBrowserDebug"
      >
        <Icon icon="lucide:bug" aria-hidden="true" />
      </button>
      <button
        type="button"
        class="layout-btn"
        data-test="layout-balance"
        :title="t('runtimeWorkbench.layoutBalanced')"
        :aria-label="t('runtimeWorkbench.layoutBalanced')"
        @click="balancePanels"
      >
        ▣
      </button>
      <button
        type="button"
        class="layout-btn"
        data-test="layout-columns"
        :title="t('runtimeWorkbench.layoutColumns')"
        :aria-label="t('runtimeWorkbench.layoutColumns')"
        @click="arrangeColumns"
      >
        ▥
      </button>
      <button
        type="button"
        class="layout-btn"
        data-test="layout-maximize"
        :class="{ active: workspace.isRuntimeWorkspaceMaximized }"
        :title="maximizeLabel"
        :aria-label="maximizeLabel"
        @click="workspace.toggleRuntimeWorkspaceMaximized"
      >
        {{ workspace.isRuntimeWorkspaceMaximized ? '↙' : '↗' }}
      </button>
    </div>
  </header>
</template>

<style scoped>
.runtime-workbench-header {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 48px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border-secondary);
  background: linear-gradient(180deg, rgba(13, 27, 35, 0.96), rgba(11, 22, 30, 0.96));
  flex-shrink: 0;
}

.runtime-context,
.runtime-layout-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.runtime-context {
  flex: 0 1 auto;
}

.runtime-drag-spacer {
  align-self: stretch;
  min-width: 24px;
  flex: 1 1 24px;
}

.runtime-layout-actions {
  flex-shrink: 0;
}

.runtime-context h1 {
  margin: 0 6px 0 0;
  color: var(--text-primary);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.2;
  white-space: nowrap;
}

.runtime-context h1 span {
  color: var(--text-secondary);
  font-weight: 500;
}

.status-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 9px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}

.status-chip.live {
  border-color: rgba(63, 185, 80, 0.28);
  background: rgba(63, 185, 80, 0.08);
  color: #7ce38b;
}

.status-chip.evidence {
  border-color: rgba(88, 166, 255, 0.28);
  background: rgba(88, 166, 255, 0.08);
  color: #58a6ff;
}

.status-chip.browser-session {
  border-color: rgba(63, 185, 80, 0.28);
  background: rgba(63, 185, 80, 0.08);
  color: #7ce38b;
}

.status-chip.browser-link {
  max-width: 220px;
  overflow: hidden;
  text-decoration: none;
  text-overflow: ellipsis;
}

.status-chip.browser-error {
  max-width: 220px;
  overflow: hidden;
  border-color: rgba(248, 81, 73, 0.32);
  background: rgba(248, 81, 73, 0.08);
  color: #ff7b72;
  text-overflow: ellipsis;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: #3fb950;
}

.panel-count {
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}

.layout-btn {
  width: 30px;
  height: 30px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
}

.layout-btn :deep(svg) {
  width: 15px;
  height: 15px;
}

.layout-btn:hover {
  border-color: rgba(88, 166, 255, 0.45);
  color: var(--text-primary);
}

.layout-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.layout-btn.active {
  border-color: rgba(88, 166, 255, 0.45);
  background: rgba(88, 166, 255, 0.1);
  color: #58a6ff;
}

@media (max-width: 980px) {
  .status-chip {
    padding: 0 7px;
    font-size: 11px;
  }

  .runtime-context h1 {
    font-size: 14px;
  }
}
</style>
