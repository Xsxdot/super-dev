<!--
操作审批快速浮层

职责：
  - 展示 pending operation approvals 的紧凑列表
  - 提供批准、拒绝、刷新和查看全部入口
  - 对带 request_origin/pairing_code 的审批（纳管请求）同样展示服务器侧推导的
    来源与配对码——本浮层也能直接批准，防伪要素不能只在设置页出现

边界：
  - 不计算审批策略
  - 不读取 approval token
  - 不替代设置页完整审批列表
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'
import type { OperationApproval } from '@/api/agent'

const emit = defineEmits<{ (event: 'view-all'): void }>()
const { t } = useI18n()
const store = useOperationApprovalStore()
const settingsStore = useSettingsStore()
const graceSelections = ref<Record<string, boolean>>({})
const graceMessage = ref('')
const graceMinutes = computed(() => settingsStore.agentSettings.approval?.grace_minutes ?? 15)

function targetSummary(approval: OperationApproval): string {
  return approval.plan.target_summary
    || approval.plan.target.deployment_id
    || approval.plan.target.template_path
    || approval.plan.kind
}

function shortItems(items?: string[]): string[] {
  return (items ?? []).slice(0, 2)
}

function canGrantGrace(approval: OperationApproval): boolean {
  return !!approval.plan.target.project_id
}

async function approveApproval(approval: OperationApproval) {
  graceMessage.value = ''
  const grantGrace = canGrantGrace(approval) && graceSelections.value[approval.id] === true
  const decision = await store.approve(approval.id, grantGrace ? { grantGrace: true } : '')
  if (decision?.grace_granted) {
    graceSelections.value[approval.id] = false
    graceMessage.value = t('settings.approvals.graceGranted', { minutes: graceMinutes.value })
  }
}
</script>

<template>
  <aside class="approval-popover" data-test="operation-approval-popover">
    <header class="popover-header">
      <div class="popover-title">
        <strong>{{ t('settings.approvals.quickTitle') }}</strong>
        <span>{{ t('settings.approvals.pendingCount', { count: store.pendingCount }) }}</span>
      </div>
      <button
        type="button"
        class="popover-refresh"
        data-test="approval-popover-refresh"
        :disabled="store.loading"
        @click="store.loadPending()"
      >
        {{ t('settings.approvals.refresh') }}
      </button>
    </header>

    <p v-if="store.error" class="popover-error" data-test="approval-popover-error">{{ store.error }}</p>
    <p v-if="graceMessage" class="popover-success" data-test="approval-popover-grace-message">{{ graceMessage }}</p>
    <p v-if="store.approvals.length === 0" class="popover-empty" data-test="approval-popover-empty">
      {{ t('settings.approvals.empty') }}
    </p>

    <div v-else class="popover-list">
      <article v-for="approval in store.approvals" :key="approval.id" class="approval-row">
        <div class="approval-row-main">
          <div class="approval-row-title">
            <strong>{{ approval.plan.kind }}</strong>
            <span>{{ t('settings.approvals.risk') }} {{ approval.plan.risk_level }}</span>
          </div>
          <p>{{ targetSummary(approval) }}</p>
          <!--
            浮层同样能直接批准，所以纳管审批的防伪要素（服务器侧推导的来源 +
            配对码）必须在这里也出现——只在设置页展示等于给「从浮层批错行」
            留了后门。
          -->
          <p v-if="approval.plan.target.request_origin" class="approval-row-origin" :data-test="`approval-popover-origin-${approval.id}`">
            {{ t('settings.approvals.requestOrigin', { origin: approval.plan.target.request_origin }) }}
          </p>
          <p v-if="approval.plan.target.pairing_code" class="approval-row-pairing" :data-test="`approval-popover-pairing-code-${approval.id}`">
            {{ t('settings.approvals.pairingCode', { code: approval.plan.target.pairing_code }) }}
          </p>
          <ul v-if="shortItems(approval.plan.reasons).length || shortItems(approval.plan.expected_effects).length">
            <li v-for="reason in shortItems(approval.plan.reasons)" :key="`reason-${approval.id}-${reason}`">
              {{ reason }}
            </li>
            <li v-for="effect in shortItems(approval.plan.expected_effects)" :key="`effect-${approval.id}-${effect}`">
              {{ effect }}
            </li>
          </ul>
        </div>

        <div class="approval-row-actions">
          <label
            class="grace-option"
            :class="{ disabled: !canGrantGrace(approval) }"
            :title="canGrantGrace(approval) ? '' : t('settings.approvals.graceUnavailable')"
          >
            <input
              v-model="graceSelections[approval.id]"
              type="checkbox"
              :data-test="`approval-popover-grace-${approval.id}`"
              :disabled="store.loading || !canGrantGrace(approval)"
            >
            <span>{{ t('settings.approvals.grantGrace', { minutes: graceMinutes }) }}</span>
          </label>
          <button
            type="button"
            class="approve-btn"
            :data-test="`approval-popover-approve-${approval.id}`"
            :disabled="store.loading"
            @click="approveApproval(approval)"
          >
            {{ t('settings.approvals.approve') }}
          </button>
          <button
            type="button"
            class="reject-btn"
            :data-test="`approval-popover-reject-${approval.id}`"
            :disabled="store.loading"
            @click="store.reject(approval.id, '')"
          >
            {{ t('settings.approvals.reject') }}
          </button>
        </div>
      </article>
    </div>

    <footer class="popover-footer">
      <button type="button" data-test="approval-popover-view-all" @click="emit('view-all')">
        {{ t('settings.approvals.viewAll') }}
      </button>
    </footer>
  </aside>
</template>

<style scoped>
.approval-popover {
  position: fixed;
  right: 16px;
  bottom: 84px;
  z-index: 60;
  display: grid;
  gap: 10px;
  width: min(380px, calc(100vw - 24px));
  max-height: min(420px, calc(100vh - 120px));
  padding: 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 18px 44px rgba(0, 0, 0, 0.34);
  color: var(--text-primary);
}

.popover-header,
.approval-row-title,
.approval-row-actions,
.popover-footer {
  display: flex;
  align-items: center;
}

.popover-header {
  justify-content: space-between;
  gap: 12px;
}

.popover-title {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.popover-title strong {
  font-size: 13px;
}

.popover-title span,
.approval-row-title span,
.approval-row-main p,
.approval-row-main li {
  color: var(--text-secondary);
  font-size: 11px;
}

.popover-refresh,
.popover-footer button,
.approve-btn,
.reject-btn {
  min-height: 28px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}

.popover-refresh,
.popover-footer button {
  padding: 0 9px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
}

.popover-refresh:hover,
.popover-footer button:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.popover-refresh:disabled,
.approve-btn:disabled,
.reject-btn:disabled {
  cursor: wait;
  opacity: 0.62;
}

.popover-error,
.popover-success,
.popover-empty {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
}

.popover-error {
  color: var(--danger);
  word-break: break-word;
}

.popover-empty {
  color: var(--text-tertiary);
}

.popover-success {
  color: var(--success);
}

.popover-list {
  display: grid;
  gap: 8px;
  overflow-y: auto;
  min-height: 0;
}

.approval-row {
  display: grid;
  gap: 10px;
  padding: 10px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.025);
}

.approval-row-main {
  display: grid;
  gap: 5px;
  min-width: 0;
}
.approval-row-main p.approval-row-pairing {
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.08em;
}

.approval-row-title {
  justify-content: space-between;
  gap: 8px;
}

.approval-row-title strong,
.approval-row-main p,
.approval-row-main li {
  overflow: hidden;
  text-overflow: ellipsis;
}

.approval-row-title strong {
  font-size: 12px;
  white-space: nowrap;
}

.approval-row-title span {
  flex-shrink: 0;
}

.approval-row-main p {
  margin: 0;
  white-space: nowrap;
}

.approval-row-main ul {
  display: grid;
  gap: 3px;
  margin: 0;
  padding-left: 16px;
}

.approval-row-main li {
  line-height: 1.35;
}

.approval-row-actions {
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}

.grace-option {
  display: inline-flex;
  align-items: center;
  min-height: 28px;
  gap: 6px;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  line-height: 1.2;
}

.grace-option input {
  width: 14px;
  height: 14px;
  margin: 0;
  accent-color: var(--accent);
}

.grace-option.disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.approve-btn,
.reject-btn {
  padding: 0 10px;
}

.approve-btn {
  border-color: var(--accent);
  background: var(--accent);
  color: #fff;
}

.reject-btn {
  border-color: color-mix(in srgb, var(--danger) 36%, var(--border-secondary));
  background: transparent;
  color: var(--danger);
}

.popover-footer {
  justify-content: flex-end;
}

@media (max-width: 560px) {
  .approval-popover {
    left: 12px;
    right: 12px;
    bottom: 84px;
    width: auto;
  }
}
</style>
