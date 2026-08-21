<!--
ProjectConfigSurface：项目运行配置的可复用编辑主体。

职责：
  - 持有项目配置草稿（深拷贝自 project），支持环境、服务、凭据和 deployment 配置
  - 提供内嵌页和 modal 共同使用的配置编辑 UI
  - 保存：校验 → 拍平为 SetupPayload → PUT /setup → reloadProject → emit saved
  - 取消：丢弃本地草稿并 emit cancel
  - project 仍是 legacy 配置格式时展示迁移横幅，打开 ConfigMigrationDialog；
    迁移成功后走 reloadProject → resetDraft 同一条刷新路径
  - 展示两条只读告警横幅：并存的陈旧 config.yaml（config_stale_legacy）、
    共享层里扫到的疑似密钥（shared_secret_warnings）

边界：
  - 告警横幅「不挡、只亮」：不禁用保存、不改草稿内容，只把风险摆到人眼前
  - 不编辑项目级流水线（由 ProjectPipelineEditor 负责）
  - 不负责创建/关闭 modal 外壳
  - 删除运行中 service 的最终守卫在后端
  - 不实现迁移预览/处置 UI（由 ConfigMigrationDialog 负责）
-->
<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type Project, type ProjectDataSourceBinding } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useDataSourceStore } from '@/stores/datasources'
import type { DryRunResult } from '@/api/datasources'
import { projectToDraft, draftToPayload, validateDraftDetailed, formatValidationIssue, type ConfigDraft, type ConfigDraftService } from '@/lib/configDraft'
import type { ProjectConfigSurfaceState } from '@/stores/workspace'
import AIGuidanceFields from './AIGuidanceFields.vue'
import EnvTabBar from './EnvTabBar.vue'
import ServiceRail from './ServiceRail.vue'
import ServiceCard from './ServiceCard.vue'
import DebugCredentialEditor from './DebugCredentialEditor.vue'
import ConfigMigrationDialog from './ConfigMigrationDialog.vue'

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
const dataSourceStore = useDataSourceStore()
const { t } = useI18n()
const draft = ref(props.state?.draft ? cloneDraft(props.state.draft) : projectToDraft(props.project))
const activeEnv = ref(props.state?.activeEnv ?? '')
const activeServiceId = ref(props.state?.activeServiceId ?? '')
const renamingEnv = ref(props.state?.renamingEnv ?? '')
const hosts = ref<Array<{ id: string; name: string }>>([])
const errors = ref<string[]>(props.state?.errors ? [...props.state.errors] : [])
const saving = ref(false)
const saveError = ref<string | null>(props.state?.saveError ?? null)
const showMigrationDialog = ref(false)
const dryRunResult = ref<DryRunResult | null>(null)
const dryRunLoading = ref(false)
const dryRunError = ref('')

function defaultProjectDataSourceBinding(): ProjectDataSourceBinding {
  return {
    postgres: { datasource_name: '', dev_database: '', terminate_connections: true },
    redis: { datasource_name: '' },
    max_concurrent_leases: 3,
    default_ttl_minutes: 30,
  }
}

function ensureProjectDataSourceBinding() {
  const source = draft.value.data_source_binding
  const defaults = defaultProjectDataSourceBinding()
  draft.value.data_source_binding = {
    postgres: { ...defaults.postgres, ...(source?.postgres ?? {}) },
    redis: { ...defaults.redis, ...(source?.redis ?? {}) },
    max_concurrent_leases: source?.max_concurrent_leases ?? defaults.max_concurrent_leases,
    default_ttl_minutes: source?.default_ttl_minutes ?? defaults.default_ttl_minutes,
  }
}

ensureProjectDataSourceBinding()

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
  ensureProjectDataSourceBinding()
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
  void dataSourceStore.load().catch(() => undefined)
  try {
    const list = await api.listHosts()
    hosts.value = list.filter(h => !h.is_self).map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

const dataSourceBinding = computed(() => draft.value.data_source_binding ?? defaultProjectDataSourceBinding())
const postgresDataSources = computed(() => dataSourceStore.sources.filter(source => source.kind === 'postgres'))
const redisDataSources = computed(() => dataSourceStore.sources.filter(source => source.kind === 'redis'))
const selectedPostgresDataSource = computed(() => postgresDataSources.value.find(
  source => source.name === dataSourceBinding.value.postgres?.datasource_name,
))

async function runDataSourceDryRun() {
  dryRunLoading.value = true
  dryRunError.value = ''
  dryRunResult.value = null
  try {
    dryRunResult.value = await dataSourceStore.dryRun(props.project.id)
  } catch (error) {
    dryRunError.value = error instanceof Error ? error.message : String(error)
  } finally {
    dryRunLoading.value = false
  }
}

// agentStore 会按轮询频率用同 id 的新对象刷新 project；编辑中不能因此覆盖未保存草稿。
// 真正切换到另一个项目时才重建草稿。
watch(() => props.project.id, () => resetDraft())

watch([draft, activeEnv, activeServiceId, renamingEnv, errors, saveError], emitState, { deep: true })

// sharedSecretWarnings 直接读 project 而非草稿：告警描述的是磁盘上那份入库
// 文件的现状，草稿里还没保存的改动不该让它提前消失或提前出现。
const sharedSecretWarnings = computed(() => props.project.shared_secret_warnings ?? [])

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

// handleMigrated 在迁移弹窗执行成功后刷新项目数据。
//
// 迁移把配置从单文件翻成两层，磁盘上的真实生效值（包括路径相对化后再解析
// 回来的 work_dir/env_file）只有重新从后端拉取才准确——与 save() 同一条
// 刷新路径（reloadProject → projectById → resetDraft），不能另起一套，
// 否则两处对「保存后如何让本地状态追上后端」的理解会分叉。
//
// 注意：这里不关闭弹窗。ConfigMigrationDialog 收到 applyConfigMigration 的
// 响应后会同步把自己的 phase 切到 'done' 再 emit migrated——如果这里把
// showMigrationDialog 置为 false，会和弹窗自身的 'done' 渲染落在同一个
// 同步栈里抢先触发 v-if 卸载，用户会在成功态还没画出来之前就被弹窗“消失”
// 打断，看不到写了哪些产物、备份去了哪。刷新照常立刻做（横幅会在弹窗背后
// 及时收敛），关闭交给用户自己点弹窗里的「关闭」按钮。
async function handleMigrated() {
  await agentStore.reloadProject(props.project.id)
  const reloaded = agentStore.projectById(props.project.id)
  if (reloaded) {
    resetDraft(reloaded)
  }
}
</script>

<template>
  <div class="project-config-surface" :class="{ embedded }" data-test="project-config-surface">
    <div class="config-scroll">
      <div v-if="project.config_format === 'legacy'" class="settings-alert settings-alert-warning migration-banner" data-test="config-migration-banner">
        <span>{{ t('configMigration.banner') }}</span>
        <button type="button" class="settings-btn settings-btn-primary" data-test="config-migration-open" @click="showMigrationDialog = true">
          {{ t('configMigration.bannerAction') }}
        </button>
      </div>
      <!--
        并存的 config.yaml：split 胜出，这份文件里的路径与密钥全部不生效。
        config_format 报的是 'split'，迁移横幅不会触发，只有这条能让人看出问题。
      -->
      <div v-if="project.config_stale_legacy" class="settings-alert settings-alert-warning" data-test="config-stale-legacy-banner">
        {{ t('configMigration.staleLegacyBanner') }}
      </div>
      <!--
        共享层疑似密钥：project.yaml 是入库文件，这些值下一次 commit 就出去了。
        「不挡、只亮」——只提示，不禁用保存，也不替用户改任何东西。
      -->
      <div
        v-if="sharedSecretWarnings.length"
        class="settings-alert settings-alert-warning shared-secret-banner"
        data-test="shared-secret-warning-banner"
      >
        <span>{{ t('configMigration.sharedSecretBanner', { count: sharedSecretWarnings.length }) }}</span>
        <ul class="shared-secret-list">
          <li v-for="(w, i) in sharedSecretWarnings" :key="`${w.scope}:${w.key}:${i}`" :data-test="`shared-secret-row-${i}`">
            <code class="settings-mono">{{ w.key }}</code>
            <code class="settings-mono shared-secret-masked">{{ w.masked_value }}</code>
            <span>{{ w.reason }}</span>
          </li>
        </ul>
      </div>
      <ul v-if="errors.length" class="settings-alert settings-alert-danger err-list">
        <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
      </ul>
      <div v-if="saveError" class="settings-alert settings-alert-danger err-list">{{ saveError }}</div>

      <!--
        数据源绑定写入共享层 project.yaml，随项目配置提交；管理连接密码只在机器层登记表中保存。
        这里因此只能选择数据源名和模板库名，绝不提供密码输入或把凭据带进项目草稿。
      -->
      <section class="settings-card project-data-source-card" data-test="project-data-source-binding">
        <div class="project-data-source-header">
          <div>
            <h2>{{ t('settings.projectDataSources.title') }}</h2>
            <p>{{ t('settings.projectDataSources.description') }}</p>
          </div>
          <button
            type="button"
            class="settings-btn settings-btn-secondary"
            data-test="project-data-source-dry-run"
            :disabled="dryRunLoading"
            @click="runDataSourceDryRun"
          >
            {{ dryRunLoading ? t('common.loading') : t('settings.projectDataSources.dryRun') }}
          </button>
        </div>
        <p v-if="!dataSourceStore.sources.length && !dataSourceStore.loading" class="settings-alert settings-alert-warning" data-test="project-data-source-register-hint">
          {{ t('settings.projectDataSources.registerHint') }}
        </p>
        <p v-if="dataSourceStore.error" class="settings-alert settings-alert-danger" data-test="project-data-source-error">
          {{ dataSourceStore.error }}
        </p>
        <div class="project-data-source-grid">
          <div class="project-data-source-kind">
            <h3>{{ t('settings.projectDataSources.postgres') }}</h3>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.projectDataSources.managementSource') }}</span>
              <select v-model="dataSourceBinding.postgres!.datasource_name" class="settings-input" data-test="project-pg-datasource">
                <option value="">{{ t('settings.projectDataSources.selectSource') }}</option>
                <option v-for="source in postgresDataSources" :key="source.id" :value="source.name">{{ source.name }}</option>
              </select>
            </label>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.projectDataSources.devDatabase') }}</span>
            <input v-model="dataSourceBinding.postgres!.dev_database" class="settings-input" data-test="project-pg-dev-database" :placeholder="t('settings.projectDataSources.devDatabasePlaceholder')">
            </label>
            <p class="project-data-source-template-hint" data-test="project-pg-template-hint">
              {{ t('settings.projectDataSources.templateHint', {
                size: selectedPostgresDataSource?.probe.facts?.template_size || t('settings.projectDataSources.templateSizeUnknown'),
                eta: selectedPostgresDataSource?.probe.facts?.estimated_clone_time || t('settings.projectDataSources.estimatedTimeUnknown'),
              }) }}
            </p>
            <label class="project-data-source-toggle">
              <input v-model="dataSourceBinding.postgres!.terminate_connections" type="checkbox" data-test="project-pg-terminate-connections">
              <span>{{ t('settings.projectDataSources.terminateConnections') }}</span>
            </label>
            <p class="settings-alert settings-alert-warning project-data-source-warning" data-test="project-pg-terminate-warning">
              {{ t('settings.projectDataSources.terminateWarning') }}
            </p>
          </div>
          <div class="project-data-source-kind">
            <h3>{{ t('settings.projectDataSources.redis') }}</h3>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.projectDataSources.managementSource') }}</span>
              <select v-model="dataSourceBinding.redis!.datasource_name" class="settings-input" data-test="project-redis-datasource">
                <option value="">{{ t('settings.projectDataSources.selectSource') }}</option>
                <option v-for="source in redisDataSources" :key="source.id" :value="source.name">{{ source.name }}</option>
              </select>
            </label>
          </div>
        </div>
        <div class="project-data-source-limits">
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.projectDataSources.maxConcurrentLeases') }}</span>
            <input v-model.number="dataSourceBinding.max_concurrent_leases" class="settings-input" data-test="project-max-concurrent-leases" type="number" min="1">
          </label>
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.projectDataSources.defaultTTL') }}</span>
            <input v-model.number="dataSourceBinding.default_ttl_minutes" class="settings-input" data-test="project-default-ttl-minutes" type="number" min="1">
          </label>
        </div>
        <div v-if="dryRunError" class="settings-alert settings-alert-danger" data-test="project-dry-run-error">{{ dryRunError }}</div>
        <div v-if="dryRunResult" class="settings-alert settings-alert-success project-dry-run-result" data-test="project-dry-run-result">
          <div v-for="plan in dryRunResult.plans" :key="`${plan.kind}:${plan.resource_name}`">
            <strong>{{ plan.kind }} · {{ plan.resource_name }}</strong>
            <ul><li v-for="step in plan.steps" :key="step">{{ step }}</li></ul>
          </div>
          <div v-for="dsn in dryRunResult.masked_dsns" :key="dsn"><code class="settings-mono">{{ dsn }}</code></div>
        </div>
      </section>

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

    <ConfigMigrationDialog
      v-if="showMigrationDialog"
      :project-id="project.id"
      @cancel="showMigrationDialog = false"
      @migrated="handleMigrated"
    />
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
.migration-banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.shared-secret-banner {
  display: grid;
  gap: 6px;
}
.shared-secret-list {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 11px;
}
.shared-secret-list li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.shared-secret-masked {
  color: var(--text-tertiary);
}
.config-env-shell,
.editor-right,
.project-data-source-card {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: rgba(13, 19, 28, 0.78);
}
.config-env-shell {
  padding: 12px 14px 14px;
}
.project-data-source-card {
  display: grid;
  gap: 14px;
  padding: 14px;
}
.project-data-source-header,
.project-data-source-grid,
.project-data-source-limits {
  display: grid;
  gap: 14px;
}
.project-data-source-header {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
}
.project-data-source-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.project-data-source-kind {
  display: grid;
  gap: 10px;
  align-content: start;
  padding: 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
}
.project-data-source-kind h3 {
  margin: 0;
  font-size: 13px;
}
.project-data-source-limits {
  grid-template-columns: repeat(2, minmax(140px, 240px));
}
.project-data-source-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}
.project-data-source-warning {
  margin: 0;
  font-size: 11px;
}
.project-dry-run-result {
  display: grid;
  gap: 6px;
  margin: 0;
}
.project-dry-run-result ul {
  margin: 4px 0;
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
  .editor-columns,
  .project-data-source-grid,
  .project-data-source-header,
  .project-data-source-limits {
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
