<!--
操作审批提示组件

职责：
  - 在桌面端运行态操作触发审批时弹出全局通知
  - 允许用户直接在通知中批准或拒绝审批
  - 当当前通知对应的审批被其他控制面抢先裁决（conflictNotice）时，切换为
    灰化的「已由 X 处理」提示，隐藏批准/拒绝按钮——这是双控制面并发裁决的
    常态信息，不是错误

边界：
  - 不计算审批策略
  - 不读取 approval token
  - 不直接执行运行态操作，审批后的续跑由 store 统一处理
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'

const { t } = useI18n()
const store = useOperationApprovalStore()
const settingsStore = useSettingsStore()
const grantGrace = ref(false)
const graceMinutes = computed(() => settingsStore.agentSettings.approval?.grace_minutes ?? 15)
const canGrantGrace = computed(() => !!store.notice?.project_id && !store.notice?.approved)
// isConflict：当前弹出的通知对应的审批已被其他控制面抢先裁决——只在 conflictNotice
// 与当前 notice 指向同一个 approval_id 时成立，避免一个不相关的历史冲突误伤新通知。
const isConflict = computed(() => !!store.conflictNotice && store.conflictNotice.id === store.notice?.approval_id)

async function approveNotice() {
  const approvalID = store.notice?.approval_id
  if (!approvalID) return
  await store.approve(approvalID, canGrantGrace.value && grantGrace.value ? { grantGrace: true } : '')
  grantGrace.value = false
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
      <header class="notice-section notice-header" data-test="operation-approval-section-header">
        <div class="notice-title">
          <strong>
            {{ store.notice.approved ? t('settings.approvals.resumeFailedTitle') : t('settings.approvals.noticeTitle') }}
          </strong>
          <span>{{ store.notice.kind }}</span>
        </div>
        <button type="button" class="notice-close" :title="t('common.close')" @click="store.clearNotice">
          ×
        </button>
      </header>
      <section class="notice-section notice-body" data-test="operation-approval-section-body">
        <p>{{ store.notice.target_summary || store.notice.kind }}</p>
        <p v-if="isConflict" class="notice-conflict" data-test="operation-approval-conflict">
          {{ store.conflictNotice!.decidedBy
            ? t('settings.approvals.decidedBy', { name: store.conflictNotice!.decidedBy })
            : t('settings.approvals.decidedUnnamed') }}
        </p>
        <template v-else>
          <label v-if="canGrantGrace" class="notice-grace">
            <input
              v-model="grantGrace"
              type="checkbox"
              data-test="operation-approval-grace"
              :disabled="store.loading"
            >
            <span>{{ t('settings.approvals.grantGrace', { minutes: graceMinutes }) }}</span>
          </label>
          <p v-if="store.error" class="notice-error" data-test="operation-approval-error">{{ store.error }}</p>
        </template>
      </section>
      <footer v-if="!isConflict" class="notice-section notice-actions" data-test="operation-approval-section-actions">
        <button
          type="button"
          class="notice-primary"
          data-test="operation-approval-approve"
          :disabled="store.loading"
          @click="approveNotice"
        >
          {{ store.notice.approved ? t('settings.approvals.retry') : t('settings.approvals.approve') }}
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
      </footer>
    </div>
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
  gap: 0;
  width: min(360px, calc(100vw - 36px));
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.22);
  padding: 0;
  color: var(--text-primary);
  overflow: hidden;
}
.notice-content {
  display: grid;
}
.notice-section {
  min-width: 0;
  padding: 12px 14px;
}
.notice-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.14);
}
.notice-title {
  display: grid;
  gap: 4px;
  min-width: 0;
}
.notice-title strong {
  font-size: 13px;
  line-height: 1.35;
}
.notice-title span,
.notice-body p {
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.notice-body {
  display: grid;
  gap: 10px;
}
.notice-body p {
  margin: 0;
  line-height: 1.45;
}
.notice-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  border-top: 1px solid rgba(139, 148, 158, 0.14);
  background: rgba(255, 255, 255, 0.018);
}
.notice-grace {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.3;
}
.notice-grace input {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  accent-color: var(--accent);
}
.notice-primary,
.notice-danger,
.notice-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
  line-height: 1;
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
  flex: 0 0 28px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 18px;
}
.notice-error {
  margin: 0;
  color: var(--danger);
  font-size: 12px;
  line-height: 1.4;
  word-break: break-word;
}
.notice-conflict {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}
@media (max-width: 560px) {
  .approval-notice {
    left: 12px;
    right: 12px;
    bottom: 12px;
    width: auto;
  }
  .notice-actions {
    justify-content: flex-end;
  }
}
</style>
