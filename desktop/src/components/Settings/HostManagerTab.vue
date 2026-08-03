<!--
HostManagerTab：设置页 Host 身份管理标签页。

职责：
  - 列出远程 Host 的身份、地址元数据和 tag
  - 提供 Host 新建、编辑、删除入口
  - 展示 SSH 登录方式摘要
  - 标记 dev_machine_mode 主机的「开发机」角色徽标（Task 12）
  - 删除 Host 命中 409 project_home 时展示自建守卫弹窗，引导用户先在项目概览
    迁回受影响项目（Task 12）
  - 保存 Host 表单后，若响应携带 homed_projects，把它透传给 HostFormModal 渲染
    「归属不变、端口镜像已停止」的横幅，并保持表单打开等用户主动关闭（Task 12）

边界：
  - 不编辑 Agent 连接方式，Agent 配置由 AgentManagerTab 负责
  - 不建立隧道 WebSocket
  - 不执行 Agent 安装、卸载或探活
  - 不提供「强制删除」——project_home 守卫弹窗只引导迁回，没有绕过归属校验的入口
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import HostFormModal from './HostFormModal.vue'
import { AgentAPIError, type Host, type HostCreatePayload } from '@/api/agent'

const store = useRemoteStore()
const { t } = useI18n()

const formVisible = ref(false)
const editing = ref<Host | null>(null)
const error = ref<string | null>(null)
// 保存成功后，若响应携带 homed_projects，回填给 HostFormModal 渲染提示横幅；
// 详细的时机说明见 HostFormModal.vue 里 hasHomedProjectsNotice 附近的注释。
const homedProjectsNotice = ref<string[] | null>(null)

// deleteHost 命中 409 project_home 时的受影响项目清单；非 null 时展示守卫弹窗。
// data 结构见 api/agent.ts deleteHost 的注释：{ host_id, projects: string[] }。
interface ProjectHomeGuardData {
  host_id?: string
  projects?: string[]
}
const projectHomeGuard = ref<{ hostName: string; projects: string[] } | null>(null)

const rescanTarget = ref<Host | null>(null)
const rescanFingerprint = ref('')
const rescanError = ref('')

const sortedHosts = computed(() =>
  [...store.hosts].sort((a, b) => a.name.localeCompare(b.name)),
)

onMounted(async () => {
  try {
    await store.loadHosts()
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.loadFailed')
  }
})

function openCreate() {
  error.value = null
  homedProjectsNotice.value = null
  editing.value = null
  formVisible.value = true
}

function openEdit(host: Host) {
  error.value = null
  homedProjectsNotice.value = null
  editing.value = host
  formVisible.value = true
}

// closeForm 是表单弹窗唯一的关闭入口（cancel 事件、以及提示横幅上的关闭按钮都
// 走这里），统一清空 homedProjectsNotice——否则下次打开表单会残留上一次的提示。
function closeForm() {
  formVisible.value = false
  homedProjectsNotice.value = null
}

async function handleSubmit(payload: HostCreatePayload) {
  error.value = null
  homedProjectsNotice.value = null
  try {
    if (editing.value) {
      const updated = await store.updateHost(editing.value.id, payload)
      // 为什么保存后才提示：homed_projects 只在后端真正把 dev_machine_mode 从
      // true 落到 false（且该主机仍是若干项目的归属）时才非空。提交前的复选框
      // 只是草稿，勾没勾都不代表已经生效——若改成监听 checkbox 变化就提示，用户
      // 还没点保存就会看到「端口镜像已停止」的误报。这里只信响应，不信中间态。
      if (updated.homed_projects && updated.homed_projects.length > 0) {
        homedProjectsNotice.value = updated.homed_projects
        // 保持弹窗打开展示提示，不像常规保存那样自动收起——用户需要看到这条
        // 「归属不变」的说明后再主动关闭。
        return
      }
    } else {
      await store.createHost(payload)
    }
    formVisible.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.saveFailed')
  }
}

async function handleDelete(host: Host) {
  if (!confirm(t('settings.hosts.deleteConfirm', { name: host.name }))) return
  try {
    await store.deleteHost(host.id)
  } catch (err) {
    // 409 project_home：该主机仍是若干项目的归属，删除会让这些项目的归属指向
    // 一个不存在的主机。用自建守卫弹窗列出受影响项目、引导「先在项目概览迁回」，
    // 不提供强制删除——迁回是唯一路径。与 agent_configured 的既有错误横幅处理
    // 相互独立：后端先查 agent_configured 再查 project_home（Task 9 的既定顺序），
    // 走到这个分支时说明 agent 层面的阻塞（如果曾经有）已经解除。
    if (err instanceof AgentAPIError && err.code === 'project_home') {
      const data = err.data as ProjectHomeGuardData | undefined
      projectHomeGuard.value = {
        hostName: host.name,
        projects: Array.isArray(data?.projects) ? data.projects : [],
      }
      return
    }
    error.value = err instanceof Error ? err.message : t('settings.hosts.deleteFailed')
  }
}

function addressLabel(host: Host): string {
  const parts = [host.public_ip, host.private_ip].filter(Boolean)
  return parts.length > 0 ? parts.join(' / ') : '-'
}

function sshLabel(host: Host): string {
  if (!host.ssh_host && !host.ssh_user) return '-'
  const user = host.ssh_user ? `${host.ssh_user}@` : ''
  const port = host.ssh_port ? `:${host.ssh_port}` : ''
  return `${user}${host.ssh_host || '-'}${port}`
}

// 重采是「接受新指纹」的唯一入口，且刻意与连接失败现场分离：
// 若把它做成报错弹窗里的一个按钮，用户会条件反射点掉，防护形同虚设。
async function openRescan(host: Host) {
  rescanTarget.value = host
  rescanFingerprint.value = ''
  rescanError.value = ''
  // 记录发起采集时的目标 host id，用于在异步返回时判断弹窗是否仍在展示同一台主机
  // （见下方成功/失败分支的 identity 校验）。
  const scannedHostId = host.id
  try {
    const result = await store.scanHostKey({
      ssh_host: host.ssh_host ?? '',
      ssh_port: host.ssh_port || 22,
    })
    // 采集到的指纹只对发起采集的那台主机有意义：飞行期间用户若关闭弹窗改采别的
    // host，此时绝不能把 A 的指纹套用到 B 的弹窗上，否则会把错误的指纹当作「用户已确认」
    // 写入 B——这正是 fail-closed 设计要防止的失败模式，且发生得悄无声息。
    if (rescanTarget.value?.id !== scannedHostId) return
    rescanFingerprint.value = result.fingerprint
  } catch (err) {
    if (rescanTarget.value?.id !== scannedHostId) return
    rescanError.value = err instanceof Error ? err.message : String(err)
  }
}

async function confirmRescan() {
  const host = rescanTarget.value
  if (!host || !rescanFingerprint.value) return
  try {
    // store.updateHost 内部已回写 hosts 状态，无需再手动刷新列表。
    await store.updateHost(host.id, {
      name: host.name,
      public_ip: host.public_ip ?? '',
      private_ip: host.private_ip ?? '',
      ssh_host: host.ssh_host ?? '',
      ssh_port: host.ssh_port || 22,
      ssh_user: host.ssh_user ?? 'root',
      ssh_password: '',
      ssh_private_key: '',
      ssh_host_key_fingerprint: rescanFingerprint.value,
      clear_ssh_password: false,
      clear_ssh_private_key: false,
      clear_ssh_host_key_fingerprint: false,
      tags: [...host.tags],
    })
    rescanTarget.value = null
  } catch (err) {
    rescanError.value = err instanceof Error ? err.message : String(err)
  }
}
</script>

<template>
  <section class="host-manager">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.hosts.title') }}</h1>
      </div>
      <div class="settings-toolbar">
        <button class="settings-btn settings-btn-primary" data-test="host-add" @click="openCreate">+ {{ t('settings.hosts.add') }}</button>
      </div>
    </header>

    <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>
    <div v-if="sortedHosts.length > 0" class="settings-surface settings-surface-scroll">
      <table class="settings-table host-table">
        <thead>
          <tr>
            <th>{{ t('settings.hosts.name') }}</th>
            <th>{{ t('settings.hosts.address') }}</th>
            <th>{{ t('settings.hostForm.sshAddress') }}</th>
            <th>{{ t('settings.hosts.tags') }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="host in sortedHosts" :key="host.id" data-test="host-row">
            <td>
              {{ host.name }}
              <span
                v-if="host.dev_machine_mode"
                class="node-route-badge dev-machine-badge"
                data-test="host-dev-machine-badge"
              >{{ t('settings.hosts.devMachineBadge') }}</span>
            </td>
            <td>
              <div class="address-meta" data-test="host-address-meta">{{ addressLabel(host) }}</div>
            </td>
            <td>
              <div class="address-meta mono" data-test="host-ssh-meta">{{ sshLabel(host) }}</div>
              <span
                v-if="host.ssh_host && !host.ssh_host_key_fingerprint_configured"
                class="fingerprint-warning"
                data-test="host-fingerprint-missing"
              >
                {{ t('settings.hosts.fingerprintMissing') }}
              </span>
            </td>
            <td>
              <span
                v-for="tag in host.tags"
                :key="tag"
                class="tag-chip"
                :style="{ background: tagColor(tag) }"
              >
                {{ tag }}
              </span>
            </td>
            <td class="row-actions" data-test="host-row-actions">
              <button
                v-if="host.ssh_host_key_fingerprint_configured"
                class="settings-btn settings-btn-text"
                data-test="host-rescan"
                @click="openRescan(host)"
              >
                {{ t('settings.hostForm.rescanAction') }}
              </button>
              <button class="settings-btn settings-btn-text" data-test="host-edit" @click="openEdit(host)">{{ t('common.edit') }}</button>
              <button class="settings-btn settings-btn-text settings-btn-danger" data-test="host-delete" @click="handleDelete(host)">{{ t('common.delete') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="settings-empty">{{ t('settings.hosts.empty') }}</div>

    <HostFormModal
      :visible="formVisible"
      :initial="editing"
      :error="error"
      :homed-projects-notice="homedProjectsNotice"
      @submit="handleSubmit"
      @cancel="closeForm"
    />

    <div v-if="projectHomeGuard" class="settings-modal-backdrop" data-test="host-delete-guard" @click.self="projectHomeGuard = null">
      <div class="settings-modal">
        <div class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.hosts.deleteGuardTitle') }}</h2>
        </div>
        <div class="settings-modal-body">
          <div class="settings-alert settings-alert-warning">
            {{ t('settings.hosts.deleteGuardIntro', { name: projectHomeGuard.hostName, count: projectHomeGuard.projects.length }) }}
          </div>
          <ul class="guard-project-list" data-test="host-delete-guard-projects">
            <li v-for="name in projectHomeGuard.projects" :key="name">{{ name }}</li>
          </ul>
          <p class="settings-field-hint">{{ t('settings.hosts.deleteGuardHint') }}</p>
        </div>
        <div class="settings-modal-footer">
          <button type="button" class="settings-btn settings-btn-primary" data-test="host-delete-guard-close" @click="projectHomeGuard = null">
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="rescanTarget" class="settings-modal-backdrop" @click.self="rescanTarget = null">
      <div class="settings-modal">
        <div class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.hostForm.rescanTitle') }}</h2>
        </div>
        <div class="settings-modal-body">
          <div class="settings-alert settings-alert-danger">{{ t('settings.hostForm.rescanWarning') }}</div>
          <div v-if="rescanError" class="settings-alert settings-alert-danger" data-test="host-rescan-error">
            {{ rescanError }}
          </div>
          <template v-else-if="rescanFingerprint">
            <p>{{ t('settings.hostForm.rescanNewFingerprint') }}</p>
            <code class="mono" data-test="host-rescan-new-fingerprint">{{ rescanFingerprint }}</code>
          </template>
        </div>
        <div class="settings-modal-footer">
          <button type="button" class="settings-btn" @click="rescanTarget = null">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-danger"
            :disabled="!rescanFingerprint"
            data-test="host-rescan-confirm"
            @click="confirmRescan"
          >
            {{ t('settings.hostForm.rescanConfirm') }}
          </button>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.host-manager {
  width: 100%;
}
.address-meta {
  color: var(--text-secondary);
  font-size: 12px;
}
.mono {
  font-family: var(--font-mono, monospace);
}
.tag-chip {
  display: inline-block;
  padding: 1px 6px;
  margin-right: 4px;
  color: #fff;
  border-radius: 2px;
  font-size: 10px;
}
.row-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  white-space: nowrap;
}
.fingerprint-warning {
  display: inline-block;
  margin-top: 4px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--status-failed);
  color: #fff;
  font-size: 11px;
}
/* 复用 NodeCenter/NodeCard.vue 的 .node-route-badge 徽标视觉家族（胶囊外观、原型
   settings.html:51 已背书）；Vue scoped 样式不跨组件生效，这里按同一套视觉规则重新
   声明基础样式，再叠加开发机专属的绿色变体——这个徽标只有「是/否为开发机」一种
   状态，不像 NodeCard 的路由徽标那样需要区分降级/直连等多种配色分支。 */
.node-route-badge {
  display: inline-block;
  max-width: 112px;
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
.dev-machine-badge {
  margin-left: 6px;
  color: var(--status-running);
  border-color: rgba(63, 185, 80, 0.4);
}
.guard-project-list {
  margin: 10px 0 0;
  padding-left: 18px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.7;
}
</style>
