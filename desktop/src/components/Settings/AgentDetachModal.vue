<!--
AgentDetachModal：仅移除 Controller Agent 配置的风险确认弹窗。

职责：
  - 明确说明 Detach 不会卸载远端 Agent
  - 警告远端 Agent 及其子进程可能继续运行
  - 要求用户通过独立确认动作承担解除纳管风险

边界：
  - 不发送 HTTP 请求，仅向父组件发出确认事件
  - 不在正常卸载路径展示，也不把 Detach 表述为卸载成功
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{
  visible: boolean
  hostName: string
  busy?: boolean
}>()

const emit = defineEmits<{
  cancel: []
  confirm: []
}>()

const { t } = useI18n()
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" data-test="agent-detach-modal" @click.self="emit('cancel')">
    <section class="settings-modal" role="dialog" aria-modal="true" aria-labelledby="agent-detach-title">
      <header class="settings-modal-header">
        <h2 id="agent-detach-title" class="settings-modal-title">{{ t('settings.agents.detachTitle', { name: hostName }) }}</h2>
        <button class="settings-btn settings-btn-icon" type="button" :disabled="busy" @click="emit('cancel')">×</button>
      </header>

      <div class="settings-modal-body detach-body">
        <div class="settings-alert settings-alert-danger" data-test="agent-detach-warning">
          <strong>{{ t('settings.agents.detachRemoteWarning') }}</strong>
        </div>
        <p>{{ t('settings.agents.detachBoundary') }}</p>
        <p>{{ t('settings.agents.detachHostKept') }}</p>
      </div>

      <footer class="settings-modal-footer">
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="busy" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button
          class="settings-btn settings-btn-danger"
          type="button"
          data-test="agent-detach-confirm"
          :disabled="busy"
          @click="emit('confirm')"
        >
          {{ busy ? t('settings.agents.detaching') : t('settings.agents.detachConfirmAction') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.detach-body {
  display: grid;
  gap: 10px;
}
.detach-body p {
  margin: 0;
  color: var(--text-secondary);
  line-height: 1.55;
}
</style>
