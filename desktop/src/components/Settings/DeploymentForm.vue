<!--
DeploymentForm：单份 deployment 的服务环境配置表单。

职责：
  - 编辑服务实例所在节点（本机 / 远程主机列表）
  - 用“监控 / 接管启停”表达 agent 对运行态的能力边界
  - 配置运行态目标（systemd / launchd / docker / command / nginx）和日志来源
  - 配置本机服务的调试入口（AI 代码调试 / Web 入口）
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
  ReadinessProbe,
  RuntimeDiagnostic,
  RuntimeConfig,
  RuntimeSchema,
  RuntimeType,
  ServiceLanguage,
  WebEntrypointConfig,
} from '@/api/agent'
import { api } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import { defaultLanguageRuntime, defaultManagedRuntime } from '@/lib/languageRuntimeDefaults'
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
  /** 同项目同 env 下可作为启动依赖的兄弟服务 */
  siblingServices?: Array<{ id: string; name: string }>
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
  return props.modelValue.location === 'local' && controlMode.value === 'managed'
    ? defaultManagedRuntime(props.serviceLanguage, props.defaultWorkDir)
    : { type: 'systemd' }
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
const isLocalManaged = computed(() => props.modelValue.location === 'local' && controlMode.value === 'managed')
const isLocalLanguageRuntime = computed(() => {
  const runtimeType = runtime.value.type || 'command'
  return props.modelValue.location === 'local' && runtimeType === 'language'
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
const readinessType = computed(() => props.modelValue.readiness?.type ?? '')
const availableDeps = computed(() =>
  (props.siblingServices ?? []).filter(s => !(props.modelValue.depends_on ?? []).includes(s.id)),
)
const serviceLanguageLabel = computed(() => {
  if (props.serviceLanguage === 'go') return 'Go'
  if (props.serviceLanguage === 'node') return 'Node'
  if (props.serviceLanguage === 'python') return 'Python'
  return ''
})
const runtimeArgsTitle = computed(() =>
  serviceLanguageLabel.value
    ? t('settings.deployment.languageRuntimeArgs', { language: serviceLanguageLabel.value })
    : t('settings.deployment.runtimeArgs'),
)

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
    return props.modelValue.location === 'local'
      ? defaultManagedRuntime(props.serviceLanguage, props.defaultWorkDir)
      : { type: 'systemd' }
  }
  return runtime.value.type === 'external' ? { type: 'systemd' } : runtime.value
}

function setControlMode(mode: ControlMode) {
  const nextRuntime = runtimeForMode(mode)
  const nextLogs = isDefaultLogForRuntime(logs.value, runtime.value) ? defaultLogsForRuntime(nextRuntime) : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs, { control_mode: mode })
}

function setLocation(location: Deployment['location']) {
  const nextRuntime = location === 'local' && controlMode.value === 'managed' && runtime.value.type !== 'language' && runtime.value.type !== 'command' && runtime.value.type !== 'launchd'
    ? defaultManagedRuntime(props.serviceLanguage, props.defaultWorkDir)
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
    const defaults = defaultLanguageRuntime(
      props.serviceLanguage,
      runtime.value.cwd ?? runtime.value.working_dir ?? props.modelValue.work_dir ?? props.defaultWorkDir,
    )
    base.cwd = runtime.value.cwd ?? defaults?.cwd ?? runtime.value.working_dir ?? props.modelValue.work_dir ?? props.defaultWorkDir
    base.env = runtime.value.env ?? runtime.value.env_vars ?? props.modelValue.env ?? defaults?.env ?? {}
    base.config = { ...(defaults?.config ?? {}), ...(runtime.value.config ?? {}) }
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

/** siblingName 用 service ID 反查显示名；悬空依赖保留 ID 方便用户定位配置问题。 */
function siblingName(id: string) {
  return props.siblingServices?.find(s => s.id === id)?.name ?? id
}

/** addDep 只写 service ID，避免服务改名导致依赖关系失效。 */
function addDep(id: string) {
  if (!id || (props.modelValue.depends_on ?? []).includes(id)) return
  patch({ depends_on: [...(props.modelValue.depends_on ?? []), id] })
}

/** removeDep 从当前 deployment 的启动依赖中移除一个 service ID。 */
function removeDep(id: string) {
  patch({ depends_on: (props.modelValue.depends_on ?? []).filter(depID => depID !== id) })
}

function readinessWithDefaults(partial: Partial<ReadinessProbe> = {}): ReadinessProbe {
  return {
    type: props.modelValue.readiness?.type ?? 'http',
    target: props.modelValue.readiness?.target ?? '',
    timeout_seconds: props.modelValue.readiness?.timeout_seconds ?? 30,
    ...partial,
  }
}

/** setReadinessType 空值表示沿用旧语义：进程起来即就绪。 */
function setReadinessType(type: string) {
  if (!type) {
    patch({ readiness: undefined })
    return
  }
  patch({ readiness: readinessWithDefaults({ type: type as ReadinessProbe['type'] }) })
}

function setReadinessTarget(target: string) {
  patch({ readiness: readinessWithDefaults({ target }) })
}

function setReadinessTimeout(timeout: number) {
  patch({ readiness: readinessWithDefaults({ timeout_seconds: timeout > 0 ? timeout : 30 }) })
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
  [() => props.serviceLanguage, () => runtime.value.type],
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
  <div class="dep-form" data-test="deployment-layout-grid">
    <div class="dep-column dep-main-column" data-test="deployment-main-column">
      <section class="dep-panel dep-runtime-target" data-test="runtime-target-panel">
        <div class="dep-heading">{{ t('settings.deployment.runtimeTarget') }}</div>
        <div class="dep-help">{{ t('settings.deployment.runtimeTargetHint') }}</div>

        <div class="dep-target-grid">
          <div class="dep-target-cell">
            <div class="settings-field-label dep-label">{{ t('settings.deployment.node') }}</div>
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
          </div>

          <div class="dep-target-cell">
            <div class="settings-field-label dep-label">{{ t('settings.deployment.serviceControl') }}</div>
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
          </div>
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
            <option v-if="canUseLanguageRuntime || runtime.type === 'language'" value="language">{{ t('settings.deployment.languageRuntime') }}</option>
            <option v-if="controlMode === 'managed' && runtime.type === 'command'" value="command">{{ t('settings.deployment.runtimeCommand') }}</option>
            <option value="systemd">{{ t('settings.deployment.systemdService') }}</option>
            <option v-if="controlMode === 'managed'" value="launchd">{{ t('settings.deployment.launchdService') }}</option>
            <option value="docker">{{ t('settings.deployment.dockerContainer') }}</option>
            <option v-if="controlMode === 'monitor'" value="nginx_static">{{ t('settings.deployment.nginxStatic') }}</option>
          </select>
        </div>
      </section>

      <section class="dep-panel dep-runtime-args" data-test="runtime-args-panel">
        <div class="dep-heading">{{ runtimeArgsTitle }}</div>

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
    </div>

    <aside class="dep-column dep-side-column" data-test="deployment-side-column">
      <slot name="side-top" />

      <!-- 自启和就绪门只对本机接管进程生效；远端/监控模式不拥有启动状态机。 -->
      <section v-if="isLocalManaged" class="dep-panel" data-test="startup-readiness-panel">
        <div class="dep-heading">{{ t('settings.deployment.startReadiness') }}</div>

        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.dependsOn') }}</label>
          <div class="dep-chips" data-test="dep-depends-on">
            <span v-for="id in (modelValue.depends_on ?? [])" :key="id" class="dep-chip">
              {{ siblingName(id) }}
              <button type="button" class="dep-chip-remove" :aria-label="t('common.delete')" @click="removeDep(id)">×</button>
            </span>
            <select
              v-if="availableDeps.length"
              class="settings-select dep-dependency-select"
              data-test="dep-add-dependency"
              @change="addDep(($event.target as HTMLSelectElement).value); ($event.target as HTMLSelectElement).value = ''"
            >
              <option value="">{{ t('settings.deployment.addDependency') }}</option>
              <option v-for="s in availableDeps" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </div>
        </div>

        <div class="settings-field dep-field">
          <label class="settings-field-label dep-label">{{ t('settings.deployment.readiness') }}</label>
          <div class="dep-readiness">
            <select
              class="settings-select"
              data-test="dep-readiness-type"
              :value="readinessType"
              @change="setReadinessType(($event.target as HTMLSelectElement).value)"
            >
              <option value="">{{ t('settings.deployment.readinessProcess') }}</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
            </select>
            <input
              v-if="readinessType"
              class="settings-input"
              data-test="dep-readiness-target"
              :placeholder="readinessType === 'http' ? 'http://127.0.0.1:9100/' : '127.0.0.1:9100'"
              :value="modelValue.readiness?.target ?? ''"
              @input="setReadinessTarget(($event.target as HTMLInputElement).value)"
            />
            <input
              v-if="readinessType"
              class="settings-input dep-readiness-timeout"
              data-test="dep-readiness-timeout"
              type="number"
              min="1"
              :value="modelValue.readiness?.timeout_seconds ?? 30"
              @input="setReadinessTimeout(Number(($event.target as HTMLInputElement).value))"
            />
          </div>
        </div>

        <label class="dep-choice dep-field">
          <input
            type="checkbox"
            data-test="dep-start-on-boot"
            :checked="modelValue.start_on_boot ?? false"
            @change="patch({ start_on_boot: ($event.target as HTMLInputElement).checked })"
          />
          {{ t('settings.deployment.startOnBoot') }}
        </label>
      </section>

      <section v-if="modelValue.location === 'local'" class="dep-panel dep-debug-entry" data-test="debug-entry-section">
        <div class="dep-heading">{{ t('settings.deployment.debugEntry') }}</div>

        <div v-if="isLocalLanguageRuntime" class="dep-subblock" data-test="code-debug-section">
          <div class="dep-subheading">{{ t('settings.deployment.codeDebug.title') }}</div>
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
        </div>

        <div class="dep-subblock dep-web-entry" data-test="web-entry-section">
          <div class="dep-subheading">{{ t('settings.deployment.webEntry') }}</div>
          <label class="dep-choice dep-field">
            <input
              type="checkbox"
              data-test="dep-web-enabled"
              :checked="modelValue.web?.enabled ?? false"
              @change="patchWeb({ enabled: ($event.target as HTMLInputElement).checked })"
            />
            {{ t('settings.deployment.webEntryEnabled') }}
          </label>
          <div v-if="modelValue.web?.enabled" class="dep-web-fields" data-test="web-entry-fields">
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
          </div>
        </div>
      </section>

      <section class="dep-panel" data-test="log-source-panel">
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
    </aside>
  </div>
</template>

<style scoped>
.dep-form {
  display: grid;
  grid-template-columns: minmax(360px, 1.05fr) minmax(340px, 0.95fr);
  gap: 18px;
  padding: 0;
}
.dep-column {
  display: grid;
  align-content: start;
  gap: 14px;
  min-width: 0;
}
.dep-panel {
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: rgba(13, 20, 30, 0.76);
}
.dep-runtime-args {
  display: grid;
  gap: 4px;
}
.dep-heading {
  font-size: 16px;
  font-weight: 700;
  color: var(--text-primary);
}
.dep-target-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}
.dep-target-cell {
  min-width: 0;
}
.dep-debug-entry {
  display: grid;
  gap: 10px;
}
.dep-subblock {
  padding-top: 10px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}
.dep-subblock:first-of-type {
  border-top: 0;
}
.dep-subheading {
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}
.dep-web-fields {
  display: grid;
  gap: 2px;
}
.dep-location,
.dep-hosts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-top: 8px;
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
  margin-top: 10px;
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
.dep-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.dep-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 24px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  padding: 2px 6px;
  font-size: 11px;
  line-height: 1;
}
.dep-chip-remove {
  border: 0;
  background: transparent;
  color: var(--text-tertiary);
  cursor: pointer;
  font: inherit;
  line-height: 1;
  padding: 0;
}
.dep-chip-remove:hover {
  color: var(--status-failed);
}
.dep-dependency-select {
  width: auto;
  min-width: 160px;
}
.dep-readiness {
  display: grid;
  grid-template-columns: minmax(120px, 170px) minmax(0, 1fr) 76px;
  gap: 8px;
  align-items: center;
}
.dep-readiness-timeout {
  text-align: right;
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
@media (max-width: 720px) {
  .dep-form,
  .dep-target-grid {
    grid-template-columns: 1fr;
  }
  .dep-readiness {
    grid-template-columns: 1fr;
  }
}
</style>
