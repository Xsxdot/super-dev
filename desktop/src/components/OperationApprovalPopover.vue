<!--
操作审批快速浮层

职责：
  - 展示 pending operation approvals 的紧凑列表
  - 提供批准、拒绝、刷新和查看全部入口

边界：
  - 不计算审批策略
  - 不读取 approval token
  - 不替代设置页完整审批列表
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import type { OperationApproval } from '@/api/agent'

const emit = defineEmits<{ (event: 'view-all'): void }>()
const { t } = useI18n()
const store = useOperationApprovalStore()

function targetSummary(approval: OperationApproval): string {
  return approval.plan.target_summary
    || approval.plan.target.deployment_id
    || approval.plan.target.template_path
    || approval.plan.kind
}

function shortItems(items?: string[]): string[] {
  return (items ?? []).slice(0, 2)
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
          <button
            type="button"
            class="approve-btn"
            :data-test="`approval-popover-approve-${approval.id}`"
            :disabled="store.loading"
            @click="store.approve(approval.id, '')"
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
  gap: 8px;
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
