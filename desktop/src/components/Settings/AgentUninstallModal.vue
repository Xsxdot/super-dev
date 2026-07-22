<!--
AgentUninstallModal：远端 Agent 卸载确认弹窗。

职责：
  - 说明 Agent 卸载对子进程与独立 Host Runtime 的影响
  - 默认保留 Agent 数据，并将数据清除作为独立显式选择
  - 对不可恢复的数据清除要求 Host 名称二次确认

边界：
  - 不发送 HTTP 请求，仅向父组件返回用户确认的 purge 选择
  - 不展示手动卸载或仅移除配置入口
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  visible: boolean
  hostName: string
  busy?: boolean
}>()

const emit = defineEmits<{
  cancel: []
  confirm: [removeData: boolean]
}>()

const { t } = useI18n()
const purge = ref(false)
const confirmName = ref('')

const canConfirm = computed(() => !props.busy && (!purge.value || confirmName.value === props.hostName))

watch(() => props.visible, visible => {
  if (!visible) return
  purge.value = false
  confirmName.value = ''
})

watch(purge, enabled => {
  // 关闭 purge 后清除已输入的名称，避免下次勾选时绕过强确认。
  if (!enabled) confirmName.value = ''
})

function submit() {
  if (!canConfirm.value) return
  emit('confirm', purge.value)
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" data-test="agent-uninstall-modal" @click.self="emit('cancel')">
    <section class="settings-modal" role="dialog" aria-modal="true" aria-labelledby="agent-uninstall-title">
      <header class="settings-modal-header">
        <h2 id="agent-uninstall-title" class="settings-modal-title">{{ t('settings.agents.uninstallTitle', { name: hostName }) }}</h2>
        <button class="settings-btn settings-btn-icon" type="button" :disabled="busy" @click="emit('cancel')">×</button>
      </header>

      <div class="settings-modal-body uninstall-body">
        <p>{{ t('settings.agents.uninstallImpactAgent') }}</p>
        <p>{{ t('settings.agents.uninstallImpactIndependent') }}</p>
        <p class="keep-data">{{ t('settings.agents.uninstallKeepData') }}</p>

        <label class="purge-choice">
          <input v-model="purge" type="checkbox" data-test="agent-uninstall-purge" :disabled="busy" />
          <span>{{ t('settings.agents.uninstallRemoveData') }}</span>
        </label>

        <div v-if="purge" class="settings-alert settings-alert-danger purge-confirm" data-test="agent-uninstall-purge-warning">
          <strong>{{ t('settings.agents.uninstallPurgeWarning') }}</strong>
          <label>
            <span>{{ t('settings.agents.uninstallPurgeConfirmName', { name: hostName }) }}</span>
            <input
              v-model="confirmName"
              class="settings-input"
              type="text"
              autocomplete="off"
              data-test="agent-uninstall-confirm-name"
              :disabled="busy"
            />
          </label>
        </div>
      </div>

      <footer class="settings-modal-footer">
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="busy" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button
          class="settings-btn settings-btn-primary"
          type="button"
          data-test="agent-uninstall-confirm"
          :disabled="!canConfirm"
          @click="submit"
        >
          {{ busy ? t('settings.agents.uninstalling') : t('settings.agents.uninstallConfirmAction') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.uninstall-body {
  display: grid;
  gap: 10px;
}
.uninstall-body p {
  margin: 0;
  color: var(--text-secondary);
  line-height: 1.55;
}
.keep-data {
  font-weight: 600;
}
.purge-choice,
.purge-confirm label {
  display: flex;
  gap: 8px;
}
.purge-choice {
  align-items: center;
  cursor: pointer;
}
.purge-confirm {
  display: grid;
  gap: 10px;
}
.purge-confirm label {
  flex-direction: column;
}
</style>
