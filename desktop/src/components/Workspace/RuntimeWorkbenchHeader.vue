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
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MAX_PANEL_LEAVES, usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { useWorkspaceStore } from '@/stores/workspace'

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()
const workspace = useWorkspaceStore()
const { t } = useI18n()

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
</script>

<template>
  <header class="runtime-workbench-header" data-test="runtime-workbench-header">
    <div class="runtime-context">
      <h1 data-test="runtime-title">
        {{ t('runtimeWorkbench.title') }} <span>· {{ runtimeLabel }}</span>
      </h1>
      <span class="status-chip live" data-test="runtime-live">
        <span class="dot" />
        {{ t('runtimeWorkbench.live') }}
      </span>
      <span class="status-chip" data-test="runtime-deployments">
        {{ t('runtimeWorkbench.deploymentCount', { count: openDeploymentIds.length }) }}
      </span>
      <span class="status-chip evidence" data-test="runtime-evidence">
        {{ evidenceLabel }}
      </span>
    </div>
    <div class="runtime-layout-actions">
      <span class="panel-count" data-test="runtime-panel-count">
        {{ t('runtimeWorkbench.panelCount', { open: panelStore.allLeaves.length, max: MAX_PANEL_LEAVES }) }}
      </span>
      <button type="button" class="layout-btn" :title="t('runtimeWorkbench.layoutBalanced')" aria-label="Balance panels">
        ▣
      </button>
      <button type="button" class="layout-btn" :title="t('runtimeWorkbench.layoutColumns')" aria-label="Column layout">
        ▥
      </button>
      <button type="button" class="layout-btn" :title="t('runtimeWorkbench.maximize')" aria-label="Maximize workspace">
        ↗
      </button>
    </div>
  </header>
</template>

<style scoped>
.runtime-workbench-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
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

.layout-btn:hover {
  border-color: rgba(88, 166, 255, 0.45);
  color: var(--text-primary);
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
