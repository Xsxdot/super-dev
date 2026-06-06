<!--
HostFormModal：单 Host 身份信息新建与编辑表单。

职责：
  - 收集 Host 展示名、入口地址元数据和 tag 字段
  - 将 identity-only payload 交由父组件保存

边界：
  - 不收集 SSH 凭据、agent 端口或连接方式
  - 不直接调用远程 API
  - 不负责 Agent 安装或连接测试
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Host, HostCreatePayload } from '@/api/agent'
import TagInput from './TagInput.vue'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const { t } = useAppI18n()

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    public_ip: '',
    private_ip: '',
    tags: [],
  }
}

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    if (initial) {
      form.value = {
        name: initial.name,
        public_ip: initial.public_ip ?? '',
        private_ip: initial.private_ip ?? '',
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

function submit() {
  emit('submit', {
    name: form.value.name,
    public_ip: form.value.public_ip,
    private_ip: form.value.private_ip,
    tags: form.value.tags ?? [],
  })
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ initial ? t('settings.hosts.edit') : t('settings.hosts.add') }}</h2>
      </div>

      <div class="settings-modal-body host-form-body">
        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.name') }} <span class="req">*</span></label>
          <input v-model="form.name" class="settings-input" placeholder="nova-api-prod-01" data-test="host-form-name" />
        </div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.publicIP') }}</label>
            <input v-model="form.public_ip" class="settings-input" placeholder="203.0.113.10" data-test="host-form-public-ip" />
          </div>
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.privateIP') }}</label>
            <input v-model="form.private_ip" class="settings-input" placeholder="10.0.0.10" data-test="host-form-private-ip" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.tags') }}</label>
          <TagInput v-model="form.tags!" data-test="host-form-tags" />
        </div>
      </div>

      <div class="settings-modal-footer">
        <button type="button" class="settings-btn" data-test="host-form-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="settings-btn settings-btn-primary" data-test="host-form-submit" @click="submit">{{ t('common.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.host-form-body {
  display: grid;
  gap: 10px;
}
.req { color: var(--status-failed); }
.row { display: flex; gap: 8px; }
.settings-field.flex { flex: 1; }
</style>
