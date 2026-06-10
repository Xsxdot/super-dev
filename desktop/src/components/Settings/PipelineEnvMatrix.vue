<!--
PipelineEnvMatrix：流水线变量区（全局变量 + 各环境变量矩阵）。

职责：
  - 编辑全局变量，对应 ProjectPipeline.Variables
  - 展示系统保留变量，点击复制 ${var}
  - 展示并编辑各环境变量矩阵，对应 Environments[env].Variables

边界：
  - 不保存配置，仅通过 update 事件回传
  - 单环境时隐藏环境矩阵
  - 不负责解析或校验模板表达式
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { PipelineEnvironment } from '@/api/agent'

const props = defineProps<{
  variables: Record<string, string>
  environments: Record<string, PipelineEnvironment>
  reservedNames: string[]
  roles?: Record<string, { from_service?: string; hosts?: string[] }>
  availableEnvironments?: string[]
  hosts?: Array<{ id: string; name: string }>
}>()

const emit = defineEmits<{
  'update:variables': [Record<string, string>]
  'update:environments': [Record<string, PipelineEnvironment>]
  'update:roles': [Record<string, { from_service?: string; hosts?: string[] }>]
}>()

const { t } = useAppI18n()
const collapsed = ref(true)
const runGroupName = ref('')

const envNames = computed(() => Object.keys(props.environments ?? {}))
const availableEnvNames = computed(() => {
  const names = props.availableEnvironments?.length ? props.availableEnvironments : envNames.value
  return Array.from(new Set(names.filter(Boolean)))
})
const isMultiEnv = computed(() => envNames.value.length > 1)
const globalEntries = computed(() => Object.entries(props.variables ?? {}))
const roleNames = computed(() => Object.keys(props.roles ?? {}).filter(name => name !== 'builder' && !name.endsWith('_runner')))
const hasRoles = computed(() => roleNames.value.length > 0)
const summaryText = computed(() => {
  const globals = globalEntries.value.map(([name]) => name).join(', ') || '0'
  const envSummary = envNames.value.length ? envNames.value.join('/') : '0'
  return `${t('settings.pipeline.globalVars')} ${globalEntries.value.length}: ${globals} · ${t('settings.pipeline.envVars')} ${envSummary}`
})

// 矩阵行必须取所有环境变量名并集，避免某环境新增变量后其他环境列缺位。
const envVarNames = computed(() => {
  const names = new Set<string>()
  for (const env of Object.values(props.environments ?? {})) {
    for (const name of Object.keys(env.variables ?? {})) {
      names.add(name)
    }
  }
  return [...names]
})

function varPlaceholder(name: string) {
  return '${' + name + '}'
}

async function copyVar(name: string) {
  await navigator.clipboard?.writeText(varPlaceholder(name))
}

function setGlobal(name: string, event: Event) {
  emit('update:variables', {
    ...props.variables,
    [name]: (event.target as HTMLInputElement).value,
  })
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

function setEnvSelected(envName: string, checked: boolean) {
  const next = { ...(props.environments ?? {}) }
  if (checked) {
    next[envName] = next[envName] ?? { variables: { env: envName } }
  } else {
    delete next[envName]
  }
  emit('update:environments', next)
}

function roleSource(roleName: string) {
  const role = props.roles?.[roleName]
  if (role?.from_service) {
    return t('settings.pipeline.fromService', { svc: role.from_service })
  }
  return t('settings.pipeline.hostCount', { count: role?.hosts?.length ?? 0 })
}

function roleHosts(roleName: string) {
  return props.roles?.[roleName]?.hosts ?? []
}

function isRoleHostChecked(roleName: string, hostId: string) {
  return roleHosts(roleName).includes(hostId)
}

function setRoleHost(roleName: string, hostId: string, checked: boolean) {
  const next = { ...(props.roles ?? {}) }
  const hosts = new Set(roleHosts(roleName))
  if (checked) hosts.add(hostId)
  else hosts.delete(hostId)
  next[roleName] = { hosts: [...hosts] }
  emit('update:roles', next)
}

function addRunGroup() {
  const name = runGroupName.value.trim()
  if (!name || name === 'builder') return
  emit('update:roles', {
    ...(props.roles ?? {}),
    [name]: props.roles?.[name] ?? { hosts: [] },
  })
  runGroupName.value = ''
}
</script>

<template>
  <section class="env-matrix-root" data-test="pipeline-env-matrix">
    <header class="emr-summary" data-test="env-matrix-summary">
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

    <template v-if="!collapsed">
    <div class="emr-section">
      <div class="emr-label">{{ t('settings.pipeline.globalVars') }}</div>
      <div class="emr-flow">
        <span v-for="[name, value] in globalEntries" :key="name" class="emr-var">
          <button type="button" class="emr-varname" :data-test="`copy-var-${name}`" @click="copyVar(name)">
            {{ name }}
          </button>
          <input
            class="settings-input emr-input"
            :data-test="`global-var-value-${name}`"
            :value="value"
            @input="setGlobal(name, $event)"
          />
        </span>
      </div>
      <div class="emr-reserved">
        <span class="emr-label">{{ t('settings.pipeline.reservedVars') }}</span>
        <button
          v-for="name in reservedNames"
          :key="name"
          type="button"
          class="emr-reserved-chip"
          :data-test="`copy-var-${name}`"
          @click="copyVar(name)"
        >
          {{ varPlaceholder(name) }}
        </button>
      </div>
    </div>

    <div v-if="isMultiEnv" class="emr-section" data-test="env-matrix">
      <div class="emr-label">{{ t('settings.pipeline.envVars') }}</div>
      <table class="emr-table">
        <thead>
          <tr>
            <th>{{ t('settings.pipeline.varName') }}</th>
            <th v-for="envName in envNames" :key="envName" :data-test="`env-col-${envName}`">{{ envName }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="varName in envVarNames" :key="varName">
            <td>
              <button type="button" class="emr-varname" :data-test="`copy-var-${varName}`" @click="copyVar(varName)">
                {{ varName }}
              </button>
            </td>
            <td v-for="envName in envNames" :key="envName">
              <input
                class="settings-input emr-input"
                :data-test="`env-var-${envName}-${varName}`"
                :value="environments[envName]?.variables?.[varName] ?? ''"
                @input="setEnvVar(envName, varName, $event)"
              />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="emr-section" data-test="run-groups">
      <div class="emr-label">{{ t('settings.pipeline.runGroups') }}</div>
      <div class="emr-run-group-add">
        <input
          v-model="runGroupName"
          class="settings-input emr-input"
          data-test="run-group-name-input"
          placeholder="group_name"
          @keydown.enter.prevent="addRunGroup"
        />
        <button type="button" class="settings-btn settings-btn-secondary" data-test="run-group-add" @click="addRunGroup">
          {{ t('common.add') }}
        </button>
      </div>
      <div v-if="hasRoles" class="emr-flow emr-run-groups">
        <div v-for="roleName in roleNames" :key="roleName" class="emr-run-group" :data-test="`run-group-${roleName}`">
          <button type="button" class="emr-varname run-group" @click="copyVar(roleName)">
            {{ roleName }}
          </button>
          <span class="emr-rg-source">{{ roleSource(roleName) }}</span>
          <label v-for="host in hosts ?? []" :key="host.id" class="emr-host-chip">
            <input
              type="checkbox"
              :data-test="`run-group-${roleName}-host-${host.id}`"
              :checked="isRoleHostChecked(roleName, host.id)"
              @change="setRoleHost(roleName, host.id, ($event.target as HTMLInputElement).checked)"
            />
            {{ host.name }}
          </label>
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
  padding: 12px 18px;
  background: #0e141c;
  border-bottom: 1px solid #263240;
}

.emr-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
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

.emr-env-selectors {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.emr-env-chip,
.emr-host-chip {
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
  gap: 8px;
}

.emr-label {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
}

.emr-flow {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.emr-var {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.emr-varname {
  border: 0;
  background: transparent;
  color: #7fdc8f;
  cursor: pointer;
  font-family: var(--font-mono, monospace);
  font-size: 12px;
}

.emr-varname.run-group {
  color: #6cc8ff;
}

.emr-rg-source {
  color: var(--text-tertiary, #667);
  font-size: 11px;
}

.emr-run-group-add {
  display: flex;
  gap: 8px;
  align-items: center;
}

.emr-run-groups {
  align-items: stretch;
}

.emr-run-group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border: 1px solid #1f2b38;
  border-radius: 5px;
  background: #111a24;
}

.emr-input {
  width: 150px;
  height: 30px;
}

.emr-reserved {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.emr-reserved-chip {
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  background: #1a2330;
  color: #c6d5dd;
  cursor: pointer;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  padding: 2px 8px;
}

.emr-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.emr-table th,
.emr-table td {
  padding: 5px 8px;
  border-top: 1px solid #1a2330;
  text-align: left;
}

.emr-table th {
  color: var(--text-secondary);
  font-size: 11px;
}
</style>
