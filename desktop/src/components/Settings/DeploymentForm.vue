<!--
DeploymentForm：单份 deployment 的服务环境配置表单。

职责：
  - 编辑服务实例所在节点（本机 / 远程主机列表）
  - 用“监控 / 接管启停”表达 agent 对运行态的能力边界
  - 配置运行态目标（systemd / launchd / docker / command / nginx）和日志来源
  - 支持日志文件 tail 与自定义日志命令

边界：
  - 不编辑项目级部署流水线；发布目录、制品路径等变量属于项目流水线
  - 不暴露旧的 read_only / external / 自定义启停命令概念，仅做兼容输出
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type {
  CodeDebugConfig,
  ControlMode,
  Deployment,
  LogConfig,
  LogKind,
  RuntimeDiagnostic,
  RuntimeConfig,
  RuntimeSchema,
  RuntimeType,
  ServiceLanguage,
  WebEntrypointConfig,
} from '@/api/agent'
import { api } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'
import SchemaFieldInput from './SchemaFieldInput.vue'
import WorkDirInput from './WorkDirInput.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
  /** 工作目录默认值，新建命令接管配置时自动填入 */
  defaultWorkDir?: string
  /** service 级语言，用于选择并加载 language runtime provider schema */
  serviceLanguage?: ServiceLanguage
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()
const { t } = useAppI18n()

const languageSchema = ref<RuntimeSchema | null>(null)
const languageSchemaLoading = ref(false)
const languageSchemaError = ref('')
const languageDiagnostics = ref<RuntimeDiagnostic[]>([])
let languageSchemaRequestID = 0

interface HostOption {
  id: string
  name: string
  missing?: boolean
}

function patch(partial: Partial<Deployment>) {
  emit('update:modelValue', { ...props.modelValue, ...partial })
}

function serviceLogTarget(serviceName?: string) {
  if (!serviceName) return undefined
  return serviceName.endsWith('.service') ? serviceName : `${serviceName}.service`
}

function serviceNameFromLogTarget(target?: string) {
  if (!target) return undefined
  return target.endsWith('.service') ? target.slice(0, -'.service'.length) : target
}

function inferRuntime(): RuntimeConfig {
  const source = props.modelValue.runtime
  if (source?.type && source.type !== 'external') return source
  if (props.modelValue.command !== undefined || props.modelValue.work_dir !== undefined || props.modelValue.env !== undefined) {
    return {
      type: 'command',
      command: props.modelValue.command ?? '',
      working_dir: props.modelValue.work_dir,
      env_file: props.modelValue.env_file,
      env_vars: props.modelValue.env,
    }
  }
  if (props.modelValue.log_type === 'docker') return { type: 'docker', container: props.modelValue.log_target }
  if (props.modelValue.log_type === 'journalctl' || props.modelValue.logs?.type === 'journalctl' || props.modelValue.log_target) {
    return { type: 'systemd', service_name: serviceNameFromLogTarget(props.modelValue.logs?.target ?? props.modelValue.log_target) }
  }
  return props.modelValue.location === 'local' ? { type: 'command', command: '' } : { type: 'systemd' }
}

const runtime = computed(() => inferRuntime())
const controlMode = computed<ControlMode>(() => {
  if (props.modelValue.control_mode) return props.modelValue.control_mode
  if (props.modelValue.read_only || props.modelValue.runtime?.type === 'external') return 'monitor'
  return 'managed'
})

function defaultLogsForRuntime(nextRuntime: RuntimeConfig): LogConfig {
  if (nextRuntime.type === 'systemd') return { type: 'journalctl', target: serviceLogTarget(nextRuntime.service_name) }
  if (nextRuntime.type === 'launchd') return { type: 'macos_log', target: nextRuntime.label ?? '' }
  if (nextRuntime.type === 'docker') return { type: 'docker', target: nextRuntime.container }
  if (nextRuntime.type === 'nginx_static') return { type: 'nginx', target: nextRuntime.domain }
  return { type: 'process' }
}

function inferLogs(): LogConfig {
  if (props.modelValue.logs) return props.modelValue.logs
  if (props.modelValue.log_type || props.modelValue.log_target || props.modelValue.extra_args) {
    return {
      type: (props.modelValue.log_type as LogKind | undefined) ?? defaultLogsForRuntime(runtime.value).type,
      target: props.modelValue.log_target,
      extra_args: props.modelValue.extra_args,
    }
  }
  return defaultLogsForRuntime(runtime.value)
}

const logs = computed(() => inferLogs())
const isLocalCommand = computed(() => {
  const runtimeType = runtime.value.type || 'command'
  return props.modelValue.location === 'local' && runtimeType === 'command'
})
const canUseLanguageRuntime = computed(() =>
  props.modelValue.location === 'local' &&
  controlMode.value === 'managed' &&
  Boolean(props.serviceLanguage),
)
const languageSchemaFields = computed(() =>
  [...(languageSchema.value?.fields ?? [])].sort((a, b) => (a.order ?? 0) - (b.order ?? 0) || a.key.localeCompare(b.key)),
)
const hostOptions = computed<HostOption[]>(() => {
  const seen = new Set<string>()
  const options: HostOption[] = props.hosts.map((host) => {
    seen.add(host.id)
    return { ...host }
  })
  for (const hostID of props.modelValue.host_ids ?? []) {
    if (seen.has(hostID)) continue
    seen.add(hostID)
    options.push({
      id: hostID,
      name: t('settings.deployment.missingHost', { id: hostID }),
      missing: true,
    })
  }
  return options
})

function legacyLogType(kind?: LogKind) {
  if (kind === 'journalctl' || kind === 'docker') return kind
  return undefined
}

function isDefaultLogForRuntime(log: LogConfig, base: RuntimeConfig) {
  const expected = defaultLogsForRuntime(base)
  return log.type === expected.type &&
    (log.target ?? '') === (expected.target ?? '') &&
    !log.path &&
    !log.command &&
    !log.extra_args?.length
}

function patchRuntimeAndLogs(nextRuntime: RuntimeConfig, nextLogs: LogConfig, extra: Partial<Deployment> = {}) {
  const nextMode = (extra.control_mode as ControlMode | undefined) ?? controlMode.value
  patch({
    ...extra,
    control_mode: nextMode,
    read_only: nextMode === 'monitor' ? true : undefined,
    runtime: nextRuntime,
    logs: nextLogs,
    command: nextRuntime.type === 'command' ? nextRuntime.command : props.modelValue.command,
    work_dir: nextRuntime.type === 'command' ? nextRuntime.working_dir : props.modelValue.work_dir,
    env_file: nextRuntime.type === 'command' ? nextRuntime.env_file : props.modelValue.env_file,
    env: nextRuntime.type === 'command' ? nextRuntime.env_vars : props.modelValue.env,
    log_type: legacyLogType(nextLogs.type),
    log_target: nextLogs.target,
    extra_args: nextLogs.extra_args,
  })
}

function patchRuntime(partial: Partial<RuntimeConfig>) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, ...partial, type: partial.type ?? runtime.value.type }
  const nextLogs = isDefaultLogForRuntime(logs.value, runtime.value) ? defaultLogsForRuntime(nextRuntime) : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function patchLogs(partial: Partial<LogConfig>) {
  const nextLogs: LogConfig = { ...logs.value, ...partial, type: partial.type ?? logs.value.type }
  patchRuntimeAndLogs(runtime.value, nextLogs)
}

function runtimeForMode(mode: ControlMode): RuntimeConfig {
  if (mode === 'managed' && (runtime.value.type === 'nginx_static' || runtime.value.type === 'external')) {
    return props.modelValue.location === 'local' ? { type: 'command', command: '' } : { type: 'systemd' }
  }
  return runtime.value.type === 'external' ? { type: 'systemd' } : runtime.value
}

function setControlMode(mode: ControlMode) {
  const nextRuntime = runtimeForMode(mode)
  const nextLogs = isDefaultLogForRuntime(logs.value, runtime.value) ? defaultLogsForRuntime(nextRuntime) : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs, { control_mode: mode })
}

function setLocation(location: Deployment['location']) {
  const nextRuntime = location === 'local' && controlMode.value === 'managed' && runtime.value.type !== 'command' && runtime.value.type !== 'launchd'
    ? { type: 'command' as const, command: '' }
    : runtimeForMode(controlMode.value)
  const nextLogs = isDefaultLogForRuntime(logs.value, runtime.value) ? defaultLogsForRuntime(nextRuntime) : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs, { location, web: location === 'local' ? props.modelValue.web : undefined })
}

function setRuntimeType(type: RuntimeType) {
  const base: RuntimeConfig = { type }
  if (type === 'command') {
    base.command = runtime.value.command ?? props.modelValue.command ?? ''
    base.working_dir = runtime.value.working_dir ?? props.modelValue.work_dir ?? props.defaultWorkDir
    base.env_file = runtime.value.env_file ?? props.modelValue.env_file
    base.env_vars = runtime.value.env_vars ?? props.modelValue.env
  } else if (type === 'systemd') {
    base.service_name = runtime.value.service_name ?? serviceNameFromLogTarget(logs.value.target)
  } else if (type === 'launchd') {
    base.label = runtime.value.label ?? logs.value.target ?? ''
    if (runtime.value.plist_path !== undefined) base.plist_path = runtime.value.plist_path
  } else if (type === 'docker') {
    base.container = runtime.value.container ?? logs.value.target
  } else if (type === 'nginx_static') {
    base.domain = runtime.value.domain ?? logs.value.target
  } else if (type === 'language') {
    base.cwd = runtime.value.cwd ?? runtime.value.working_dir ?? props.modelValue.work_dir ?? props.defaultWorkDir
    base.env = runtime.value.env ?? runtime.value.env_vars ?? props.modelValue.env ?? {}
    base.config = runtime.value.config ?? {}
  }
  patchRuntimeAndLogs(base, defaultLogsForRuntime(base))
}

function setSystemdServiceName(serviceName: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'systemd', service_name: serviceName }
  const oldDefault = serviceLogTarget(runtime.value.service_name)
  const shouldSyncLogs = logs.value.type === 'journalctl' && (!logs.value.target || logs.value.target === oldDefault)
  const nextLogs = shouldSyncLogs ? { type: 'journalctl' as const, target: serviceLogTarget(serviceName) } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setLaunchdLabel(label: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'launchd', label }
  const oldDefault = runtime.value.label ?? ''
  const shouldSyncLogs = logs.value.type === 'macos_log' && (!logs.value.target || logs.value.target === oldDefault)
  const nextLogs = shouldSyncLogs ? { type: 'macos_log' as const, target: label } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setLaunchdPlistPath(plistPath: string) {
  patchRuntime({ type: 'launchd', plist_path: plistPath })
}

function setDockerContainer(container: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'docker', container }
  const shouldSyncLogs = logs.value.type === 'docker' && (!logs.value.target || logs.value.target === runtime.value.container)
  const nextLogs = shouldSyncLogs ? { type: 'docker' as const, target: container } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setNginxDomain(domain: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'nginx_static', domain }
  const shouldSyncLogs = logs.value.type === 'nginx' && (!logs.value.target || logs.value.target === runtime.value.domain)
  const nextLogs = shouldSyncLogs ? { type: 'nginx' as const, target: domain } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setLogKind(kind: LogKind) {
  if (kind === 'file_tail') {
    patchRuntimeAndLogs(runtime.value, { type: kind, path: logs.value.path ?? '' })
  } else if (kind === 'command') {
    patchRuntimeAndLogs(runtime.value, { type: kind, command: logs.value.command ?? '' })
  } else if (kind === 'process') {
    patchRuntimeAndLogs(runtime.value, { type: kind })
  } else if (kind === 'journalctl') {
    patchRuntimeAndLogs(runtime.value, { type: kind, target: runtime.value.type === 'systemd' ? serviceLogTarget(runtime.value.service_name) : logs.value.target })
  } else if (kind === 'macos_log') {
    patchRuntimeAndLogs(runtime.value, { type: kind, target: runtime.value.type === 'launchd' ? runtime.value.label : logs.value.target })
  } else if (kind === 'docker') {
    patchRuntimeAndLogs(runtime.value, { type: kind, target: runtime.value.type === 'docker' ? runtime.value.container : logs.value.target })
  } else {
    patchRuntimeAndLogs(runtime.value, { type: kind, target: runtime.value.type === 'nginx_static' ? runtime.value.domain : logs.value.target })
  }
}

function toggleHost(id: string, checked: boolean) {
  const set = new Set(props.modelValue.host_ids ?? [])
  if (checked) set.add(id)
  else set.delete(id)
  patch({ host_ids: [...set] })
}

function setEnv(env: Record<string, string>) {
  patchRuntime({ type: 'command', env_vars: env })
}

function setLanguageEnv(env: Record<string, string>) {
  patchRuntime({ type: 'language', env })
}

function setLanguageConfigValue(key: string, value: unknown) {
  patchRuntime({ type: 'language', config: { ...(runtime.value.config ?? {}), [key]: value } })
}

const escapeExecutable = computed(() => String((runtime.value.config ?? {}).runtime_executable ?? ''))
const escapeArgs = computed(() => {
  const value = (runtime.value.config ?? {}).runtime_args
  return Array.isArray(value) ? value.map((item: unknown) => String(item)) : []
})
const hasEscapeHatch = computed(() => escapeExecutable.value.trim() !== '')

function setEscapeExecutable(value: string) {
  setLanguageConfigValue('runtime_executable', value)
}

function setEscapeArgs(args: string[]) {
  setLanguageConfigValue('runtime_args', args)
}

function addEscapeArg() {
  setEscapeArgs([...escapeArgs.value, ''])
}

function updateEscapeArg(index: number, value: string) {
  const next = [...escapeArgs.value]
  next[index] = value
  setEscapeArgs(next)
}

function removeEscapeArg(index: number) {
  const next = [...escapeArgs.value]
  next.splice(index, 1)
  setEscapeArgs(next)
}

function setLanguageCWD(cwd: string) {
  patchRuntime({ type: 'language', cwd })
}

function patchWeb(partial: Partial<WebEntrypointConfig>) {
  const current = props.modelValue.web ?? {
    enabled: false,
    url: '',
    default_path: '/',
    readiness: { type: 'http', timeout_seconds: 30 },
    ai_debug: { enabled: false },
  }
  patch({
    web: {
      ...current,
      ...partial,
      readiness: { ...(current.readiness ?? { type: 'http', timeout_seconds: 30 }), ...(partial.readiness ?? {}) },
      ai_debug: { ...(current.ai_debug ?? { enabled: false }), ...(partial.ai_debug ?? {}) },
    },
  })
}

function patchCodeDebug(partial: Partial<CodeDebugConfig>) {
  const current = props.modelValue.code_debug ?? {}
  patch({ code_debug: { ...current, ...partial } })
}

watch(
  () => [props.serviceLanguage, runtime.value.type] as const,
  async ([language, runtimeType]) => {
    const requestID = ++languageSchemaRequestID
    languageSchemaError.value = ''
    languageDiagnostics.value = []
    if (runtimeType !== 'language' || !language) {
      languageSchema.value = null
      languageSchemaLoading.value = false
      return
    }

    languageSchemaLoading.value = true
    try {
      const schema = await api.describeLanguageRuntimeSchema(language)
      if (requestID !== languageSchemaRequestID) return
      languageSchema.value = schema
    } catch (error) {
      if (requestID !== languageSchemaRequestID) return
      languageSchema.value = null
      languageSchemaError.value = error instanceof Error ? error.message : String(error)
    } finally {
      if (requestID === languageSchemaRequestID) languageSchemaLoading.value = false
    }
  },
  { immediate: true },
)

</script>

<template>
  <div class="dep-form">
    <section class="dep-block">
      <div class="dep-heading">{{ t('settings.deployment.node') }}</div>
      <div class="dep-location">
        <label class="dep-choice">
          <input
            type="radio"
            data-test="dep-location-local"
            :checked="modelValue.location === 'local'"
            @change="setLocation('local')"
          /> {{ t('settings.deployment.local') }}
        </label>
        <label class="dep-choice">
          <input
            type="radio"
            data-test="dep-location-remote"
            :checked="modelValue.location === 'remote'"
            @change="setLocation('remote')"
          /> {{ t('settings.deployment.remoteHost') }}
        </label>
      </div>
      <div v-if="modelValue.location === 'remote'" class="dep-hosts">
        <div v-if="hostOptions.length === 0" class="dep-hint">{{ t('settings.deployment.noHosts') }}</div>
        <label v-for="h in hostOptions" v-else :key="h.id" class="dep-host" :class="{ 'dep-host-missing': h.missing }">
          <input
            type="checkbox"
            :checked="(modelValue.host_ids ?? []).includes(h.id)"
            @change="toggleHost(h.id, ($event.target as HTMLInputElement).checked)"
          /> {{ h.name }}
        </label>
      </div>
    </section>

    <section class="dep-block">
      <div class="dep-heading">{{ t('settings.deployment.serviceControl') }}</div>
      <div class="dep-location">
        <label class="dep-choice">
          <input
            type="radio"
            data-test="dep-control-monitor"
            :checked="controlMode === 'monitor'"
            @change="setControlMode('monitor')"
          /> {{ t('settings.deployment.monitor') }}
        </label>
        <label class="dep-choice">
          <input
            type="radio"
            data-test="dep-control-managed"
            :checked="controlMode === 'managed'"
            @change="setControlMode('managed')"
          /> {{ t('settings.deployment.managed') }}
        </label>
      </div>
      <div class="dep-help">
        {{ controlMode === 'monitor' ? t('settings.deployment.monitorDesc') : t('settings.deployment.managedDesc') }}
      </div>

      <div class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ controlMode === 'monitor' ? t('settings.deployment.monitorTarget') : t('settings.deployment.managedMode') }}</label>
        <select
          class="settings-select dep-input"
          data-test="dep-target-type"
          :value="runtime.type"
          @change="setRuntimeType(($event.target as HTMLSelectElement).value as RuntimeType)"
        >
          <option v-if="controlMode === 'managed'" value="command">{{ t('settings.deployment.runtimeCommand') }}</option>
          <option v-if="canUseLanguageRuntime" value="language">{{ t('settings.deployment.languageRuntime') }}</option>
          <option value="systemd">{{ t('settings.deployment.systemdService') }}</option>
          <option v-if="controlMode === 'managed'" value="launchd">{{ t('settings.deployment.launchdService') }}</option>
          <option value="docker">{{ t('settings.deployment.dockerContainer') }}</option>
          <option v-if="controlMode === 'monitor'" value="nginx_static">{{ t('settings.deployment.nginxStatic') }}</option>
        </select>
      </div>

      <template v-if="runtime.type === 'command'">
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.startCommand') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-command"
            :placeholder="t('settings.deployment.commandPlaceholder')"
            :value="runtime.command"
            @input="patchRuntime({ type: 'command', command: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.workDir') }}</label>
          <WorkDirInput
            v-if="modelValue.location === 'local'"
            data-test="dep-work-dir"
            :model-value="runtime.working_dir"
            @update:model-value="patchRuntime({ type: 'command', working_dir: $event })"
          />
          <input
            v-else
            class="settings-input dep-input"
            data-test="dep-work-dir"
            :placeholder="t('settings.deployment.workDirPlaceholder')"
            :value="runtime.working_dir"
            @input="patchRuntime({ type: 'command', working_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.envFile') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-env-file"
            :placeholder="t('settings.deployment.envFilePlaceholder')"
            :value="runtime.env_file"
            @input="patchRuntime({ type: 'command', env_file: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="settings-field-label dep-label">{{ t('settings.deployment.envVars') }}</div>
        <EnvKeyValueEditor :model-value="runtime.env_vars ?? {}" @update:model-value="setEnv" />
      </template>

      <template v-else-if="runtime.type === 'language'">
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.workDir') }}</label>
          <WorkDirInput
            v-if="modelValue.location === 'local'"
            data-test="dep-language-cwd"
            :model-value="runtime.cwd"
            @update:model-value="setLanguageCWD"
          />
          <input
            v-else
            class="settings-input dep-input"
            data-test="dep-language-cwd"
            :placeholder="t('settings.deployment.workDirPlaceholder')"
            :value="runtime.cwd"
            @input="setLanguageCWD(($event.target as HTMLInputElement).value)"
          />
        </div>
        <div class="settings-field-label dep-label dep-field">{{ t('settings.deployment.envVars') }}</div>
        <EnvKeyValueEditor :model-value="runtime.env ?? {}" @update:model-value="setLanguageEnv" />

        <div v-if="languageSchemaLoading" class="dep-help" data-test="dep-language-schema-loading">
          {{ t('settings.deployment.languageSchemaLoading') }}
        </div>
        <div v-else-if="languageSchemaError" class="dep-warning" data-test="dep-language-schema-error">
          {{ t('settings.deployment.languageSchemaLoadFailed') }}: {{ languageSchemaError }}
        </div>
        <div v-else-if="!serviceLanguage" class="dep-warning" data-test="dep-language-schema-missing">
          {{ t('settings.deployment.languageSchemaMissing') }}
        </div>
        <div v-else class="language-schema-fields" data-test="dep-language-schema-fields">
          <SchemaFieldInput
            v-for="field in languageSchemaFields"
            :key="field.key"
            :field="field"
            :value="(runtime.config ?? {})[field.key]"
            :diagnostics="languageDiagnostics"
            @update:value="setLanguageConfigValue(field.key, $event)"
          />
        </div>

        <details class="dep-advanced" data-test="dep-escape-hatch">
          <summary>{{ t('settings.deployment.escapeHatchTitle') }}</summary>
          <div class="dep-help">{{ t('settings.deployment.escapeHatchHint') }}</div>
          <div class="settings-field dep-field">
            <label class="settings-field-label dep-label">{{ t('settings.deployment.escapeHatchExecutable') }}</label>
            <input
              class="settings-input dep-input"
              data-test="dep-escape-executable"
              :value="escapeExecutable"
              @input="setEscapeExecutable(($event.target as HTMLInputElement).value)"
            />
          </div>
          <div class="settings-field dep-field">
            <label class="settings-field-label dep-label">{{ t('settings.deployment.escapeHatchArgs') }}</label>
            <div class="schema-array">
              <div v-for="(item, index) in escapeArgs" :key="index" class="schema-array-row">
                <input
                  class="settings-input dep-input"
                  :data-test="`dep-escape-arg-${index}`"
                  :value="item"
                  @input="updateEscapeArg(index, ($event.target as HTMLInputElement).value)"
                />
                <button type="button" class="settings-btn settings-btn-danger" :data-test="`dep-escape-arg-remove-${index}`" @click="removeEscapeArg(index)">
                  {{ t('common.remove') }}
                </button>
              </div>
              <button type="button" class="settings-btn settings-btn-secondary" data-test="dep-escape-arg-add" @click="addEscapeArg">
                {{ t('common.add') }}
              </button>
            </div>
          </div>
          <div v-if="hasEscapeHatch" class="dep-warning" data-test="dep-escape-override-notice">
            {{ t('settings.deployment.escapeHatchOverrideNotice') }}
          </div>
        </details>
      </template>

      <div v-else-if="runtime.type === 'systemd'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.serviceName') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-service-name"
          :placeholder="t('settings.deployment.serviceNamePlaceholder')"
          :value="runtime.service_name"
          @input="setSystemdServiceName(($event.target as HTMLInputElement).value)"
        />
      </div>

      <template v-else-if="runtime.type === 'launchd'">
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.launchdLabel') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-launchd-label"
            :placeholder="t('settings.deployment.launchdLabelPlaceholder')"
            :value="runtime.label"
            @input="setLaunchdLabel(($event.target as HTMLInputElement).value)"
          />
        </div>
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.plistPath') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-launchd-plist"
            :placeholder="t('settings.deployment.plistPathPlaceholder')"
            :value="runtime.plist_path"
            @input="setLaunchdPlistPath(($event.target as HTMLInputElement).value)"
          />
        </div>
      </template>

      <div v-else-if="runtime.type === 'docker'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.containerName') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-container"
          :placeholder="t('settings.deployment.containerPlaceholder')"
          :value="runtime.container"
          @input="setDockerContainer(($event.target as HTMLInputElement).value)"
        />
      </div>

      <div v-else-if="runtime.type === 'nginx_static'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.siteDomain') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-domain"
          :placeholder="t('settings.deployment.domainPlaceholder')"
          :value="runtime.domain"
          @input="setNginxDomain(($event.target as HTMLInputElement).value)"
        />
      </div>
    </section>

    <section v-if="modelValue.location === 'local'" class="dep-block">
      <div class="dep-heading">{{ t('settings.deployment.webEntry') }}</div>
      <label class="dep-choice">
        <input
          type="checkbox"
          data-test="dep-web-enabled"
          :checked="modelValue.web?.enabled ?? false"
          @change="patchWeb({ enabled: ($event.target as HTMLInputElement).checked })"
        />
        {{ t('settings.deployment.webEntryEnabled') }}
      </label>
      <template v-if="modelValue.web?.enabled">
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.webUrl') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-web-url"
            :value="modelValue.web?.url ?? ''"
            placeholder="http://127.0.0.1:3000"
            @input="patchWeb({ url: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.webDefaultPath') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-web-default-path"
            :value="modelValue.web?.default_path ?? '/'"
            @input="patchWeb({ default_path: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.webReadinessTimeout') }}</label>
          <input
            class="settings-input dep-input"
            data-test="dep-web-readiness-timeout"
            type="number"
            min="1"
            max="120"
            :value="modelValue.web?.readiness?.timeout_seconds ?? 30"
            @change="patchWeb({ readiness: { type: 'http', timeout_seconds: Number(($event.target as HTMLInputElement).value) } })"
          />
        </div>
        <label class="dep-choice">
          <input
            type="checkbox"
            data-test="dep-web-ai-debug"
            :checked="modelValue.web?.ai_debug?.enabled ?? false"
            @change="patchWeb({ ai_debug: { enabled: ($event.target as HTMLInputElement).checked } })"
          />
          {{ t('settings.deployment.webAIDebug') }}
        </label>
      </template>
    </section>

    <section v-if="isLocalCommand" class="dep-block" data-test="code-debug-section">
      <div class="dep-heading">{{ t('settings.deployment.codeDebug.title') }}</div>
      <div class="dep-help">{{ t('settings.deployment.codeDebug.devDefaultHint') }}</div>

      <div class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.codeDebug.policy') }}</label>
        <select
          class="settings-select dep-input"
          data-test="code-debug-policy"
          :value="modelValue.code_debug?.policy ?? 'auto'"
          @change="patchCodeDebug({ policy: ($event.target as HTMLSelectElement).value as CodeDebugConfig['policy'] })"
        >
          <option value="auto">{{ t('settings.deployment.codeDebug.policyAuto') }}</option>
          <option value="enabled" data-test="code-debug-policy-enabled">{{ t('settings.deployment.codeDebug.policyEnabled') }}</option>
          <option value="disabled" data-test="code-debug-policy-disabled">{{ t('settings.deployment.codeDebug.policyDisabled') }}</option>
        </select>
      </div>

      <div
        v-if="(modelValue.code_debug?.policy ?? 'auto') === 'enabled'"
        class="dep-warning"
        data-test="code-debug-nondev-warning"
      >
        {{ t('settings.deployment.codeDebug.nonDevWarning') }}
      </div>

      <details class="dep-advanced">
        <summary>{{ t('settings.deployment.codeDebug.overrides') }}</summary>
        <label class="dep-choice dep-field">
          <input
            type="checkbox"
            data-test="code-debug-stop-on-entry"
            :checked="modelValue.code_debug?.stop_on_entry ?? false"
            @change="patchCodeDebug({ stop_on_entry: ($event.target as HTMLInputElement).checked })"
          />
          {{ t('settings.deployment.codeDebug.stopOnEntry') }}
        </label>
      </details>
    </section>

    <section class="dep-block">
      <div class="dep-heading">{{ t('settings.deployment.logSource') }}</div>
      <div class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.sourceType') }}</label>
        <select
          class="settings-select dep-input"
          data-test="dep-log-type"
          :value="logs.type"
          @change="setLogKind(($event.target as HTMLSelectElement).value as LogKind)"
        >
          <option value="process">{{ t('settings.deployment.processOutput') }}</option>
          <option value="journalctl">journalctl</option>
          <option value="macos_log">macOS log stream</option>
          <option value="docker">docker logs</option>
          <option value="nginx">{{ t('settings.deployment.nginxLogs') }}</option>
          <option value="file_tail">{{ t('settings.deployment.fileTail') }}</option>
          <option value="command">{{ t('settings.deployment.customLogCommand') }}</option>
        </select>
      </div>

      <div v-if="logs.type === 'journalctl' || logs.type === 'macos_log' || logs.type === 'docker' || logs.type === 'nginx'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.logTarget') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-log-target"
          :placeholder="t('settings.deployment.logTargetPlaceholder')"
          :value="logs.target"
          @input="patchLogs({ target: ($event.target as HTMLInputElement).value })"
        />
      </div>

      <div v-else-if="logs.type === 'file_tail'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.logFilePath') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-log-path"
          :placeholder="t('settings.deployment.logFilePlaceholder')"
          :value="logs.path"
          @input="patchLogs({ path: ($event.target as HTMLInputElement).value })"
        />
      </div>

      <div v-else-if="logs.type === 'command'" class="settings-field dep-field">
        <label class="settings-field-label dep-label">{{ t('settings.deployment.logCommand') }}</label>
        <input
          class="settings-input dep-input"
          data-test="dep-log-command"
          :placeholder="t('settings.deployment.logCommandPlaceholder')"
          :value="logs.command"
          @input="patchLogs({ command: ($event.target as HTMLInputElement).value })"
        />
      </div>
    </section>
  </div>
</template>

<style scoped>
.dep-form {
  padding: 6px 0 2px;
}
.dep-block {
  padding: 10px 0;
  border-top: 1px solid var(--border-secondary);
}
.dep-block:first-child {
  border-top: 0;
  padding-top: 4px;
}
.dep-heading {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
}
.dep-location,
.dep-hosts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-top: 6px;
}
.dep-choice,
.dep-host {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary);
}
.dep-host-missing {
  color: var(--warning);
}
.dep-field {
  margin-top: 8px;
}
.dep-help,
.dep-hint {
  margin-top: 6px;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.5;
}
.dep-hint {
  color: var(--status-failed);
}
.dep-warning {
  margin-top: 8px;
  color: var(--warning);
  font-size: 11px;
  line-height: 1.5;
}
.dep-advanced {
  margin-top: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}
.dep-advanced summary {
  cursor: pointer;
  color: var(--text-secondary);
}
</style>
