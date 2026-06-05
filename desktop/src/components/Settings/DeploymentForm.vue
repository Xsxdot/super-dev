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
import { computed } from 'vue'
import type { ControlMode, Deployment, LogConfig, LogKind, RuntimeConfig, RuntimeType } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'
import WorkDirInput from './WorkDirInput.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
  /** 工作目录默认值，新建命令接管配置时自动填入 */
  defaultWorkDir?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()
const { t } = useAppI18n()

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
  patchRuntimeAndLogs(nextRuntime, nextLogs, { location })
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
        <div v-if="hosts.length === 0" class="dep-hint">{{ t('settings.deployment.noHosts') }}</div>
        <label v-for="h in hosts" v-else :key="h.id" class="dep-host">
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
</style>
