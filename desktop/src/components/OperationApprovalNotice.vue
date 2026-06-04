<!--
操作审批提示组件

职责：
  - 在桌面端运行态操作触发审批时给出明确提示
  - 提供跳转到设置页操作审批 tab 的入口

边界：
  - 不执行审批或运行态操作
  - 不读取 approval token
-->
<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useOperationApprovalStore } from '@/stores/operationApproval'

const router = useRouter()
const { t } = useI18n()
const store = useOperationApprovalStore()

async function openApprovals() {
  await router.push({ path: '/settings', query: { tab: 'approvals' } })
  store.clearNotice()
}
</script>

<template>
  <aside v-if="store.notice" class="approval-notice" data-test="operation-approval-notice">
    <div class="notice-copy">
      <strong>{{ t('settings.approvals.noticeTitle') }}</strong>
      <span>{{ store.notice.target_summary || store.notice.kind }}</span>
    </div>
    <div class="notice-actions">
      <button type="button" class="notice-primary" data-test="operation-approval-open" @click="openApprovals">
        {{ t('settings.approvals.noticeAction') }}
      </button>
      <button type="button" class="notice-close" :title="t('common.close')" @click="store.clearNotice">
        ×
      </button>
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
  align-items: center;
  gap: 14px;
  max-width: min(420px, calc(100vw - 36px));
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.22);
  padding: 12px;
  color: var(--text-primary);
}
.notice-copy {
  min-width: 0;
  display: grid;
  gap: 3px;
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
.notice-close {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
}
.notice-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
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
@media (max-width: 560px) {
  .approval-notice {
    left: 12px;
    right: 12px;
    bottom: 12px;
    align-items: stretch;
    flex-direction: column;
  }
  .notice-actions {
    justify-content: flex-end;
  }
}
</style>
