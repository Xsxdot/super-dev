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
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Deployment, ServiceLanguage } from '@/api/agent'
import type { ConfigDraftService } from '@/lib/configDraft'
import { defaultManagedRuntime } from '@/lib/languageRuntimeDefaults'
import AIGuidanceFields from './AIGuidanceFields.vue'
import DeploymentForm from './DeploymentForm.vue'
import DebugCredentialEditor from './DebugCredentialEditor.vue'

const props = defineProps<{
  service: ConfigDraftService
  envName: string
  hosts: Array<{ id: string; name: string }>
  /** 项目根目录，用于新建 deployment 时自动填入工作目录默认值 */
  projectPath?: string
  /** 同 env 下可被当前 service 依赖的本机接管服务 */
  siblingServices?: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{
  'update:service': [ConfigDraftService]
  'remove': []
}>()
const { t } = useAppI18n()

const latestService = ref<ConfigDraftService>(props.service)
const dep = computed(() => props.service.deployments.find(d => d.env_name === props.envName))
const defaultWorkDir = computed(() =>
  props.projectPath && props.service.name ? `${props.projectPath}/${props.service.name}` : ''
)

watch(
  () => props.service,
  value => {
    latestService.value = value
  },
)

function patchService(partial: Partial<ConfigDraftService>) {
  const next = { ...latestService.value, ...partial }
  latestService.value = next
  emit('update:service', next)
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
    runtime: defaultManagedRuntime(props.service.language, defaultWorkDir.value),
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
    <div v-else class="svc-dep" data-test="service-config-grid">
      <DeploymentForm
        :model-value="dep"
        :hosts="hosts"
        :default-work-dir="defaultWorkDir"
        :service-language="service.language"
        :sibling-services="siblingServices ?? []"
        @update:model-value="updateDep"
      >
        <template #side-top>
          <AIGuidanceFields
            class="service-ai-guidance-panel"
            :title="t('settings.aiGuidance.serviceTitle')"
            :hint="t('settings.aiGuidance.serviceHint')"
            :ai-note="service.ai_note"
            :auth-hint="service.auth_hint"
            test-prefix="service"
            @update:ai-note="patchService({ ai_note: $event })"
            @update:auth-hint="patchService({ auth_hint: $event })"
          />
          <!-- 服务级凭据只属于配置编辑；overview/list 不渲染该明文字段。 -->
          <DebugCredentialEditor
            :model-value="service.debug_credentials ?? []"
            class="service-credential-panel"
            data-test="service-debug-credentials"
            :title="t('settings.debugCredentials.serviceTitle')"
            :hint="t('settings.debugCredentials.serviceHint')"
            @update:model-value="patchService({ debug_credentials: $event })"
          />
        </template>
      </DeploymentForm>
      <div class="svc-danger-row">
        <button type="button" class="dep-remove" data-test="remove-env-config" @click="removeDep">{{ t('settings.service.removeEnvConfig') }}</button>
      </div>
    </div>
  </article>
</template>

<style scoped>
.service-card {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  margin-bottom: 10px;
  overflow: hidden;
  background: rgba(8, 13, 20, 0.72);
}
.svc-header {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--border-secondary);
  background: rgba(16, 24, 35, 0.72);
}
.svc-name {
  flex: 1;
  min-width: 180px;
  padding: 7px 10px;
  font-size: 18px;
  font-weight: 650;
  background: transparent;
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
  min-width: 82px;
  height: 32px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 11px;
}
.svc-remove {
  padding: 7px 11px;
  background: transparent;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 12px;
  font-weight: 650;
}
.svc-empty {
  padding: 18px;
  font-size: 12px;
  color: var(--text-tertiary);
}
.enable-btn {
  margin-left: 8px;
  padding: 6px 10px;
  background: var(--accent);
  border-radius: 6px;
  border: none;
  color: #fff;
  cursor: pointer;
  font-size: 11px;
}
.svc-dep {
  display: grid;
  gap: 16px;
  padding: 18px;
}
.service-credential-panel {
  margin: 0;
}
.service-ai-guidance-panel {
  margin-bottom: 12px;
}
.svc-danger-row {
  display: flex;
  justify-content: flex-end;
}
.dep-remove {
  padding: 9px 14px;
  background: transparent;
  border: 1px solid rgba(255, 87, 87, 0.28);
  border-radius: 6px;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 12px;
  font-weight: 650;
}
.svc-dep :deep(.service-credential-panel) {
  border-top: 0;
  margin-top: 0;
}
@media (max-width: 760px) {
  .svc-header {
    align-items: stretch;
    flex-direction: column;
  }
  .svc-name {
    width: 100%;
  }
}
</style>
