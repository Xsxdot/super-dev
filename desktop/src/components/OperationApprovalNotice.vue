<!--
操作审批提示组件

职责：
  - 在桌面端运行态操作触发审批时弹出全局通知
  - 允许用户直接在通知中批准或拒绝审批

边界：
  - 不计算审批策略
  - 不读取 approval token
  - 不直接执行运行态操作，审批后的续跑由 store 统一处理
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'

const { t } = useI18n()
const store = useOperationApprovalStore()

async function approveNotice() {
  const approvalID = store.notice?.approval_id
  if (!approvalID) return
  await store.approve(approvalID, '')
}

async function rejectNotice() {
  const approvalID = store.notice?.approval_id
  if (!approvalID) return
  await store.reject(approvalID, '')
}
</script>

<template>
  <aside v-if="store.notice" class="approval-notice" data-test="operation-approval-notice">
    <div class="notice-content">
      <div class="notice-copy">
        <strong>{{ t('settings.approvals.noticeTitle') }}</strong>
        <span>{{ store.notice.target_summary || store.notice.kind }}</span>
      </div>
      <div class="notice-actions">
        <button
          type="button"
          class="notice-primary"
          data-test="operation-approval-approve"
          :disabled="store.loading"
          @click="approveNotice"
        >
          {{ t('settings.approvals.approve') }}
        </button>
        <button
          type="button"
          class="notice-danger"
          data-test="operation-approval-reject"
          :disabled="store.loading"
          @click="rejectNotice"
        >
          {{ t('settings.approvals.reject') }}
        </button>
        <button type="button" class="notice-close" :title="t('common.close')" @click="store.clearNotice">
          ×
        </button>
      </div>
    </div>
    <p v-if="store.error" class="notice-error" data-test="operation-approval-error">{{ store.error }}</p>
  </aside>
</template>

<style scoped>
.approval-notice {
  position: fixed;
  right: 18px;
  bottom: 18px;
  z-index: 50;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 14px;
  max-width: min(420px, calc(100vw - 36px));
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.22);
  padding: 12px;
  color: var(--text-primary);
}
.notice-content {
  display: flex;
  align-items: center;
  gap: 14px;
}
.notice-copy {
  min-width: 0;
  display: grid;
  gap: 3px;
  flex: 1;
}
.notice-copy strong {
  font-size: 13px;
}
.notice-copy span {
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.notice-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.notice-primary,
.notice-danger,
.notice-close {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
}
.notice-primary:disabled,
.notice-danger:disabled {
  cursor: wait;
  opacity: 0.62;
}
.notice-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
  padding: 6px 10px;
}
.notice-danger {
  background: transparent;
  border-color: color-mix(in srgb, var(--danger) 36%, var(--border-secondary));
  color: var(--danger);
  padding: 6px 10px;
}
.notice-close {
  width: 28px;
  height: 28px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 18px;
  line-height: 1;
}
.notice-error {
  margin: -4px 0 0;
  color: var(--danger);
  font-size: 12px;
  line-height: 1.4;
  word-break: break-word;
}
@media (max-width: 560px) {
  .approval-notice {
    left: 12px;
    right: 12px;
    bottom: 12px;
  }
  .notice-content {
    align-items: stretch;
    flex-direction: column;
  }
  .notice-actions {
    justify-content: flex-end;
  }
}
</style>
