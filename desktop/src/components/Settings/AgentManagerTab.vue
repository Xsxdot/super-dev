<!--
AgentManagerTab：设置页 Agent 连接与安装管理标签页。

职责：
  - 列出 Host Agent 的生命周期阶段、连接配置态和实时路由态
  - 提供阶段主按钮、更多动作菜单和统一配置面板入口
  - 复用 NodeRegistry 前端缓存展示最新 runtime/route

边界：
  - 不编辑 Host 身份字段
  - 不管理项目或日志状态
  - 不直接打开 deployment 运行控制
-->
<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import { AgentAPIError, agentUninstallScriptURL, type AgentDTO, type AgentHealth, type AgentRuntime } from '@/api/agent'
import { useAgentsStore } from '@/stores/agents'
import { useRemoteStore } from '@/stores/remote'
import { useNodeStore } from '@/stores/node'
import { tagColor } from '@/lib/tagColor'
import { formatRelativeAge } from '@/lib/timeDisplay'
import { agentStage, agentStageView, runtimeFor, type AgentPanelTab } from '@/lib/agentStage'
import { agentRouteRows, agentRouteSummary, transportTypeLabelKey, type AgentRouteRowStatus } from '@/lib/agentRoute'
import AgentConfigPanel from './AgentConfigPanel.vue'
import AgentBulkUpdateModal from './AgentBulkUpdateModal.vue'
import AgentUninstallModal from './AgentUninstallModal.vue'
import AgentDetachModal from './AgentDetachModal.vue'

const agentsStore = useAgentsStore()
const remoteStore = useRemoteStore()
const nodeStore = useNodeStore()
const { t } = useI18n()

const panelTarget = ref<AgentDTO | null>(null)
const panelInitialTab = ref<AgentPanelTab>('security')
const panelMode = ref<'edit' | 'create'>('edit')
const expandedRoutes = ref<Set<string>>(new Set())
const openMenuHostId = ref<string | null>(null)
const menuPosition = ref({ top: 0, left: 0 })
const menuTriggerRect = ref<{ top: number; right: number; bottom: number } | null>(null)
const checking = ref<Set<string>>(new Set())
const uninstalling = ref<Set<string>>(new Set())
const uninstallTarget = ref<AgentDTO | null>(null)
const manualUninstallTarget = ref<AgentDTO | null>(null)
const detachTarget = ref<AgentDTO | null>(null)
const detaching = ref<Set<string>>(new Set())
const error = ref<string | null>(null)
const bulkUpdateVisible = ref(false)
const actionMenuWidth = 150
const actionMenuGap = 6
const viewportMargin = 8

const routeStatusKeys: Record<AgentRouteRowStatus, string> = {
  reachable: 'settings.agents.routeStatusReachable',
  failed: 'settings.agents.routeStatusFailed',
  untested: 'settings.agents.routeStatusUntested',
}

const healthLabelKeys: Record<AgentHealth, string> = {
  unknown: 'settings.agents.healthUnknown',
  healthy: 'settings.agents.healthHealthy',
  unreachable: 'settings.agents.healthUnreachable',
  'version-mismatch': 'settings.agents.healthVersionMismatch',
  'auth-failed': 'settings.agents.healthAuthFailed',
  'pending-bootstrap': 'settings.agents.healthPendingBootstrap',
}

const sortedAgents = computed(() =>
  [...agentsStore.agents].sort((a, b) => a.host_name.localeCompare(b.host_name) || a.host_id.localeCompare(b.host_id)),
)

const availableHosts = computed(() => {
  const configured = new Set(agentsStore.agents.map(agent => agent.host_id))
  return remoteStore.hosts.filter(host => !configured.has(host.id)).sort((a, b) => a.name.localeCompare(b.name))
})

onMounted(async () => {
  document.addEventListener('click', closeMenuOnOutsideClick)
  try {
    await Promise.all([remoteStore.loadHosts(), agentsStore.loadAgents(), nodeStore.start()])
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.loadFailed')
  }
})

onUnmounted(() => {
  document.removeEventListener('click', closeMenuOnOutsideClick)
  nodeStore.stop()
})

function nodeOf(agent: AgentDTO) {
  return nodeStore.nodeOf(agent.host_id)
}

function runtimeOf(agent: AgentDTO): AgentRuntime {
  return runtimeFor(agent, nodeOf(agent))
}

function stageViewFor(agent: AgentDTO) {
  return agentStageView(agentStage(agent, nodeOf(agent)))
}

function routeSummaryFor(agent: AgentDTO) {
  return agentRouteSummary(agent, nodeOf(agent))
}

function routeRowsFor(agent: AgentDTO) {
  return agentRouteRows(agent, nodeOf(agent))
}

function hostFor(agent: AgentDTO) {
  return remoteStore.hosts.find(host => host.id === agent.host_id) ?? null
}

function routeStatusKey(status: AgentRouteRowStatus) {
  return routeStatusKeys[status]
}

function transportLabel(type?: string) {
  return t(transportTypeLabelKey(type))
}

function healthLabelKey(health: AgentHealth) {
  return healthLabelKeys[health]
}

function openPanel(agent: AgentDTO, tab: AgentPanelTab) {
  panelTarget.value = agent
  panelInitialTab.value = tab
  panelMode.value = 'edit'
  openMenuHostId.value = null
}

function openCreatePanel() {
  panelTarget.value = null
  panelInitialTab.value = 'security'
  panelMode.value = 'create'
}

function closePanel() {
  panelTarget.value = null
  panelMode.value = 'edit'
}

function openBulkUpdate() {
  bulkUpdateVisible.value = true
}

function closeBulkUpdate() {
  bulkUpdateVisible.value = false
}

function agentCreated(agent: AgentDTO) {
  panelTarget.value = agent
  panelInitialTab.value = 'install'
  panelMode.value = 'edit'
}

function toggleRoute(hostId: string) {
  const next = new Set(expandedRoutes.value)
  if (next.has(hostId)) next.delete(hostId)
  else next.add(hostId)
  expandedRoutes.value = next
}

async function toggleMenu(hostId: string, event: MouseEvent) {
  if (openMenuHostId.value === hostId) {
    openMenuHostId.value = null
    return
  }
  positionMenu(event.currentTarget)
  openMenuHostId.value = hostId
  await nextTick()
  fitMenuInViewport()
}

function closeMenuOnOutsideClick(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Element)) return
  if (target.closest('.agent-more')) return
  if (target.closest('.agent-action-menu')) return
  openMenuHostId.value = null
}

function positionMenu(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return
  const rect = target.getBoundingClientRect()
  menuTriggerRect.value = { top: rect.top, right: rect.right, bottom: rect.bottom }
  const viewportWidth = window.innerWidth || document.documentElement.clientWidth
  const maxLeft = Math.max(viewportMargin, viewportWidth - actionMenuWidth - viewportMargin)
  menuPosition.value = {
    top: rect.bottom + actionMenuGap,
    left: Math.min(Math.max(viewportMargin, rect.right - actionMenuWidth), maxLeft),
  }
}

function fitMenuInViewport() {
  if (!openMenuHostId.value || !menuTriggerRect.value) return
  const menu = document.querySelector(`[data-test="agent-menu-${openMenuHostId.value}"]`)
  if (!(menu instanceof HTMLElement)) return
  const viewportHeight = window.innerHeight || document.documentElement.clientHeight
  const menuHeight = menu.getBoundingClientRect().height
  if (menuPosition.value.top + menuHeight <= viewportHeight - viewportMargin) return
  menuPosition.value = {
    ...menuPosition.value,
    top: Math.max(viewportMargin, menuTriggerRect.value.top - menuHeight - actionMenuGap),
  }
}

function actionMenuStyle(): CSSProperties {
  return {
    top: `${menuPosition.value.top}px`,
    left: `${menuPosition.value.left}px`,
    width: `${actionMenuWidth}px`,
  }
}

function updatedLabel(agent: AgentDTO): string {
  return formatRelativeAge(
    agent.updated_at,
    count => t('settings.hosts.checkedSecondsAgo', { count }),
    count => t('settings.hosts.checkedMinutesAgo', { count }),
    count => t('settings.hosts.checkedHoursAgo', { count }),
  ) || '-'
}

async function refresh() {
  await Promise.all([remoteStore.loadHosts(), agentsStore.loadAgents()])
}

async function runPrimaryAction(agent: AgentDTO) {
  const view = stageViewFor(agent)
  if (view.opensPanel && view.panelTab) {
    openPanel(agent, view.panelTab)
    return
  }
  await checkAgent(agent)
}

async function checkAgent(agent: AgentDTO) {
  openMenuHostId.value = null
  const next = new Set(checking.value)
  next.add(agent.host_id)
  checking.value = next
  try {
    await agentsStore.checkAgent(agent.host_id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.checkFailed')
  } finally {
    const done = new Set(checking.value)
    done.delete(agent.host_id)
    checking.value = done
  }
}

function openUninstall(agent: AgentDTO) {
  openMenuHostId.value = null
  uninstallTarget.value = agent
}

async function uninstallAgent(removeData: boolean) {
  const agent = uninstallTarget.value
  if (!agent) return
  uninstallTarget.value = null
  const next = new Set(uninstalling.value)
  next.add(agent.host_id)
  uninstalling.value = next
  try {
    await agentsStore.uninstallAgent(agent.host_id, removeData)
    if (manualUninstallTarget.value?.host_id === agent.host_id) {
      manualUninstallTarget.value = null
    }
  } catch (err) {
    if (err instanceof AgentAPIError && err.stage === 'remote_uninstall') {
      error.value = t('settings.agents.remoteUninstallFailed', { message: err.message })
      // SSH 自动卸载失败时保留配置，并暴露与当前 Controller 版本匹配的离线脚本。
      manualUninstallTarget.value = agent
    } else if (err instanceof AgentAPIError && err.stage === 'config_remove') {
      error.value = t('settings.agents.configRemoveFailed', { message: err.message })
    } else {
      error.value = err instanceof Error ? err.message : t('settings.agents.uninstallFailed')
    }
  } finally {
    const done = new Set(uninstalling.value)
    done.delete(agent.host_id)
    uninstalling.value = done
  }
}

function openDetachFallback() {
  // 只有自动卸载失败并已进入手动指引的 Agent，才允许打开 Detach 风险确认。
  if (!manualUninstallTarget.value) return
  detachTarget.value = manualUninstallTarget.value
}

async function detachAgent() {
  const agent = detachTarget.value
  if (!agent) return
  const next = new Set(detaching.value)
  next.add(agent.host_id)
  detaching.value = next
  try {
    await agentsStore.detachAgent(agent.host_id, 'manual_uninstall_failed')
    detachTarget.value = null
    if (manualUninstallTarget.value?.host_id === agent.host_id) {
      manualUninstallTarget.value = null
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.detachFailed')
  } finally {
    const done = new Set(detaching.value)
    done.delete(agent.host_id)
    detaching.value = done
  }
}
</script>

<template>
  <section class="agent-manager">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.agents.title') }}</h1>
      </div>
      <div class="settings-toolbar">
        <button class="settings-btn settings-btn-primary" type="button" data-test="agent-create" :disabled="availableHosts.length === 0" @click="openCreatePanel">
          + {{ t('settings.agents.create') }}
        </button>
        <button class="settings-btn settings-btn-secondary" type="button" data-test="agent-bulk-update" :disabled="sortedAgents.length === 0" @click="openBulkUpdate">
          {{ t('settings.agents.bulkUpdate') }}
        </button>
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="agentsStore.loading" @click="refresh">
          {{ t('settings.agents.refresh') }}
        </button>
      </div>
    </header>

    <div v-if="error || agentsStore.error" class="settings-alert settings-alert-danger">{{ error || agentsStore.error }}</div>
    <div v-if="manualUninstallTarget" class="settings-alert manual-uninstall" data-test="agent-manual-uninstall">
      <strong>{{ t('settings.agents.manualUninstallTitle', { name: manualUninstallTarget.host_name }) }}</strong>
      <p>{{ t('settings.agents.manualUninstallDescription') }}</p>
      <div class="manual-uninstall-actions">
        <div class="manual-uninstall-platform">
          <a
            class="settings-btn settings-btn-secondary"
            data-test="agent-uninstall-script-shell"
            :href="agentUninstallScriptURL('uninstall-agent.sh')"
            download="uninstall-agent.sh"
          >{{ t('settings.agents.downloadShellUninstall') }}</a>
          <code>sh uninstall-agent.sh</code>
          <code>sh uninstall-agent.sh --purge</code>
        </div>
        <div class="manual-uninstall-platform">
          <a
            class="settings-btn settings-btn-secondary"
            data-test="agent-uninstall-script-powershell"
            :href="agentUninstallScriptURL('uninstall-agent.ps1')"
            download="uninstall-agent.ps1"
          >{{ t('settings.agents.downloadPowerShellUninstall') }}</a>
          <code>powershell -ExecutionPolicy Bypass -File .\uninstall-agent.ps1</code>
          <code>powershell -ExecutionPolicy Bypass -File .\uninstall-agent.ps1 -Purge</code>
        </div>
      </div>
      <div class="manual-uninstall-footer">
        <button
          class="settings-btn settings-btn-text"
          type="button"
          data-test="agent-detach-unavailable"
          :disabled="detaching.has(manualUninstallTarget.host_id)"
          @click="openDetachFallback"
        >
          {{ t('settings.agents.manualUninstallUnavailable') }}
        </button>
      </div>
    </div>
    <div v-if="sortedAgents.length > 0" class="settings-surface settings-surface-scroll">
      <table class="settings-table agent-table">
        <thead>
          <tr>
            <th>{{ t('settings.agents.host') }}</th>
            <th>{{ t('settings.agents.connectionStatus') }}</th>
            <th>{{ t('settings.agents.nextAction') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="agent in sortedAgents" :key="agent.host_id" data-test="agent-row">
            <td class="agent-host-cell">
              <div class="agent-host-name">{{ agent.host_name }}</div>
              <div class="muted mono">{{ agent.host_id }}</div>
              <div class="agent-tags">
                <span v-for="tag in agent.tags" :key="tag" class="tag-chip" :style="{ background: tagColor(tag) }">
                  {{ tag }}
                </span>
              </div>
            </td>
            <td class="agent-route-cell">
              <div class="route-summary">
                <span class="status-dot" :class="`tone-${stageViewFor(agent).tone}`" aria-hidden="true"></span>
                <span class="route-stage">{{ t(stageViewFor(agent).labelKey) }}</span>
                <span v-if="agentStage(agent, nodeOf(agent)) === 'healthy' || agentStage(agent, nodeOf(agent)) === 'degraded'" class="mono route-address">
                  {{ routeSummaryFor(agent).address }}
                </span>
                <span v-if="agentStage(agent, nodeOf(agent)) === 'degraded'" class="degraded-badge">
                  {{ t('settings.agents.stageDegraded') }}
                </span>
              </div>
              <button type="button" class="settings-btn settings-btn-text route-toggle" :data-test="`agent-route-toggle-${agent.host_id}`" @click="toggleRoute(agent.host_id)">
                {{ t('settings.agents.routeCount', { count: routeSummaryFor(agent).count }) }} {{ expandedRoutes.has(agent.host_id) ? '▴' : '▾' }}
              </button>
              <div v-if="expandedRoutes.has(agent.host_id)" class="route-details">
                <div
                  v-for="row in routeRowsFor(agent)"
                  :key="row.index"
                  class="route-detail-row"
                  :data-test="`agent-route-row-${agent.host_id}-${row.index}`"
                >
                  <span class="mono">{{ row.index + 1 }}</span>
                  <span class="route-dot" :class="`route-${row.status}`" aria-hidden="true"></span>
                  <span class="mono route-entry">{{ transportLabel(row.type) }} · {{ row.address }}</span>
                  <span class="route-status-label">{{ t(routeStatusKey(row.status)) }}</span>
                  <span v-if="row.current" class="route-current-badge">{{ t('settings.agents.routeCurrent') }}</span>
                  <span>{{ row.role === 'primary' ? t('settings.agents.primaryTransport') : t('settings.agents.fallbackTransport') }}</span>
                  <span v-if="row.error" class="route-error">{{ row.error }}</span>
                </div>
              </div>
            </td>
            <td class="agent-action-cell">
              <div class="agent-runtime">
                <span class="health" :class="`health-${runtimeOf(agent).health}`">{{ t(healthLabelKey(runtimeOf(agent).health)) }}</span>
                <span v-if="runtimeOf(agent).version" class="muted"> · v{{ runtimeOf(agent).version?.replace(/^v/, '') }}</span>
                <span class="muted"> · {{ updatedLabel(agent) }}</span>
              </div>
              <div class="agent-actions">
                <button
                  class="settings-btn"
                  :class="stageViewFor(agent).primary ? 'settings-btn-primary' : 'settings-btn-secondary'"
                  type="button"
                  :disabled="checking.has(agent.host_id)"
                  :data-test="`agent-primary-${agent.host_id}`"
                  @click="runPrimaryAction(agent)"
                >
                  {{ checking.has(agent.host_id) ? t('common.loading') : t(stageViewFor(agent).primaryActionKey) }}
                </button>
                <div class="agent-more">
                  <button class="settings-btn settings-btn-icon" type="button" :data-test="`agent-more-${agent.host_id}`" @click="toggleMenu(agent.host_id, $event)">⋯</button>
                  <Teleport to="body">
                    <div
                      v-if="openMenuHostId === agent.host_id"
                      class="agent-action-menu"
                      :style="actionMenuStyle()"
                      :data-test="`agent-menu-${agent.host_id}`"
                    >
                      <button type="button" :data-test="`agent-menu-transport-${agent.host_id}`" @click="openPanel(agent, 'transport')">{{ t('settings.agents.editConnection') }}</button>
                      <button type="button" :data-test="`agent-menu-security-${agent.host_id}`" @click="openPanel(agent, 'security')">{{ t('settings.agents.securityConfig') }}</button>
                      <button type="button" :data-test="`agent-menu-install-${agent.host_id}`" @click="openPanel(agent, 'install')">{{ t('settings.agents.generateCommand') }}</button>
                      <button type="button" :data-test="`agent-menu-check-${agent.host_id}`" @click="checkAgent(agent)">{{ t('settings.agents.recheck') }}</button>
                      <button type="button" class="danger" :disabled="uninstalling.has(agent.host_id)" :data-test="`agent-menu-uninstall-${agent.host_id}`" @click="openUninstall(agent)">
                        {{ t('settings.agents.uninstall') }}
                      </button>
                    </div>
                  </Teleport>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="settings-empty">{{ t('settings.agents.empty') }}</div>

    <AgentConfigPanel
      :visible="panelMode === 'create' || Boolean(panelTarget)"
      :agent="panelTarget"
      :node="panelTarget ? nodeOf(panelTarget) : undefined"
      :initial-tab="panelInitialTab"
      :mode="panelMode"
      :hosts="availableHosts"
      :host="panelTarget ? hostFor(panelTarget) : null"
      @created="agentCreated"
      @cancel="closePanel"
    />
    <AgentBulkUpdateModal
      :visible="bulkUpdateVisible"
      :agents="sortedAgents"
      :hosts="remoteStore.hosts"
      @cancel="closeBulkUpdate"
    />
    <AgentUninstallModal
      :visible="Boolean(uninstallTarget)"
      :host-name="uninstallTarget?.host_name || ''"
      :busy="uninstallTarget ? uninstalling.has(uninstallTarget.host_id) : false"
      @cancel="uninstallTarget = null"
      @confirm="uninstallAgent"
    />
    <AgentDetachModal
      :visible="Boolean(detachTarget)"
      :host-name="detachTarget?.host_name || ''"
      :busy="detachTarget ? detaching.has(detachTarget.host_id) : false"
      @cancel="detachTarget = null"
      @confirm="detachAgent"
    />
  </section>
</template>

<style scoped>
.agent-manager {
  width: 100%;
}
.mono {
  font-family: var(--font-mono, monospace);
}
.muted {
  color: var(--text-tertiary);
  font-size: 11px;
}
.manual-uninstall {
  display: grid;
  gap: 8px;
}
.manual-uninstall p {
  margin: 0;
}
.manual-uninstall-actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 8px;
}
.manual-uninstall-actions a {
  text-decoration: none;
}
.manual-uninstall-platform {
  display: grid;
  gap: 6px;
}
.manual-uninstall-platform code {
  overflow-x: auto;
  padding: 6px 8px;
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  white-space: nowrap;
}
.manual-uninstall-footer {
  display: flex;
  justify-content: flex-end;
}
.tag-chip {
  display: inline-block;
  padding: 1px 6px;
  margin-right: 4px;
  color: #fff;
  border-radius: 2px;
  font-size: 10px;
}
.agent-table {
  min-width: 820px;
}
.agent-host-cell,
.agent-route-cell,
.agent-action-cell {
  min-width: 0;
}
.agent-host-name {
  color: var(--text-primary);
  font-weight: 650;
}
.agent-tags {
  margin-top: 5px;
}
.route-summary,
.agent-actions,
.agent-runtime {
  display: flex;
  align-items: center;
  gap: 8px;
}
.route-summary {
  min-width: 0;
}
.status-dot,
.route-dot {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
}
.tone-success,
.route-reachable {
  background: var(--status-running);
}
.tone-warning {
  background: var(--status-warning);
}
.tone-danger,
.route-failed {
  background: var(--status-failed);
}
.route-untested {
  background: var(--text-tertiary);
}
.route-stage {
  color: var(--text-primary);
  font-weight: 600;
}
.route-address,
.route-entry {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.degraded-badge {
  color: var(--status-warning);
  font-size: 11px;
}
.route-toggle {
  margin-top: 4px;
}
.route-details {
  display: grid;
  gap: 4px;
  margin-top: 8px;
}
.route-detail-row {
  display: grid;
  grid-template-columns: 20px 10px minmax(150px, 1fr) auto auto auto;
  gap: 6px;
  align-items: center;
  padding: 6px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  font-size: 11px;
}
.route-current-badge {
  padding: 1px 6px;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  color: var(--text-primary);
  font-size: 10px;
}
.route-error {
  grid-column: 3 / -1;
  color: var(--status-failed);
}
.agent-action-cell {
  width: 260px;
}
.agent-action-cell,
.agent-actions {
  position: relative;
}
.agent-runtime {
  justify-content: flex-end;
  margin-bottom: 6px;
}
.agent-actions {
  justify-content: flex-end;
}
.agent-more {
  position: relative;
}
.agent-action-menu {
  position: fixed;
  z-index: 1000;
  display: grid;
  padding: 6px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: var(--bg-overlay);
  box-shadow: var(--shadow-modal);
}
.agent-action-menu button {
  min-height: 28px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  text-align: left;
}
.agent-action-menu button:hover:not(:disabled) {
  background: var(--control-hover);
  color: var(--text-primary);
}
.agent-action-menu button.danger {
  color: var(--danger);
}
.health-healthy {
  color: var(--status-running);
}
.health-unreachable {
  color: var(--status-failed);
}
.health-auth-failed,
.health-version-mismatch,
.health-pending-bootstrap {
  color: var(--status-warning);
}
</style>
