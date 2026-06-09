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
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { PipelineEnvironment } from '@/api/agent'

const props = defineProps<{
  variables: Record<string, string>
  environments: Record<string, PipelineEnvironment>
  reservedNames: string[]
  roles?: Record<string, { from_service?: string; hosts?: string[] }>
}>()

const emit = defineEmits<{
  'update:variables': [Record<string, string>]
  'update:environments': [Record<string, PipelineEnvironment>]
}>()

const { t } = useAppI18n()

const envNames = computed(() => Object.keys(props.environments ?? {}))
const isMultiEnv = computed(() => envNames.value.length > 1)
const globalEntries = computed(() => Object.entries(props.variables ?? {}))
const roleNames = computed(() => Object.keys(props.roles ?? {}))
const hasRoles = computed(() => roleNames.value.length > 0)

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

function roleSource(roleName: string) {
  const role = props.roles?.[roleName]
  if (role?.from_service) {
    return t('settings.pipeline.fromService', { svc: role.from_service })
  }
  return t('settings.pipeline.hostCount', { count: role?.hosts?.length ?? 0 })
}
</script>

<template>
  <section class="env-matrix-root" data-test="pipeline-env-matrix">
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

    <div v-if="hasRoles" class="emr-section" data-test="run-groups">
      <div class="emr-label">{{ t('settings.pipeline.runGroups') }}</div>
      <div class="emr-flow">
        <span v-for="roleName in roleNames" :key="roleName" class="emr-var" :data-test="`run-group-${roleName}`">
          <button type="button" class="emr-varname run-group" @click="copyVar(roleName)">
            {{ roleName }}
          </button>
          <span class="emr-rg-source">{{ roleSource(roleName) }}</span>
        </span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.env-matrix-root {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 12px 18px;
  background: #0e141c;
  border-bottom: 1px solid #263240;
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
