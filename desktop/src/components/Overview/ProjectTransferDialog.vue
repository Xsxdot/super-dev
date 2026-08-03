<!--
ProjectTransferDialog：项目开发环境归属转移/迁回的预览与执行弹窗。

职责：
  - 挂载时按方向（转移到目标机 / 迁回本机）跑一次只读预检（仅正向转移有预检
    端点），分段渲染后端已分类好的阻塞项 blockers 与就绪确认 ready 两段清单
  - 阻塞项非空时禁止执行，按钮文案退化为「先处理上述阻塞项」
  - 执行转移/迁回后以 2s 间隔轮询 transferStatus，渲染步骤列表的实时状态
    （✓/×/spinner），202 初始快照的 steps 可能为空（后端 goroutine 竞态），
    不依赖它，一切以轮询结果为准
  - 转移结束（成功）后展示资产清单（env 文件缺失/疑似密钥键名等提示），文案
    明确「需自行补齐，SuperDev 不搬运值」
  - applying 阶段禁止关闭（背景点击、右上角 × 和 Cancel 按钮均被拦截）

边界：
  - 不做预检判定——哪些是 blocker、哪些是 ready 全由后端决定，本组件只分段
    渲染已分类好的结果，不重新推导
  - 不搬运、不反脱敏任何密钥值，asset_report 只携带键名/路径级提示
  - 迁回方向没有独立只读预检端点（后端未提供成对的双向探测），不会为此虚构
    一次不存在的预检请求，直接进入确认态
-->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { api, AgentAPIError, type TransferCheckItem, type TransferStep, type TransferStepState } from '@/api/agent'

const props = defineProps<{ projectId: string; hostId?: string; hostName?: string }>()
const emit = defineEmits<{ cancel: []; done: [] }>()

const { t } = useAppI18n()

type Phase = 'loading' | 'preview' | 'applying' | 'done' | 'error'

// hostId 缺省即迁回本机：调用方（ProjectHomeCard）按这一个字段区分方向，
// 不另外传 mode，避免两个字段表达同一件事、之后彼此不同步。
const isBack = computed(() => !props.hostId)

const phase = ref<Phase>('loading')
const blockers = ref<TransferCheckItem[]>([])
const ready = ref<TransferCheckItem[]>([])
const targetDir = ref('')
const branch = ref('')
const loadError = ref<string | null>(null)
const applyError = ref<string | null>(null)
const steps = ref<TransferStep[]>([])
const assetReport = ref<TransferCheckItem[]>([])
const runError = ref<string | null>(null)

let pollTimer: ReturnType<typeof window.setInterval> | undefined

onMounted(load)
onUnmounted(stopPolling)

async function load() {
  phase.value = 'loading'
  loadError.value = null
  if (isBack.value) {
    blockers.value = []
    ready.value = []
    targetDir.value = ''
    branch.value = ''
    phase.value = 'preview'
    return
  }
  try {
    const res = await api.transferPreflight(props.projectId, props.hostId!, undefined)
    blockers.value = res.blockers
    ready.value = res.ready
    targetDir.value = res.target_dir
    branch.value = res.branch
    phase.value = 'preview'
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err)
    phase.value = 'error'
  }
}

async function execute() {
  // 双保险：按钮本身已用 disabled 挡住，这里再挡一次防止编程式触发绕过。
  if (blockers.value.length > 0) return
  phase.value = 'applying'
  applyError.value = null
  runError.value = null
  try {
    const initial = isBack.value
      ? await api.transferBack(props.projectId)
      : await api.startTransfer(props.projectId, props.hostId!, targetDir.value || undefined)
    // 202 响应体的 steps 可能因后端 goroutine 竞态而为空，只在非空时用它垫一帧，
    // 真正权威的进度以随后启动的轮询为准，不依赖这里一定有数据。
    if (initial.steps.length > 0) steps.value = initial.steps
    startPolling()
  } catch (err) {
    // 常见触发：同项目已有进行中的转移（409）。退回 preview 让用户看到原因，
    // 而不是卡死在一个已经失败的 applying 态里。
    applyError.value = err instanceof Error ? err.message : String(err)
    phase.value = 'preview'
  }
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(() => {
    void pollStatus()
  }, 2000)
}

function stopPolling() {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

async function pollStatus() {
  try {
    const res = await api.transferStatus(props.projectId)
    steps.value = res.steps
    if (res.state === 'succeeded') {
      assetReport.value = res.asset_report ?? []
      stopPolling()
      phase.value = 'done'
      emit('done')
    } else if (res.state === 'failed') {
      runError.value = res.error ?? null
      stopPolling()
      phase.value = 'error'
    }
    // state === 'running'：不动 phase，等下一轮 2s 轮询。
  } catch (err) {
    // 404 = agent 进程重启后内存态丢失，不是网络抖动，没有「下一轮」可等，
    // 直接判定转移记录丢失。其余错误（超时等）容忍，交给下一次轮询自然重试，
    // 不因偶发一次失败就打断用户正在看的进度。
    if (err instanceof AgentAPIError && err.status === 404) {
      stopPolling()
      runError.value = t('projectTransfer.statusLost')
      phase.value = 'error'
    }
  }
}

function stepLabel(code: string): string {
  return t(`projectTransfer.steps.${code}`)
}

function stepStateLabel(state: TransferStepState): string {
  return t(`projectTransfer.stepState.${state}`)
}

function stepSymbol(state: TransferStepState): string {
  if (state === 'done') return '✓'
  if (state === 'failed') return '×'
  if (state === 'skipped') return '−'
  if (state === 'running') return ''
  return '•'
}

function dismiss() {
  if (phase.value === 'applying') return
  emit('cancel')
}
</script>

<template>
  <div class="settings-modal-backdrop" data-test="project-transfer-dialog" @click.self="dismiss">
    <section class="settings-modal transfer-modal" role="dialog" aria-modal="true" aria-labelledby="project-transfer-title">
      <header class="settings-modal-header">
        <h2 id="project-transfer-title" class="settings-modal-title">
          {{ isBack ? t('projectTransfer.titleBack') : t('projectTransfer.titleTo', { host: hostName || hostId }) }}
        </h2>
        <button class="settings-btn settings-btn-icon" type="button" :disabled="phase === 'applying'" @click="dismiss">×</button>
      </header>

      <div class="settings-modal-body transfer-body">
        <div v-if="phase === 'loading'" class="settings-alert" data-test="transfer-loading">{{ t('common.loading') }}</div>

        <div v-else-if="phase === 'error'" class="settings-alert settings-alert-danger" data-test="transfer-error">
          {{ runError || loadError }}
        </div>

        <template v-else-if="phase === 'preview'">
          <section class="transfer-section" data-test="transfer-blockers">
            <h3 class="transfer-subtitle">{{ t('projectTransfer.blockersTitle') }}</h3>
            <div v-if="blockers.length === 0" class="settings-empty" data-test="transfer-blockers-empty">
              {{ t('projectTransfer.blockersEmpty') }}
            </div>
            <ul v-else class="transfer-check-list">
              <li
                v-for="(item, index) in blockers"
                :key="`b-${item.code}-${index}`"
                class="transfer-check-row is-blocker"
                :data-test="`transfer-blocker-${index}`"
              >
                <span class="check-icon" aria-hidden="true">⚠</span>
                <code class="settings-mono">{{ item.code }}</code>
                <span class="check-detail">{{ item.detail }}</span>
              </li>
            </ul>
          </section>

          <section class="transfer-section" data-test="transfer-ready">
            <h3 class="transfer-subtitle">{{ t('projectTransfer.readyTitle') }}</h3>
            <div v-if="ready.length === 0" class="settings-empty" data-test="transfer-ready-empty">
              {{ t('projectTransfer.readyEmpty') }}
            </div>
            <ul v-else class="transfer-check-list">
              <li
                v-for="(item, index) in ready"
                :key="`r-${item.code}-${index}`"
                class="transfer-check-row is-ready"
                :data-test="`transfer-ready-${index}`"
              >
                <span class="check-icon" aria-hidden="true">✓</span>
                <code class="settings-mono">{{ item.code }}</code>
                <span class="check-detail">{{ item.detail }}</span>
              </li>
            </ul>
          </section>

          <section v-if="!isBack" class="transfer-section">
            <label class="settings-field">
              <span class="settings-field-label">{{ t('projectTransfer.targetDirLabel') }}</span>
              <input v-model="targetDir" class="settings-input" data-test="transfer-target-dir" />
            </label>
            <p v-if="branch" class="transfer-branch" data-test="transfer-branch">{{ t('projectTransfer.branchLabel') }}: {{ branch }}</p>
          </section>

          <div v-if="applyError" class="settings-alert settings-alert-danger" data-test="transfer-apply-error">{{ applyError }}</div>
        </template>

        <template v-else-if="phase === 'applying'">
          <h3 class="transfer-subtitle" data-test="transfer-applying-title">{{ t('projectTransfer.applyingTitle') }}</h3>
          <ul class="transfer-step-list" data-test="transfer-applying">
            <li v-for="step in steps" :key="step.code" class="transfer-step-row" :data-test="`transfer-step-${step.code}`">
              <span class="step-icon" :class="step.state" :aria-label="step.state">{{ stepSymbol(step.state) }}</span>
              <span class="step-label">{{ stepLabel(step.code) }}</span>
              <span class="step-state">{{ stepStateLabel(step.state) }}</span>
            </li>
          </ul>
        </template>

        <template v-else-if="phase === 'done'">
          <div class="settings-alert settings-alert-success" data-test="transfer-done">
            <strong>{{ t('projectTransfer.doneTitle') }}</strong>
            <p>{{ t('projectTransfer.doneAssetNote') }}</p>
          </div>
          <div v-if="assetReport.length === 0" class="settings-empty" data-test="transfer-asset-empty">
            {{ t('projectTransfer.assetReportEmpty') }}
          </div>
          <ul v-else class="transfer-check-list">
            <li v-for="(item, index) in assetReport" :key="`a-${item.code}-${index}`" class="transfer-check-row" :data-test="`transfer-asset-${index}`">
              <code class="settings-mono">{{ item.code }}</code>
              <span class="check-detail">{{ item.detail }}</span>
            </li>
          </ul>
        </template>
      </div>

      <footer class="settings-modal-footer">
        <template v-if="phase === 'done' || phase === 'error'">
          <button class="settings-btn settings-btn-primary" type="button" data-test="transfer-close" @click="dismiss">
            {{ t('common.close') }}
          </button>
        </template>
        <template v-else>
          <button
            class="settings-btn settings-btn-secondary"
            type="button"
            data-test="transfer-cancel"
            :disabled="phase === 'applying'"
            @click="dismiss"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            class="settings-btn settings-btn-primary"
            type="button"
            data-test="transfer-execute"
            :disabled="phase !== 'preview' || blockers.length > 0"
            @click="execute"
          >
            {{ phase === 'applying' ? t('common.loading') : (blockers.length > 0 ? t('projectTransfer.executeBlocked') : t('projectTransfer.execute')) }}
          </button>
        </template>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.transfer-modal {
  width: min(640px, calc(100vw - 48px));
}
.transfer-body {
  display: grid;
  gap: 16px;
}
.transfer-section {
  display: grid;
  gap: 8px;
}
.transfer-subtitle {
  margin: 0;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 650;
}
.transfer-branch {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 11px;
}
.transfer-check-list {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.transfer-check-row {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 6px 10px;
  font-size: 12px;
}
.transfer-check-row .check-icon {
  flex-shrink: 0;
  width: 14px;
  text-align: center;
}
.transfer-check-row.is-blocker .check-icon {
  color: var(--status-starting);
}
.transfer-check-row.is-ready .check-icon {
  color: var(--success);
}
.transfer-check-row .check-detail {
  color: var(--text-secondary);
}
.transfer-step-list {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.transfer-step-row {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 6px 10px;
  font-size: 12px;
}
.step-icon {
  width: 14px;
  height: 14px;
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 14px;
  text-align: center;
}
.step-icon.running {
  border: 2px solid color-mix(in srgb, var(--accent) 25%, transparent);
  border-top-color: var(--accent);
  animation: transfer-step-spin 0.9s linear infinite;
}
.step-icon.done {
  color: var(--success);
}
.step-icon.failed {
  color: var(--status-failed);
}
.step-icon.skipped,
.step-icon.pending {
  color: var(--text-tertiary);
}
@keyframes transfer-step-spin {
  to { transform: rotate(360deg); }
}
.step-label {
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.step-state {
  color: var(--text-tertiary);
  font-size: 11px;
}
</style>
