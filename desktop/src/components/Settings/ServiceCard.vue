<!--
ServiceCard：单个 service 在某个 env 下的配置卡片。

职责：
  - 编辑 service 名称 / required
  - 展示该 env 下的 deployment（DeploymentForm）；无则显示「启用」占位
  - 删除服务
边界：
  - 一个 service 在一个 env 下至多一份 deployment（按 env_name 匹配）
  - 变更整份 service 草稿向上 emit
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Deployment, ServiceLanguage } from '@/api/agent'
import type { ConfigDraftService } from '@/lib/configDraft'
import DeploymentForm from './DeploymentForm.vue'

const props = defineProps<{
  service: ConfigDraftService
  envName: string
  hosts: Array<{ id: string; name: string }>
  /** 项目根目录，用于新建 deployment 时自动填入工作目录默认值 */
  projectPath?: string
}>()
const emit = defineEmits<{
  'update:service': [ConfigDraftService]
  'remove': []
}>()
const { t } = useAppI18n()

const dep = computed(() => props.service.deployments.find(d => d.env_name === props.envName))
const defaultWorkDir = computed(() =>
  props.projectPath && props.service.name ? `${props.projectPath}/${props.service.name}` : ''
)

function patchService(partial: Partial<ConfigDraftService>) {
  emit('update:service', { ...props.service, ...partial })
}

function setLanguage(language: string) {
  patchService({ language: language ? language as ServiceLanguage : undefined })
}

function enableDep() {
  const newDep: Deployment = {
    id: '',
    env_name: props.envName,
    location: 'local',
    control_mode: 'managed',
    runtime: { type: 'command', command: '', working_dir: defaultWorkDir.value },
    logs: { type: 'process' },
    status: '',
  }
  patchService({ deployments: [...props.service.deployments, newDep] })
}

function updateDep(updated: Deployment) {
  patchService({
    deployments: props.service.deployments.map(d => (d.env_name === props.envName ? updated : d)),
  })
}

function removeDep() {
  patchService({ deployments: props.service.deployments.filter(d => d.env_name !== props.envName) })
}
</script>

<template>
  <article class="service-card" data-test="service-card">
    <header class="svc-header">
      <input
        class="svc-name" :placeholder="t('settings.service.serviceNamePlaceholder')"
        :value="service.name" @input="patchService({ name: ($event.target as HTMLInputElement).value })"
      />
      <label class="svc-required">
        <input
          type="checkbox" :checked="service.required"
          @change="patchService({ required: ($event.target as HTMLInputElement).checked })"
        /> {{ t('common.required') }}
      </label>
      <label class="svc-language">
        <span>{{ t('settings.service.language') }}</span>
        <select
          data-test="service-language"
          :value="service.language ?? ''"
          @change="setLanguage(($event.target as HTMLSelectElement).value)"
        >
          <option value="">{{ t('settings.service.languageAuto') }}</option>
          <option value="go">Go</option>
          <option value="node">Node</option>
          <option value="python">Python</option>
        </select>
      </label>
      <button type="button" class="svc-remove" data-test="remove-service" @click="emit('remove')">{{ t('common.delete') }}</button>
    </header>

    <div v-if="!dep" class="svc-empty">
      {{ t('settings.service.unconfigured') }}
      <button type="button" class="enable-btn" data-test="enable-dep" @click="enableDep">{{ t('settings.service.enable') }}</button>
    </div>
    <div v-else class="svc-dep">
      <DeploymentForm
        :model-value="dep"
        :hosts="hosts"
        :default-work-dir="defaultWorkDir"
        @update:model-value="updateDep"
      />
      <button type="button" class="dep-remove" @click="removeDep">{{ t('settings.service.removeEnvConfig') }}</button>
    </div>
  </article>
</template>

<style scoped>
.service-card {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 10px;
}
.svc-header {
  display: flex;
  align-items: center;
  gap: 10px;
}
.svc-name {
  flex: 1;
  padding: 4px 8px;
  font-size: 13px;
  font-weight: 600;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
}
.svc-required {
  font-size: 11px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.svc-language {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
.svc-language select {
  height: 24px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 11px;
}
.svc-remove {
  padding: 3px 9px;
  background: transparent;
  border: 1px solid var(--border-secondary);
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.svc-empty {
  margin-top: 8px;
  font-size: 12px;
  color: var(--text-tertiary);
}
.enable-btn {
  margin-left: 8px;
  padding: 2px 10px;
  background: var(--accent);
  border: none;
  color: #fff;
  cursor: pointer;
  font-size: 11px;
}
.dep-remove {
  padding: 2px 8px;
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
</style>
