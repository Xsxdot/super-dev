<!--
PipelineEnvMatrix：流水线变量矩阵（全局默认值 + 环境覆盖 + 运行组变量）。

职责：
  - 编辑全局变量，对应 ProjectPipeline.Variables
  - 按环境编辑变量覆盖，对应 Environments[env].Variables
  - 将运行组作为特殊变量行展示，并按环境选择目标主机
  - 展示系统保留变量，点击复制 ${var}

边界：
  - 不保存配置，仅通过 update 事件回传
  - 不编辑部署目标只读信息，该职责由 DeployTargetReadonly 承担
  - 不负责解析或校验模板表达式
-->
<script setup lang="ts">
import { Icon } from '@iconify/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { PipelineEnvironment, ProjectPipelineRole } from '@/api/agent'

type HostOption = { id: string; name: string }

const props = withDefaults(defineProps<{
  variables: Record<string, string>
  environments: Record<string, PipelineEnvironment>
  reservedNames: string[]
  roles?: Record<string, ProjectPipelineRole>
  availableEnvironments?: string[]
  hosts?: HostOption[]
  standalone?: boolean
}>(), {
  standalone: true,
})

const emit = defineEmits<{
  'update:variables': [Record<string, string>]
  'update:environments': [Record<string, PipelineEnvironment>]
  'update:roles': [Record<string, ProjectPipelineRole>]
}>()

const { t } = useAppI18n()
const collapsed = ref(true)
const rootRef = ref<HTMLElement | null>(null)
const runGroupName = ref('')
const runGroupHostIds = ref<string[]>([])
const newGlobalName = ref('')
const newGlobalValue = ref('')
const copiedName = ref('')
const addHostPickerOpen = ref(false)
const openRolePickerKey = ref('')

const envNames = computed(() => Object.keys(props.environments ?? {}))
const availableEnvNames = computed(() => {
  const names = props.availableEnvironments?.length ? props.availableEnvironments : envNames.value
  return Array.from(new Set(names.filter(Boolean)))
})
const hasEnvironments = computed(() => envNames.value.length >= 1)
const globalEntries = computed(() => Object.entries(props.variables ?? {}))
const roleNames = computed(() => Object.keys(props.roles ?? {}).filter(name => name !== 'builder' && !name.endsWith('_runner')))
const runGroupHostSummary = computed(() => hostSummary(runGroupHostIds.value))

const summaryText = computed(() => {
  const globals = globalEntries.value.map(([name]) => name).join(', ') || '0'
  const envSummary = envNames.value.length ? envNames.value.join('/') : '0'
  return `${t('settings.pipeline.globalVars')} ${globalEntries.value.length}: ${globals} · ${t('settings.pipeline.envVars')} ${envSummary}`
})

// 矩阵行取全局变量和环境覆盖变量的并集，避免用户需要在两个区域来回找同一变量。
const envVarNames = computed(() => {
  const names = new Set<string>()
  for (const env of Object.values(props.environments ?? {})) {
    for (const name of Object.keys(env.variables ?? {})) {
      names.add(name)
    }
  }
  return [...names]
})

const variableRows = computed(() => {
  const names = new Set<string>()
  for (const [name] of globalEntries.value) names.add(name)
  for (const name of envVarNames.value) names.add(name)
  return [...names]
})

function varPlaceholder(name: string) {
  return '${' + name + '}'
}

async function copyVar(name: string) {
  await navigator.clipboard?.writeText(varPlaceholder(name))
  copiedName.value = name
}

function setGlobal(name: string, event: Event) {
  emit('update:variables', {
    ...props.variables,
    [name]: (event.target as HTMLInputElement).value,
  })
}

function addGlobalVar() {
  const name = newGlobalName.value.trim()
  if (!name) return
  emit('update:variables', {
    ...(props.variables ?? {}),
    [name]: newGlobalValue.value,
  })
  newGlobalName.value = ''
  newGlobalValue.value = ''
}

function setEnvVar(envName: string, varName: string, event: Event) {
  const env = props.environments[envName] ?? { variables: {} }
  emit('update:environments', {
    ...props.environments,
    [envName]: {
      ...env,
      variables: {
        ...(env.variables ?? {}),
        [varName]: (event.target as HTMLInputElement).value,
      },
    },
  })
}

function isReservedName(name: string) {
  return props.reservedNames.includes(name)
}

function deleteVariable(name: string) {
  if (isReservedName(name)) return
  const nextVariables = { ...(props.variables ?? {}) }
  delete nextVariables[name]

  const nextEnvironments: Record<string, PipelineEnvironment> = {}
  for (const [envName, env] of Object.entries(props.environments ?? {})) {
    const variables = { ...(env.variables ?? {}) }
    delete variables[name]
    nextEnvironments[envName] = { ...env, variables }
  }

  emit('update:variables', nextVariables)
  emit('update:environments', nextEnvironments)
}

function setEnvSelected(envName: string, checked: boolean) {
  // 流水线必须至少属于一个环境：取消最后一个环境时阻止，不 emit。
  if (!checked && envNames.value.length <= 1) return
  const next = { ...(props.environments ?? {}) }
  if (checked) {
    next[envName] = next[envName] ?? { variables: { env: envName } }
  } else {
    delete next[envName]
  }
  emit('update:environments', next)
}

function hostRoleKeys(host: HostOption) {
  return [host.id, host.name].filter(Boolean)
}

function displayHostName(ref: string) {
  return props.hosts?.find(host => hostRoleKeys(host).includes(ref))?.name ?? ref
}

function hostSummary(hostRefs: string[]) {
  if (hostRefs.length === 0) return t('settings.pipeline.selectHost')
  const labels = hostRefs.map(displayHostName)
  if (labels.length <= 2) return labels.join(', ')
  return `${labels.slice(0, 2).join(', ')} +${labels.length - 2}`
}

function roleHosts(roleName: string, envName: string) {
  const role = props.roles?.[roleName]
  if (!role) return []
  if (role.environments && Object.prototype.hasOwnProperty.call(role.environments, envName)) {
    return role.environments[envName] ?? []
  }
  return role.hosts ?? []
}

function roleHostLabels(roleName: string, envName: string) {
  return roleHosts(roleName, envName).map(displayHostName)
}

function rolePickerKey(envName: string, roleName: string) {
  return `${envName}::${roleName}`
}

function isRolePickerOpen(envName: string, roleName: string) {
  return openRolePickerKey.value === rolePickerKey(envName, roleName)
}

function toggleRolePicker(envName: string, roleName: string) {
  const key = rolePickerKey(envName, roleName)
  openRolePickerKey.value = openRolePickerKey.value === key ? '' : key
  if (openRolePickerKey.value) addHostPickerOpen.value = false
}

function toggleAddHostPicker() {
  addHostPickerOpen.value = !addHostPickerOpen.value
  if (addHostPickerOpen.value) openRolePickerKey.value = ''
}

function roleSource(roleName: string) {
  const role = props.roles?.[roleName]
  if (role?.from_service) {
    return t('settings.pipeline.fromService', { svc: role.from_service })
  }
  const count = envNames.value.reduce((sum, envName) => sum + roleHosts(roleName, envName).length, 0)
  return t('settings.pipeline.hostCount', { count })
}

function isRoleHostChecked(roleName: string, envName: string, host: HostOption) {
  const selected = new Set(roleHosts(roleName, envName))
  return hostRoleKeys(host).some(key => selected.has(key))
}

function isRunGroupHostChecked(host: HostOption) {
  const selected = new Set(runGroupHostIds.value)
  return hostRoleKeys(host).some(key => selected.has(key))
}

function setRunGroupHost(host: HostOption, checked: boolean) {
  const next = new Set(runGroupHostIds.value)
  for (const key of hostRoleKeys(host)) next.delete(key)
  if (checked) next.add(host.id || host.name)
  runGroupHostIds.value = [...next]
}

function setRoleHost(roleName: string, envName: string, host: HostOption, checked: boolean) {
  const next = { ...(props.roles ?? {}) }
  const role = { ...(next[roleName] ?? {}) }
  const hosts = new Set(roleHosts(roleName, envName))
  for (const key of hostRoleKeys(host)) hosts.delete(key)
  if (checked) hosts.add(host.id || host.name)
  role.environments = {
    ...(role.environments ?? {}),
    [envName]: [...hosts],
  }
  next[roleName] = role
  emit('update:roles', next)
}

function addRunGroup() {
  const name = runGroupName.value.trim()
  if (!name || name === 'builder') return
  const environments: Record<string, string[]> = {}
  for (const envName of envNames.value) environments[envName] = [...runGroupHostIds.value]
  emit('update:roles', {
    ...(props.roles ?? {}),
    [name]: props.roles?.[name] ?? { environments },
  })
  runGroupName.value = ''
  runGroupHostIds.value = []
  addHostPickerOpen.value = false
}

function deleteRunGroup(name: string) {
  const next = { ...(props.roles ?? {}) }
  delete next[name]
  emit('update:roles', next)
}

function closePickersOnOutsideClick(event: MouseEvent) {
  if (rootRef.value?.contains(event.target as Node)) return
  addHostPickerOpen.value = false
  openRolePickerKey.value = ''
}

onMounted(() => document.addEventListener('click', closePickersOnOutsideClick))
onBeforeUnmount(() => document.removeEventListener('click', closePickersOnOutsideClick))
</script>

<template>
  <section ref="rootRef" class="env-matrix-root" :class="{ standalone }" data-test="pipeline-env-matrix">
    <header v-if="standalone" class="emr-summary" data-test="env-matrix-summary">
      <button type="button" class="emr-toggle" data-test="env-matrix-toggle" @click="collapsed = !collapsed">
        {{ collapsed ? '▸' : '▾' }}
      </button>
      <span>{{ summaryText }}</span>
      <div v-if="availableEnvNames.length > 0" class="emr-env-selectors">
        <label v-for="envName in availableEnvNames" :key="envName" class="emr-env-chip">
          <input
            type="checkbox"
            :data-test="`env-select-${envName}`"
            :checked="envNames.includes(envName)"
            @change="setEnvSelected(envName, ($event.target as HTMLInputElement).checked)"
          />
          {{ envName }}
        </label>
      </div>
    </header>

    <template v-if="!standalone || !collapsed">
      <div v-if="hasEnvironments" class="emr-section" data-test="env-matrix">
        <div class="emr-section-head">
          <div>
            <div class="emr-title">{{ t('settings.pipeline.envVars') }}</div>
            <div class="emr-subtitle">
              {{ t('settings.pipeline.globalVars') }} / {{ t('settings.pipeline.envVars') }} / {{ t('settings.pipeline.runGroups') }}
            </div>
          </div>
          <div v-if="availableEnvNames.length > 0 && !standalone" class="emr-env-selectors">
            <label v-for="envName in availableEnvNames" :key="envName" class="emr-env-chip">
              <input
                type="checkbox"
                :data-test="`env-select-${envName}`"
                :checked="envNames.includes(envName)"
                @change="setEnvSelected(envName, ($event.target as HTMLInputElement).checked)"
              />
              {{ envName }}
            </label>
          </div>
        </div>

        <table class="emr-table">
          <thead>
            <tr>
              <th>{{ t('settings.pipeline.varName') }}</th>
              <th>{{ t('settings.pipeline.globalVars') }}</th>
              <th v-for="envName in envNames" :key="envName" :data-test="`env-col-${envName}`">{{ envName }}</th>
              <th class="emr-op-head">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="varName in variableRows" :key="varName" :data-test="`env-var-row-${varName}`">
              <td>
                <span class="emr-name-cell">
                  <button type="button" class="emr-varname" :data-test="`copy-var-${varName}`" @click="copyVar(varName)">
                    <span>{{ varName }}</span>
                    <Icon class="emr-copy-icon" icon="lucide:copy" :data-test="`copy-var-${varName}-icon`" aria-hidden="true" />
                  </button>
                  <span v-if="copiedName === varName" class="emr-copy-feedback" :data-test="`copy-var-${varName}-feedback`">
                    {{ t('common.copied') }}
                  </span>
                  <span class="emr-kind-badge">{{ t('settings.pipeline.varName') }}</span>
                </span>
              </td>
              <td>
                <input
                  class="settings-input emr-cell-input"
                  :data-test="`global-var-value-${varName}`"
                  :placeholder="t('settings.pipeline.globalVars')"
                  :value="variables[varName] ?? ''"
                  @input="setGlobal(varName, $event)"
                />
              </td>
              <td v-for="envName in envNames" :key="envName">
                <input
                  class="settings-input emr-cell-input"
                  :data-test="`env-var-${envName}-${varName}`"
                  :placeholder="variables[varName] ? t('settings.pipeline.globalVars') : ''"
                  :value="environments[envName]?.variables?.[varName] ?? ''"
                  @input="setEnvVar(envName, varName, $event)"
                />
              </td>
              <td class="emr-op-cell">
                <button
                  v-if="!isReservedName(varName)"
                  type="button"
                  class="settings-btn settings-btn-text settings-btn-danger emr-delete-btn"
                  :data-test="`delete-var-${varName}`"
                  @click="deleteVariable(varName)"
                >
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>

            <tr v-for="roleName in roleNames" :key="roleName" class="emr-role-row" :data-test="`env-var-row-${roleName}`">
              <td>
                <span class="emr-name-cell">
                  <button type="button" class="emr-varname run-group" :data-test="`copy-var-${roleName}`" @click="copyVar(roleName)">
                    <span>{{ roleName }}</span>
                    <Icon class="emr-copy-icon" icon="lucide:copy" :data-test="`copy-var-${roleName}-icon`" aria-hidden="true" />
                  </button>
                  <span v-if="copiedName === roleName" class="emr-copy-feedback" :data-test="`copy-var-${roleName}-feedback`">
                    {{ t('common.copied') }}
                  </span>
                  <span class="emr-kind-badge role">{{ t('settings.pipeline.runGroups') }}</span>
                </span>
              </td>
              <td>
                <span class="emr-muted">{{ roleSource(roleName) }}</span>
              </td>
              <td v-for="envName in envNames" :key="envName">
                <div class="emr-host-picker emr-role-picker" :data-test="`role-hosts-${envName}-${roleName}`">
                  <button
                    type="button"
                    class="emr-picker-trigger"
                    :aria-expanded="isRolePickerOpen(envName, roleName)"
                    :data-test="`role-host-trigger-${envName}-${roleName}`"
                    @click="toggleRolePicker(envName, roleName)"
                  >
                    <span v-if="roleHostLabels(roleName, envName).length" class="emr-role-chip-list">
                      <span v-for="hostName in roleHostLabels(roleName, envName)" :key="hostName" class="emr-host-token">
                        {{ hostName }}
                      </span>
                    </span>
                    <span v-else class="emr-muted">{{ t('common.none') }}</span>
                    <Icon class="emr-picker-chevron" icon="lucide:chevron-down" aria-hidden="true" />
                  </button>
                  <div
                    v-if="isRolePickerOpen(envName, roleName)"
                    class="emr-picker-menu emr-role-menu"
                    :data-test="`role-host-menu-${envName}-${roleName}`"
                  >
                    <label v-for="host in hosts ?? []" :key="host.id" class="emr-role-option">
                      <input
                        type="checkbox"
                        :data-test="`role-host-${envName}-${roleName}-${host.id}`"
                        :checked="isRoleHostChecked(roleName, envName, host)"
                        @change="setRoleHost(roleName, envName, host, ($event.target as HTMLInputElement).checked)"
                      />
                      <span>{{ host.name }}</span>
                    </label>
                    <span v-if="!hosts?.length" class="emr-muted">{{ t('common.none') }}</span>
                  </div>
                </div>
              </td>
              <td class="emr-op-cell">
                <button
                  type="button"
                  class="settings-btn settings-btn-text settings-btn-danger emr-delete-btn"
                  :data-test="`delete-role-${roleName}`"
                  @click="deleteRunGroup(roleName)"
                >
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>

          </tbody>
        </table>

        <div class="emr-add-panel">
          <div class="emr-add-toolbar" data-test="variable-add-toolbar">
            <div class="emr-add-group">
              <input
                v-model="newGlobalName"
                class="settings-input emr-add-name-input"
                data-test="global-var-name-input"
                :placeholder="t('settings.pipeline.newVarName')"
                @keydown.enter.prevent="addGlobalVar"
              />
              <input
                v-model="newGlobalValue"
                class="settings-input emr-add-value-input"
                data-test="global-var-value-input"
                :placeholder="t('settings.pipeline.defaultValueOptional')"
                @keydown.enter.prevent="addGlobalVar"
              />
              <button type="button" class="settings-btn settings-btn-secondary" data-test="global-var-add" @click="addGlobalVar">
                {{ t('settings.pipeline.addVariable') }}
              </button>
            </div>

            <div class="emr-add-divider" aria-hidden="true"></div>

            <div class="emr-add-group emr-run-group-add">
              <input
                v-model="runGroupName"
                class="settings-input emr-add-role-input"
                data-test="run-group-name-input"
                :placeholder="t('settings.pipeline.newRunGroupName')"
                @keydown.enter.prevent="addRunGroup"
              />
              <div class="emr-host-picker emr-add-host-picker">
                <button
                  type="button"
                  class="emr-picker-trigger emr-add-host-trigger"
                  :aria-expanded="addHostPickerOpen"
                  data-test="run-group-host-trigger"
                  @click="toggleAddHostPicker"
                >
                  <span :class="{ 'emr-muted': runGroupHostIds.length === 0 }">{{ runGroupHostSummary }}</span>
                  <Icon class="emr-picker-chevron" icon="lucide:chevron-down" aria-hidden="true" />
                </button>
                <div v-if="addHostPickerOpen" class="emr-picker-menu emr-add-host-menu" data-test="run-group-host-menu">
                  <label v-for="host in hosts ?? []" :key="host.id" class="emr-role-option">
                    <input
                      type="checkbox"
                      :data-test="`run-group-host-option-${host.id}`"
                      :checked="isRunGroupHostChecked(host)"
                      @change="setRunGroupHost(host, ($event.target as HTMLInputElement).checked)"
                    />
                    <span>{{ host.name }}</span>
                  </label>
                  <span v-if="!hosts?.length" class="emr-muted">{{ t('common.none') }}</span>
                </div>
              </div>
              <button type="button" class="settings-btn settings-btn-secondary" data-test="run-group-add" @click="addRunGroup">
                {{ t('settings.pipeline.addRunGroup') }}
              </button>
            </div>
          </div>

          <div class="emr-reserved">
            <span class="emr-label">{{ t('settings.pipeline.reservedVarsHint') }}</span>
            <span v-for="name in reservedNames" :key="name" class="emr-copy-chip">
              <button
                type="button"
                class="emr-reserved-chip"
                :data-test="`copy-var-${name}`"
                @click="copyVar(name)"
              >
                <span>{{ varPlaceholder(name) }}</span>
                <Icon class="emr-copy-icon" icon="lucide:copy" :data-test="`copy-var-${name}-icon`" aria-hidden="true" />
              </button>
              <span v-if="copiedName === name" class="emr-copy-feedback" :data-test="`copy-var-${name}-feedback`">
                {{ t('common.copied') }}
              </span>
            </span>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.env-matrix-root {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 0;
  background: transparent;
  border-bottom: 0;
}

.env-matrix-root.standalone {
  padding: 12px 18px;
  background: #0e141c;
  border-bottom: 1px solid #263240;
}

.emr-summary,
.emr-env-selectors,
.emr-section-head,
.emr-table-actions,
.emr-reserved,
.emr-name-cell,
.emr-role-chip-list {
  display: flex;
  align-items: center;
}

.emr-summary {
  flex-wrap: wrap;
  gap: 10px;
  color: var(--text-secondary);
  font-size: 12px;
}

.emr-toggle {
  width: 24px;
  height: 24px;
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  background: #17212c;
  color: var(--text-primary);
  cursor: pointer;
}

.emr-env-selectors,
.emr-table-actions,
.emr-reserved,
.emr-role-chip-list {
  flex-wrap: wrap;
  gap: 6px;
}

.emr-env-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  padding: 3px 8px;
  background: #121b25;
  color: var(--text-primary);
  font-size: 11px;
}

.emr-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}

.emr-section-head {
  justify-content: space-between;
  gap: 16px;
}

.emr-title {
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}

.emr-subtitle,
.emr-muted {
  color: var(--text-tertiary, #667);
  font-size: 11px;
}

.emr-label {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
}

.emr-varname {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 0;
  background: transparent;
  color: #7fdc8f;
  cursor: pointer;
  font-family: var(--font-mono, monospace);
  font-size: 12px;
  padding: 0;
}

.emr-copy-icon {
  width: 14px;
  height: 14px;
  color: var(--text-tertiary, #7d8896);
}

.emr-varname:hover .emr-copy-icon,
.emr-reserved-chip:hover .emr-copy-icon {
  color: var(--text-primary);
}

.emr-varname.run-group {
  color: #6cc8ff;
}

.emr-name-cell {
  gap: 6px;
  min-width: 0;
}

.emr-kind-badge {
  border: 1px solid #263240;
  border-radius: 5px;
  background: #15202b;
  color: var(--text-secondary);
  font-size: 10px;
  padding: 2px 6px;
  white-space: nowrap;
}

.emr-kind-badge.role {
  border-color: rgba(31, 111, 235, 0.35);
  background: rgba(31, 111, 235, 0.14);
  color: #8ec5ff;
}

.emr-copy-feedback {
  color: #54d27d;
  font-size: 11px;
  font-weight: 600;
}

.emr-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 12px;
  border: 1px solid #1d2936;
  border-radius: 6px;
  overflow: visible;
}

.emr-table th,
.emr-table td {
  padding: 8px 10px;
  border-top: 1px solid #1a2330;
  border-right: 1px solid #1a2330;
  text-align: left;
  vertical-align: middle;
}

.emr-table th {
  color: var(--text-secondary);
  background: #101821;
  font-size: 11px;
  font-weight: 600;
}

.emr-table th:last-child,
.emr-table td:last-child {
  border-right: 0;
}

.emr-op-head,
.emr-op-cell {
  width: 96px;
}

.emr-op-cell {
  white-space: nowrap;
}

.emr-table tbody tr:first-child td {
  border-top: 1px solid #1a2330;
}

.emr-role-row td {
  background: rgba(31, 111, 235, 0.035);
}

.emr-cell-input {
  height: 30px;
  min-height: 30px;
  font-family: var(--font-mono, monospace);
}

.emr-inline-input {
  width: 150px;
  height: 30px;
  min-height: 30px;
  font-family: var(--font-mono, monospace);
}

.emr-delete-btn {
  padding: 0 2px;
}

.emr-host-picker {
  position: relative;
  min-width: 0;
}

.emr-picker-trigger {
  min-height: 30px;
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-primary);
  color: var(--text-primary);
  padding: 4px 8px;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.emr-picker-trigger:hover,
.emr-picker-trigger[aria-expanded="true"] {
  border-color: rgba(47, 128, 237, 0.72);
}

.emr-picker-chevron {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  color: var(--text-tertiary);
}

.emr-picker-trigger[aria-expanded="true"] .emr-picker-chevron {
  transform: rotate(180deg);
}

.emr-host-token,
.emr-reserved-chip {
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  background: #1a2330;
  color: #c6d5dd;
}

.emr-host-token {
  padding: 2px 7px;
}

.emr-picker-menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 6px);
  left: 0;
  min-width: 190px;
  max-height: 220px;
  overflow: auto;
  display: grid;
  gap: 4px;
  padding: 8px;
  border: 1px solid #263240;
  border-radius: 6px;
  background: #111a24;
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.32);
}

.emr-role-menu {
  width: max-content;
}

.emr-role-option {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--text-primary);
  font-size: 12px;
  min-height: 26px;
  cursor: pointer;
  white-space: nowrap;
}

.emr-role-option:hover {
  color: #e6edf3;
}

.emr-add-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border: 1px solid #1d2936;
  border-radius: 6px;
  background: #0b121a;
  padding: 10px 12px;
}

.emr-add-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: center;
}

.emr-add-group,
.emr-run-group-add {
  display: flex;
  flex: 1 1 420px;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.emr-add-divider {
  align-self: stretch;
  width: 1px;
  min-height: 34px;
  background: #344152;
}

.emr-add-name-input,
.emr-add-value-input,
.emr-add-role-input,
.emr-add-host-picker {
  height: 34px;
  min-height: 34px;
}

.emr-add-name-input,
.emr-add-value-input {
  flex: 1 1 220px;
}

.emr-add-role-input {
  flex: 1 1 180px;
}

.emr-add-host-picker {
  flex: 1 1 190px;
}

.emr-add-host-trigger {
  min-height: 34px;
  background: #0f151d;
}

.emr-add-host-menu {
  width: 100%;
  min-width: 220px;
}

.emr-reserved {
  gap: 6px;
  align-items: center;
}

.emr-reserved-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  padding: 3px 8px;
}
</style>
