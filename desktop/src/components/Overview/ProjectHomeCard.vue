<!--
ProjectHomeCard：项目概览页的「开发环境归属」卡片。

职责：
  - 展示项目当前归属（本机 / 某台远端主机）的徽标，复用 .node-route-badge
    视觉家族
  - 归属在本机时提供「转移到 <开发机>…」入口（多台开发机时下拉选一台）；
    没有任何开发机主机时按钮置灰并提示去设置页开启
  - 归属在远端时提供「迁回本机…」入口
  - 挂载 ProjectTransferDialog 并在转移成功后刷新项目数据，让徽标/描述
    立刻反映最新归属，不需要用户手动刷新页面
  - compact 模式（复用于 workspace tab）只渲染徽标一行，不渲染标题/描述/
    操作按钮/底部说明，避免挤占 workspace tab 头部空间

边界：
  - 不做转移可行性判定——预检结果（blockers/ready）完全来自后端，本卡片
    和它挂载的弹窗都只负责渲染
  - 不直接调用转移相关 HTTP 接口，全部经 ProjectTransferDialog 完成
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
import ProjectTransferDialog from './ProjectTransferDialog.vue'
import type { Host, Project } from '@/api/agent'

const props = defineProps<{ project: Project; compact?: boolean }>()

const { t } = useAppI18n()
const router = useRouter()
const agentStore = useAgentStore()
const remoteStore = useRemoteStore()

const dropdownOpen = ref(false)
const showDialog = ref(false)
const dialogHostId = ref<string | undefined>(undefined)
const dialogHostName = ref<string | undefined>(undefined)

const isRemote = computed(() => !!props.project.home_host_id)

// 开发机候选：dev_machine_mode 打开且非本机自身节点。本机 is_self 节点的
// dev_machine_mode 恒为 false（见 api/agent.ts Host 类型注释），过滤条件里
// 的 !is_self 是双保险，不依赖这个恒等式将来不被打破。
const devMachineHosts = computed(() =>
  remoteStore.hosts.filter(host => host.dev_machine_mode && !host.is_self),
)

const badgeText = computed(() =>
  isRemote.value
    ? t('overview.home.badgeRemote', { host: props.project.home_host_name || props.project.home_host_id })
    : t('overview.home.badgeLocal'),
)

const descriptionText = computed(() =>
  isRemote.value
    ? t('overview.home.descriptionRemote', { host: props.project.home_host_name || props.project.home_host_id })
    : t('overview.home.descriptionLocal'),
)

const transferButtonLabel = computed(() => {
  if (devMachineHosts.value.length === 1) {
    return t('overview.home.transferTo', { host: devMachineHosts.value[0].name })
  }
  return t('overview.home.transferGeneric')
})

onMounted(async () => {
  // compact 模式只渲染徽标，徽标数据来自 project.home_host_name（已随项目一起
  // 拿到），不依赖 hosts 列表——没必要在挤占空间的 workspace tab 里额外拉一次。
  if (props.compact) return
  if (remoteStore.hosts.length === 0) {
    // 加载失败不阻断卡片渲染：最坏情况是按钮短暂显示「暂无开发机」，用户重开
    // 弹窗或刷新页面即可恢复，不值得为此单独维护一条错误态 UI。
    await remoteStore.loadHosts().catch(() => undefined)
  }
})

function onTransferClick() {
  if (devMachineHosts.value.length === 0) return
  if (devMachineHosts.value.length === 1) {
    openDialog(devMachineHosts.value[0])
    return
  }
  dropdownOpen.value = !dropdownOpen.value
}

function selectHost(host: Host) {
  dropdownOpen.value = false
  openDialog(host)
}

function openDialog(host: Host) {
  dialogHostId.value = host.id
  dialogHostName.value = host.name
  showDialog.value = true
}

function openBackDialog() {
  dialogHostId.value = undefined
  dialogHostName.value = undefined
  showDialog.value = true
}

function closeDialog() {
  showDialog.value = false
}

// handleTransferDone 在弹窗轮询到「succeeded」的同一时刻触发（弹窗仍停留在
// done 态等用户自己关闭）：立刻刷新项目数据，这样用户关闭弹窗时卡片上的
// 徽标/描述已经是转移后的归属，不需要额外手动刷新页面。
async function handleTransferDone() {
  await agentStore.reloadProject(props.project.id).catch(() => undefined)
}

function goSettings() {
  dropdownOpen.value = false
  void router.push({ path: '/settings', query: { tab: 'hosts' } })
}
</script>

<template>
  <section class="home-card" :class="{ compact }" data-test="project-home-card">
    <div class="home-card-head">
      <h3 v-if="!compact" class="home-card-title" data-test="home-title">{{ t('overview.home.title') }}</h3>
      <span class="node-route-badge home-badge" data-test="home-badge">{{ badgeText }}</span>
    </div>

    <template v-if="!compact">
      <p class="home-card-desc" data-test="home-desc">{{ descriptionText }}</p>

      <div class="home-actions" data-test="home-actions">
        <template v-if="!isRemote">
          <div class="home-transfer-wrap">
            <button
              type="button"
              class="home-btn-primary"
              data-test="home-transfer-btn"
              :disabled="devMachineHosts.length === 0"
              @click="onTransferClick"
            >
              {{ transferButtonLabel }}<span v-if="devMachineHosts.length > 1" aria-hidden="true"> ▾</span>
            </button>
            <div v-if="dropdownOpen" class="home-dev-machine-menu" data-test="home-dev-machine-menu">
              <button
                v-for="host in devMachineHosts"
                :key="host.id"
                type="button"
                class="home-dev-machine-item"
                :data-test="`home-dev-machine-item-${host.id}`"
                @click="selectHost(host)"
              >
                {{ host.name }}
              </button>
            </div>
          </div>
          <span v-if="devMachineHosts.length === 0" class="home-hint" data-test="home-transfer-hint">
            {{ t('overview.home.noDevMachineHint') }} ·
            <button type="button" class="home-hint-link" data-test="home-goto-settings" @click="goSettings">
              {{ t('overview.home.goSettings') }}
            </button>
          </span>
        </template>
        <template v-else>
          <button type="button" class="home-btn-primary" data-test="home-transfer-back-btn" @click="openBackDialog">
            {{ t('overview.home.transferBack') }}
          </button>
        </template>
      </div>

      <p class="home-card-footer" data-test="home-footer">{{ t('overview.home.footerNote') }}</p>
    </template>

    <ProjectTransferDialog
      v-if="showDialog"
      :project-id="project.id"
      :host-id="dialogHostId"
      :host-name="dialogHostName"
      @cancel="closeDialog"
      @done="handleTransferDone"
    />
  </section>
</template>

<style scoped>
.home-card {
  display: grid;
  gap: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  padding: 12px 14px;
  margin: 0 22px 14px;
}
.home-card.compact {
  margin: 8px 16px;
  padding: 6px 10px;
}
.home-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.home-card-title {
  margin: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}
.home-card-desc {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.5;
}
.home-card-footer {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 10px;
}
.home-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
.home-transfer-wrap {
  position: relative;
}
.home-btn-primary {
  min-height: 28px;
  border: 1px solid var(--accent);
  border-radius: 6px;
  background: var(--accent);
  color: #fff;
  padding: 5px 12px;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  white-space: nowrap;
}
.home-btn-primary:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.home-dev-machine-menu {
  position: absolute;
  z-index: 20;
  top: calc(100% + 4px);
  left: 0;
  display: grid;
  min-width: 160px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
  padding: 4px;
  box-shadow: var(--shadow-modal);
}
.home-dev-machine-item {
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  padding: 6px 8px;
  text-align: left;
  font: inherit;
  font-size: 12px;
}
.home-dev-machine-item:hover {
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.home-hint {
  color: var(--text-tertiary);
  font-size: 11px;
}
.home-hint-link {
  border: 0;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  padding: 0;
  font: inherit;
  font-size: 11px;
}

/* 复用 NodeCenter/NodeCard.vue 的 .node-route-badge 徽标视觉家族；scoped
   样式不跨组件生效，这里按同一套视觉规则重新声明（同 HostManagerTab.vue 的
   既有做法）。 */
.node-route-badge {
  display: inline-block;
  max-width: 220px;
  padding: 1px 6px;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.home-badge {
  color: var(--status-running);
  border-color: rgba(63, 185, 80, 0.4);
}
</style>
