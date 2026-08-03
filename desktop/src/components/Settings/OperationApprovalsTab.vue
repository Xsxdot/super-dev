<!--
操作审批 Tab

职责：
  - 展示待处理 operation approval
  - 提供批准/拒绝入口
  - 展示「最近已裁决」分节（store.decided，来自 /ws/operation-approvals 快照），
    行灰化样式 + 标注裁决方，让本控制面之外的裁决结果也能在这里看到

边界：
  - 不计算风险等级
  - 不展示 approval token
-->
<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'
import type { ApprovalPolicy, OperationApproval } from '@/api/agent'

const { t } = useI18n()
const store = useOperationApprovalStore()
const settingsStore = useSettingsStore()
const approvalForm = reactive<ApprovalPolicy>(defaultApprovalPolicy())
const savingApprovalPolicy = ref(false)
const saveMessage = ref('')
const saveMessageKind = ref<'success' | 'error'>('success')

onMounted(() => {
  void store.loadPending()
  void settingsStore.loadAgentSettings()
})

watch(
  () => settingsStore.agentSettings.approval,
  approval => {
    Object.assign(approvalForm, normalizeApprovalPolicy(approval))
  },
  { immediate: true },
)

function defaultApprovalPolicy(): ApprovalPolicy {
  return {
    config_upsert: true,
    pipeline_upsert: true,
    pipeline_run: true,
    template_import: true,
    browser_debug_open: true,
    code_debug_open: true,
    code_debug_evaluate: true,
    grace_minutes: 15,
  }
}

function normalizeApprovalPolicy(approval?: ApprovalPolicy): ApprovalPolicy {
  return { ...defaultApprovalPolicy(), ...(approval ?? {}) }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function clearSaveMessage() {
  saveMessage.value = ''
}

async function saveApprovalSettings() {
  clearSaveMessage()
  savingApprovalPolicy.value = true
  try {
    await settingsStore.saveApprovalPolicy({
      config_upsert: approvalForm.config_upsert,
      pipeline_upsert: approvalForm.pipeline_upsert,
      pipeline_run: approvalForm.pipeline_run,
      template_import: approvalForm.template_import,
      browser_debug_open: approvalForm.browser_debug_open,
      code_debug_open: approvalForm.code_debug_open,
      code_debug_evaluate: approvalForm.code_debug_evaluate,
      grace_minutes: approvalForm.grace_minutes,
    })
    saveMessageKind.value = 'success'
    saveMessage.value = t('settings.approvals.saveSucceeded')
  } catch (err) {
    saveMessageKind.value = 'error'
    saveMessage.value = t('settings.approvals.saveFailed', { message: errorMessage(err) })
  } finally {
    savingApprovalPolicy.value = false
  }
}

function shortFingerprint(approval: OperationApproval): string {
  const fp = approval.plan.fingerprint || ''
  return fp.length > 18 ? `${fp.slice(0, 18)}...` : fp
}

// decidedLabel 渲染「最近已裁决」行的归属文案。
//
// 注意：
//   - expired 终态没有裁决人，approval.decided_by 是空字符串——必须与「有
//     decided_by」区分开，否则会拼出「已由  处理」这种空洞文案（见 store 注释）
function decidedLabel(approval: OperationApproval): string {
  return approval.decided_by
    ? t('settings.approvals.decidedBy', { name: approval.decided_by })
    : t('settings.approvals.decidedUnnamed')
}
</script>

<template>
  <section data-test="operation-approvals-tab" class="approval-pane">
    <header class="settings-pane-header approval-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.approvals.title') }}</h1>
        <p class="settings-pane-description">{{ t('settings.approvals.pendingCount', { count: store.pendingCount }) }}</p>
      </div>
      <button class="settings-btn settings-btn-secondary" type="button" :disabled="store.loading" @click="() => store.loadPending()">
        {{ t('settings.approvals.refresh') }}
      </button>
    </header>

    <p v-if="store.error" class="settings-alert settings-alert-danger">{{ store.error }}</p>
    <section class="settings-card approval-policy-card" data-test="approval-policy-card">
      <div class="approval-policy-header">
        <div>
          <h2>{{ t('settings.approvals.policyTitle') }}</h2>
          <p>{{ t('settings.approvals.policyDescription') }}</p>
        </div>
        <button
          class="settings-btn settings-btn-primary"
          type="button"
          data-test="approval-settings-save"
          :disabled="settingsStore.loading || savingApprovalPolicy"
          @click="saveApprovalSettings"
        >
          {{ t('settings.approvals.savePolicy') }}
        </button>
      </div>

      <div class="approval-policy-grid">
        <label class="policy-toggle">
          <input v-model="approvalForm.config_upsert" data-test="approval-switch-config-upsert" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.configUpsert') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.pipeline_upsert" data-test="approval-switch-pipeline-upsert" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.pipelineUpsert') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.pipeline_run" data-test="approval-switch-pipeline-run" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.pipelineRun') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.template_import" data-test="approval-switch-template-import" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.templateImport') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.browser_debug_open" data-test="approval-switch-browser-debug-open" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.browserDebugOpen') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.code_debug_open" data-test="approval-switch-code-debug-open" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.codeDebugOpen') }}</span>
        </label>
        <label class="policy-toggle">
          <input v-model="approvalForm.code_debug_evaluate" data-test="approval-switch-code-debug-evaluate" type="checkbox" @change="clearSaveMessage">
          <span>{{ t('settings.approvals.codeDebugEvaluate') }}</span>
        </label>
        <label class="policy-number">
          <span>{{ t('settings.approvals.graceMinutes') }}</span>
          <input
            v-model.number="approvalForm.grace_minutes"
            data-test="approval-grace-minutes"
            type="number"
            min="1"
            max="120"
            @change="clearSaveMessage"
          >
        </label>
      </div>
      <p
        v-if="saveMessage"
        class="settings-alert approval-save-notice"
        :class="saveMessageKind === 'success' ? 'settings-alert-success' : 'settings-alert-danger'"
        :role="saveMessageKind === 'success' ? 'status' : 'alert'"
        aria-live="polite"
        data-test="approval-policy-save-notice"
      >
        {{ saveMessage }}
      </p>
    </section>

    <p v-if="!store.error && store.approvals.length === 0" class="settings-empty">{{ t('settings.approvals.empty') }}</p>

    <div v-else class="approval-list">
      <article v-for="approval in store.approvals" :key="approval.id" class="settings-card approval-item">
        <div class="approval-main">
          <div class="approval-title">
            <strong>{{ approval.plan.kind }}</strong>
            <span class="settings-badge risk-badge">{{ t('settings.approvals.risk') }} {{ approval.plan.risk_level }}</span>
          </div>
          <p>{{ approval.plan.target_summary || approval.plan.target.deployment_id || approval.plan.target.template_path }}</p>
          <p class="fingerprint">{{ shortFingerprint(approval) }}</p>
        </div>

        <div class="approval-details">
          <div v-if="approval.plan.reasons?.length">
            <span>{{ t('settings.approvals.reasons') }}</span>
            <ul>
              <li v-for="reason in approval.plan.reasons" :key="reason">{{ reason }}</li>
            </ul>
          </div>
          <div v-if="approval.plan.expected_effects?.length">
            <span>{{ t('settings.approvals.effects') }}</span>
            <ul>
              <li v-for="effect in approval.plan.expected_effects" :key="effect">{{ effect }}</li>
            </ul>
          </div>
        </div>

        <div class="approval-actions">
          <button
            class="settings-btn settings-btn-primary"
            type="button"
            :data-test="`approval-approve-${approval.id}`"
            @click="store.approve(approval.id, '')"
          >
            {{ t('settings.approvals.approve') }}
          </button>
          <button
            class="settings-btn settings-btn-danger"
            type="button"
            :data-test="`approval-reject-${approval.id}`"
            @click="store.reject(approval.id, '')"
          >
            {{ t('settings.approvals.reject') }}
          </button>
        </div>
      </article>
    </div>

    <section v-if="store.decided.length" class="approval-decided-section" data-test="approval-decided-section">
      <h2 class="approval-decided-title">{{ t('settings.approvals.decidedTitle') }}</h2>
      <div class="approval-list">
        <article
          v-for="approval in store.decided"
          :key="approval.id"
          class="settings-card approval-item approval-item-decided"
          :data-test="`approval-decided-item-${approval.id}`"
        >
          <div class="approval-main">
            <div class="approval-title">
              <strong>{{ approval.plan.kind }}</strong>
              <span class="settings-badge risk-badge">{{ t('settings.approvals.risk') }} {{ approval.plan.risk_level }}</span>
            </div>
            <p>{{ approval.plan.target_summary || approval.plan.target.deployment_id || approval.plan.target.template_path }}</p>
            <p class="fingerprint">{{ shortFingerprint(approval) }}</p>
          </div>
          <p class="approval-decided-by">{{ decidedLabel(approval) }}</p>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.approval-pane {
  width: 100%;
}
.approval-title,
.approval-actions,
.approval-policy-header,
.policy-toggle,
.policy-number {
  display: flex;
  align-items: center;
}
.approval-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.approval-item {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(220px, 1.4fr) auto;
  gap: 14px;
  align-items: start;
  padding: 12px;
}
.approval-title {
  gap: 8px;
  flex-wrap: wrap;
}
.approval-title strong {
  font-size: 13px;
}
.approval-main p {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
}
.approval-pane > .settings-empty,
.approval-pane > .settings-alert {
  margin: 0;
}
.approval-policy-card {
  display: grid;
  gap: 14px;
  margin-bottom: 14px;
  padding: 12px;
}
.approval-policy-header {
  justify-content: space-between;
  gap: 12px;
}
.approval-policy-header h2 {
  margin: 0;
  color: var(--text-primary);
  font-size: 14px;
}
.approval-policy-header p {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
}
.approval-policy-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px 14px;
}
.policy-toggle,
.policy-number {
  color: var(--text-secondary);
  font-size: 12px;
}
.policy-toggle {
  justify-content: flex-start;
  gap: 8px;
}
.policy-number {
  justify-content: space-between;
  gap: 10px;
}
.policy-toggle input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}
.approval-save-notice {
  margin: 0;
}
.policy-number input {
  width: 84px;
  min-height: 30px;
  padding: 0 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-primary);
}
.fingerprint {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.approval-details {
  display: grid;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}
.approval-details span {
  font-weight: 600;
}
ul {
  margin: 4px 0 0;
  padding-left: 16px;
}
.approval-actions {
  gap: 8px;
}
.approval-decided-section {
  display: grid;
  gap: 10px;
  margin-top: 18px;
}
.approval-decided-title {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 600;
}
/* 灰化：已有裁决方胜出的记录只用于回溯，不再需要用户操作，弱化视觉权重。 */
.approval-item-decided {
  grid-template-columns: minmax(180px, 1fr) auto;
  opacity: 0.62;
}
.approval-decided-by {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 11px;
  white-space: nowrap;
}
@media (max-width: 760px) {
  .approval-item {
    grid-template-columns: 1fr;
  }
  .approval-actions {
    justify-content: flex-start;
  }
}
</style>
