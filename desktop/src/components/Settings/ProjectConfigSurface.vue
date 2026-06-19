<!--
ProjectConfigSurface：项目运行配置的可复用编辑主体。

职责：
  - 持有项目配置草稿（深拷贝自 project），支持环境、服务、凭据和 deployment 配置
  - 提供内嵌页和 modal 共同使用的配置编辑 UI
  - 保存：校验 → 拍平为 SetupPayload → PUT /setup → reloadProject → emit saved
  - 取消：丢弃本地草稿并 emit cancel

边界：
  - 不编辑项目级流水线（由 ProjectPipelineEditor 负责）
  - 不负责创建/关闭 modal 外壳
  - 删除运行中 service 的最终守卫在后端
-->
<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type Project } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { projectToDraft, draftToPayload, validateDraftDetailed, formatValidationIssue, type ConfigDraft, type ConfigDraftService } from '@/lib/configDraft'
import type { ProjectConfigSurfaceState } from '@/stores/workspace'
import AIGuidanceFields from './AIGuidanceFields.vue'
import EnvTabBar from './EnvTabBar.vue'
import ServiceRail from './ServiceRail.vue'
import ServiceCard from './ServiceCard.vue'
import DebugCredentialEditor from './DebugCredentialEditor.vue'

const props = withDefaults(defineProps<{
  project: Project
  isNew?: boolean
  embedded?: boolean
  state?: ProjectConfigSurfaceState
}>(), {
  embedded: false,
})
const emit = defineEmits<{
  saved: [Project]
  cancel: []
  'update:state': [ProjectConfigSurfaceState]
}>()

const agentStore = useAgentStore()
const { t } = useI18n()
const draft = ref(props.state?.draft ? cloneDraft(props.state.draft) : projectToDraft(props.project))
const activeEnv = ref(props.state?.activeEnv ?? '')
const activeServiceId = ref(props.state?.activeServiceId ?? '')
const renamingEnv = ref(props.state?.renamingEnv ?? '')
const hosts = ref<Array<{ id: string; name: string }>>([])
const errors = ref<string[]>(props.state?.errors ? [...props.state.errors] : [])
const saving = ref(false)
const saveError = ref<string | null>(props.state?.saveError ?? null)

function cloneDraft(value: ConfigDraft): ConfigDraft {
  return JSON.parse(JSON.stringify(value))
}

function serviceMatchesActive(service: ConfigDraftService, index: number) {
  return Boolean(service.id && service.id === activeServiceId.value) || String(index) === activeServiceId.value
}

function selectDefaultEnvAndService(force = false) {
  const envs = draft.value.environments
  if (force || !envs.some(e => e.name === activeEnv.value)) {
    activeEnv.value = (envs.find(e => e.is_dev) ?? envs[0])?.name ?? ''
  }
  // 默认选中第一个服务，避免用户进入配置 tab 后还要再点一次；恢复草稿时保留用户离开前的选择。
  const first = draft.value.services[0]
  if (force || !draft.value.services.some(serviceMatchesActive)) {
    activeServiceId.value = first?.id || (draft.value.services.length > 0 ? '0' : '')
  }
}

function resetDraft(project: Project = props.project) {
  draft.value = projectToDraft(project)
  errors.value = []
  saveError.value = null
  renamingEnv.value = ''
  selectDefaultEnvAndService(true)
}

function emitState() {
  emit('update:state', {
    draft: cloneDraft(draft.value),
    activeEnv: activeEnv.value,
    activeServiceId: activeServiceId.value,
    renamingEnv: renamingEnv.value,
    errors: [...errors.value],
    saveError: saveError.value,
  })
}

onMounted(async () => {
  selectDefaultEnvAndService()
  try {
    const list = await api.listHosts()
    hosts.value = list.filter(h => !h.is_self).map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

// agentStore 会按轮询频率用同 id 的新对象刷新 project；编辑中不能因此覆盖未保存草稿。
// 真正切换到另一个项目时才重建草稿。
watch(() => props.project.id, () => resetDraft())

watch([draft, activeEnv, activeServiceId, renamingEnv, errors, saveError], emitState, { deep: true })

const currentServices = computed(() => draft.value.services)
const currentEnv = computed(() => draft.value.environments.find(e => e.name === activeEnv.value) ?? null)

// activeService：优先按 id 匹配，id 为空时按索引字符串匹配
const activeService = computed<ConfigDraftService | null>(() => {
  const byId = draft.value.services.find(s => s.id && s.id === activeServiceId.value)
  if (byId) return byId
  const n = Number(activeServiceId.value)
  return isNaN(n) ? draft.value.services[0] ?? null : draft.value.services[n] ?? null
})

const activeServiceIndex = computed<number>(() => {
  const byId = draft.value.services.findIndex(s => s.id && s.id === activeServiceId.value)
  if (byId >= 0) return byId
  const n = Number(activeServiceId.value)
  return isNaN(n) ? 0 : n
})

function hasLocalManagedDeployment(service: ConfigDraftService, envName: string) {
  const dep = service.deployments.find(d => d.env_name === envName)
  if (!dep) return false
  const mode = dep.control_mode ?? (dep.read_only ? 'monitor' : 'managed')
  return dep.location === 'local' && mode === 'managed'
}

const activeSiblingServices = computed(() => {
  const current = activeService.value
  if (!current) return []
  return draft.value.services
    .filter(service => service !== current && Boolean(service.id))
    .filter(service => hasLocalManagedDeployment(service, activeEnv.value))
    .map(service => ({ id: service.id, name: service.name || service.id }))
})

function addEnv() {
  const base = 'env'
  let name = base
  let n = 1
  const taken = new Set(draft.value.environments.map(e => e.name))
  while (taken.has(name)) name = `${base}${n++}`
  draft.value.environments.push({ id: '', name, is_dev: false, order: draft.value.environments.length })
  activeEnv.value = name
  renamingEnv.value = name // 新增后立即进入改名态
}

function removeEnv(name: string) {
  draft.value.environments = draft.value.environments.filter(e => e.name !== name)
  for (const s of draft.value.services) {
    s.deployments = s.deployments.filter(d => d.env_name !== name)
  }
  if (activeEnv.value === name) {
    activeEnv.value = draft.value.environments[0]?.name ?? ''
  }
}

function renameEnv(oldName: string, newName: string) {
  const env = draft.value.environments.find(e => e.name === oldName)
  if (!env) return
  // 重名时拒绝，避免 deployment 的 env_name 引用变得不确定。
  if (draft.value.environments.some(e => e.name === newName)) return
  env.name = newName
  // 同步所有 deployment 的 env_name 引用，否则 deployment 和环境脱钩。
  for (const s of draft.value.services) {
    for (const d of s.deployments) {
      if (d.env_name === oldName) d.env_name = newName
    }
  }
  if (activeEnv.value === oldName) activeEnv.value = newName
  renamingEnv.value = ''
}

function updateActiveEnvName(value: string) {
  const next = value.trim()
  if (!next || next === activeEnv.value) return
  renameEnv(activeEnv.value, next)
}

function toggleDev(name: string) {
  const env = draft.value.environments.find(e => e.name === name)
  if (env) env.is_dev = !env.is_dev
}

function setActiveEnvDev(value: boolean) {
  const env = currentEnv.value
  if (env) env.is_dev = value
}

function addService() {
  const newSvc: ConfigDraftService = {
    id: '',
    name: '',
    required: false,
    order: draft.value.services.length,
    debug_credentials: [],
    deployments: [],
  }
  draft.value.services.push(newSvc)
  activeServiceId.value = String(draft.value.services.length - 1)
}

function updateService(i: number, svc: ConfigDraftService) {
  draft.value.services[i] = svc
}

function removeService(i: number) {
  draft.value.services.splice(i, 1)
  // 删除后选中前一个或第一个。
  const next = draft.value.services[i] ?? draft.value.services[i - 1] ?? draft.value.services[0]
  if (next) {
    activeServiceId.value = next.id || String(draft.value.services.indexOf(next))
  } else {
    activeServiceId.value = ''
  }
}

function selectService(id: string) {
  activeServiceId.value = id
}

function configValidationErrors(): string[] {
  // 项目级流水线已拆到独立编辑器，配置保存只拦截环境/服务/运行日志相关错误。
  return validateDraftDetailed(draft.value)
    .filter(error => error.scope === 'config')
    .map(formatValidationIssue)
}

function cancel() {
  resetDraft()
  emit('cancel')
}

async function save() {
  errors.value = configValidationErrors()
  if (errors.value.length) return
  saving.value = true
  saveError.value = null
  try {
    const updated = await api.putProjectSetup(props.project.id, draftToPayload(draft.value))
    await agentStore.reloadProject(props.project.id)
    const reloaded = agentStore.projectById(props.project.id)
    if (reloaded) {
      // 保存成功后同步后端生成的 ID 等规范化字段，避免后续继续编辑旧草稿。
      resetDraft(reloaded)
    }
    emit('saved', updated)
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : t('common.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="project-config-surface" :class="{ embedded }" data-test="project-config-surface">
    <div class="config-scroll">
      <ul v-if="errors.length" class="settings-alert settings-alert-danger err-list">
        <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
      </ul>
      <div v-if="saveError" class="settings-alert settings-alert-danger err-list">{{ saveError }}</div>

      <section class="config-env-shell">
        <AIGuidanceFields
          class="project-ai-guidance"
          :title="t('settings.aiGuidance.projectTitle')"
          :hint="t('settings.aiGuidance.projectHint')"
          :ai-note="draft.ai_note"
          :auth-hint="draft.auth_hint"
          test-prefix="project"
          @update:ai-note="draft.ai_note = $event"
          @update:auth-hint="draft.auth_hint = $event"
        />
        <EnvTabBar
          :environments="draft.environments"
          :active="activeEnv"
          :renamingEnv="renamingEnv"
          @update:active="activeEnv = $event"
          @add-env="addEnv"
          @remove-env="removeEnv"
          @rename-env="renameEnv"
          @toggle-dev="toggleDev"
          @start-rename="renamingEnv = $event"
        />
        <div v-if="currentEnv" class="env-settings" data-test="env-settings">
          <div class="settings-field env-name-field">
            <label class="settings-field-label">{{ t('settings.env.name') }}</label>
            <input
              class="settings-input"
              data-test="env-name-input"
              :value="currentEnv.name"
              @change="updateActiveEnvName(($event.target as HTMLInputElement).value)"
              @blur="updateActiveEnvName(($event.target as HTMLInputElement).value)"
            />
          </div>
          <label class="env-dev-toggle">
            <input
              type="checkbox"
              data-test="env-is-dev"
              :checked="currentEnv.is_dev"
              @change="setActiveEnvDev(($event.target as HTMLInputElement).checked)"
            />
            <span>{{ t('settings.env.setDev') }}</span>
          </label>
          <button type="button" class="env-delete" data-test="env-remove-active" @click="removeEnv(currentEnv.name)">
            {{ t('settings.env.delete') }}
          </button>
          <AIGuidanceFields
            class="env-ai-guidance"
            :title="t('settings.aiGuidance.envTitle')"
            :hint="t('settings.aiGuidance.envHint')"
            :ai-note="currentEnv.ai_note"
            :auth-hint="currentEnv.auth_hint"
            test-prefix="env"
            @update:ai-note="currentEnv.ai_note = $event"
            @update:auth-hint="currentEnv.auth_hint = $event"
          />
        </div>
        <DebugCredentialEditor
          v-if="currentEnv?.is_dev"
          v-model="draft.debug_credentials"
          class="env-project-credentials"
          data-test="project-debug-credentials"
          :title="t('settings.debugCredentials.projectTitle')"
          :hint="t('settings.debugCredentials.projectHint')"
        />
      </section>

      <div class="editor-columns">
        <aside class="editor-left">
          <ServiceRail
            :services="currentServices"
            :activeId="activeServiceId"
            :envName="activeEnv"
            @select="selectService"
            @add="addService"
            @remove="removeService"
          />
        </aside>
        <main class="editor-right">
          <template v-if="activeService">
            <ServiceCard
              data-test="service-card"
              :service="activeService"
              :env-name="activeEnv"
              :hosts="hosts"
              :project-path="project.root_path"
              :sibling-services="activeSiblingServices"
              @update:service="updateService(activeServiceIndex, $event)"
              @remove="removeService(activeServiceIndex)"
            />
          </template>
          <div v-else class="editor-empty">{{ t('settings.service.addPrompt') }}</div>
        </main>
      </div>
    </div>

    <div class="config-actions">
      <button type="button" class="settings-btn" data-test="config-cancel" @click="cancel">{{ t('common.cancel') }}</button>
      <button type="button" class="settings-btn settings-btn-primary" data-test="config-save" :disabled="saving" @click="save">
        {{ saving ? t('common.loading') : t('common.save') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.project-config-surface {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  color: var(--text-primary);
}
.config-scroll {
  display: grid;
  align-content: start;
  flex: 1;
  grid-auto-rows: max-content;
  min-height: 0;
  gap: 14px;
  overflow: auto;
  padding: 0;
}
.project-config-surface.embedded .config-scroll {
  padding: 18px 22px 0;
}
.err-list {
  margin: 0;
  list-style: none;
}
.config-env-shell,
.editor-right {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: rgba(13, 19, 28, 0.78);
}
.config-env-shell {
  padding: 12px 14px 14px;
}
.project-ai-guidance {
  margin-bottom: 12px;
}
.env-project-credentials {
  margin-top: 12px;
}
.env-settings {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto auto;
  gap: 12px;
  align-items: end;
  padding-top: 4px;
}
.env-name-field {
  margin: 0;
}
.env-ai-guidance {
  grid-column: 1 / -1;
}
.env-dev-toggle {
  display: inline-flex;
  align-items: center;
  min-height: 34px;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}
.env-delete {
  min-height: 34px;
  padding: 0 10px;
  border: 1px solid rgba(255, 87, 87, 0.28);
  border-radius: 6px;
  background: transparent;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 12px;
  font-weight: 650;
}
.editor-columns {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  gap: 18px;
  min-height: 320px;
}
.editor-left {
  min-width: 0;
  border-right: 1px solid var(--border-secondary);
  padding-right: 14px;
}
.editor-right {
  min-width: 0;
  padding: 14px;
}
.editor-empty {
  color: var(--text-tertiary);
  font-size: 12px;
  padding: 20px 0;
}
.config-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  flex-shrink: 0;
  padding: 14px 0 0;
}
.project-config-surface.embedded .config-actions {
  padding: 14px 22px 18px;
  border-top: 1px solid var(--border-secondary);
  background: rgba(7, 11, 17, 0.94);
}
@media (max-width: 860px) {
  .env-settings,
  .editor-columns {
    grid-template-columns: 1fr;
  }
  .editor-left {
    border-right: 0;
    border-bottom: 1px solid var(--border-secondary);
    padding-right: 0;
    padding-bottom: 12px;
  }
}
</style>
