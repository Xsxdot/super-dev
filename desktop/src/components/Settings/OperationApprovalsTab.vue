<!--
操作审批 Tab

职责：
  - 展示待处理 operation approval
  - 提供批准/拒绝入口

边界：
  - 不计算风险等级
  - 不展示 approval token
-->
<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import type { OperationApproval } from '@/api/agent'

const { t } = useI18n()
const store = useOperationApprovalStore()

onMounted(() => {
  void store.loadPending()
})

function shortFingerprint(approval: OperationApproval): string {
  const fp = approval.plan.fingerprint || ''
  return fp.length > 18 ? `${fp.slice(0, 18)}...` : fp
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
    <p v-else-if="store.approvals.length === 0" class="settings-empty">{{ t('settings.approvals.empty') }}</p>

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
  </section>
</template>

<style scoped>
.approval-pane {
  width: 100%;
}
.approval-title,
.approval-actions {
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
@media (max-width: 760px) {
  .approval-item {
    grid-template-columns: 1fr;
  }
  .approval-actions {
    justify-content: flex-start;
  }
}
</style>
