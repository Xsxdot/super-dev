<!--
DeploymentForm：单份 deployment 的编辑表单（最大组件，职责单一）。

职责：
  - 编辑服务在某环境下的运行位置、运行节点和运行接管方式
  - command：命令 / 工作目录（WorkDirInput）/ 环境变量（EnvKeyValueEditor）
  - systemd/docker/nginx/external：只暴露运行识别所需的最小字段
  - 日志来源默认按运行方式推导，必要时再展开覆盖
边界：
  - 不做校验、不发请求；变更整份 emit 给父层草稿
  - 不编辑项目级流水线变量，发布目录等部署过程配置留给项目流水线
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Deployment, LogConfig, LogKind, RuntimeConfig, RuntimeType } from '@/api/agent'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'
import WorkDirInput from './WorkDirInput.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
  /** 工作目录默认值，新建命令运行配置时自动填入 */
  defaultWorkDir?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()
const showLogOverride = ref(false)
const showControlAdvanced = ref(false)

// patch 生成新对象后整份 emit，不做本地 ref，保持单向数据流
function patch(partial: Partial<Deployment>) {
  emit('update:modelValue', { ...props.modelValue, ...partial })
}

function systemdLogTarget(serviceName?: string) {
  if (!serviceName) return undefined
  return serviceName.endsWith('.service') ? serviceName : `${serviceName}.service`
}

function logKindLabel(kind: LogKind) {
  if (kind === 'process') return '进程输出'
  if (kind === 'journalctl') return 'journalctl'
  if (kind === 'docker') return 'docker logs'
  return 'Nginx 日志'
}

function inferRuntime(): RuntimeConfig {
  if (props.modelValue.runtime) return props.modelValue.runtime
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
  if (props.modelValue.log_type === 'journalctl' || props.modelValue.log_target) {
    return { type: 'systemd', service_name: props.modelValue.log_target?.replace(/\.service$/, '') }
  }
  return props.modelValue.location === 'local' ? { type: 'command', command: '' } : { type: 'systemd' }
}

function defaultLogKind(type: RuntimeType): LogKind {
  if (type === 'docker') return 'docker'
  if (type === 'nginx_static') return 'nginx'
  if (type === 'systemd') return 'journalctl'
  return 'process'
}

function defaultLogsForRuntime(nextRuntime: RuntimeConfig, previous?: LogConfig): LogConfig {
  if (nextRuntime.type === 'systemd') {
    return { type: 'journalctl', target: systemdLogTarget(nextRuntime.service_name) }
  }
  if (nextRuntime.type === 'docker') {
    return { type: 'docker', target: nextRuntime.container }
  }
  if (nextRuntime.type === 'nginx_static') {
    return { type: 'nginx', target: nextRuntime.domain }
  }
  if (nextRuntime.type === 'external') {
    return previous?.type ? previous : { type: 'journalctl' }
  }
  return { type: 'process' }
}

function inferLogs(runtime: RuntimeConfig): LogConfig {
  if (props.modelValue.logs) return props.modelValue.logs
  if (!props.modelValue.log_type && !props.modelValue.log_target && !props.modelValue.extra_args) {
    return defaultLogsForRuntime(runtime)
  }
  return {
    type: props.modelValue.log_type ?? defaultLogKind(runtime.type),
    target: props.modelValue.log_target,
    extra_args: props.modelValue.extra_args,
  }
}

const runtime = computed(() => inferRuntime())
const logs = computed(() => inferLogs(runtime.value))
const logSummary = computed(() => {
  const target = logs.value.target?.trim()
  return target ? `${logKindLabel(logs.value.type)}：${target}` : logKindLabel(logs.value.type)
})

function legacyLogType(kind: LogKind) {
  return kind === 'journalctl' || kind === 'docker' ? kind : undefined
}

function patchRuntimeAndLogs(nextRuntime: RuntimeConfig, nextLogs: LogConfig = logs.value, extra: Partial<Deployment> = {}) {
  patch({
    ...extra,
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
  const next: RuntimeConfig = { ...runtime.value, ...partial, type: partial.type ?? runtime.value.type }
  patchRuntimeAndLogs(next)
}

function patchLogs(partial: Partial<LogConfig>) {
  const next: LogConfig = { ...logs.value, ...partial, type: partial.type ?? logs.value.type }
  patchRuntimeAndLogs(runtime.value, next)
}

function setLocation(location: Deployment['location']) {
  const nextRuntime: RuntimeConfig = location === 'local'
    ? (runtime.value.type === 'command' ? runtime.value : { type: 'command', command: '' })
    : runtime.value
  const nextLogs = location === 'local' ? { type: 'process' as const } : defaultLogsForRuntime(nextRuntime, logs.value)
  patchRuntimeAndLogs(nextRuntime, nextLogs, { location })
}

function setRuntimeType(type: RuntimeType) {
  const base: RuntimeConfig = { type }
  if (type === 'command') {
    base.command = runtime.value.command ?? props.modelValue.command ?? ''
    base.working_dir = runtime.value.working_dir ?? props.modelValue.work_dir
    base.env_file = runtime.value.env_file ?? props.modelValue.env_file
    base.env_vars = runtime.value.env_vars ?? props.modelValue.env
  } else if (type === 'systemd') {
    base.service_name = runtime.value.service_name
  } else if (type === 'docker') {
    base.container = runtime.value.container ?? logs.value.target
  } else if (type === 'nginx_static') {
    base.domain = runtime.value.domain
  }
  patchRuntimeAndLogs(base, defaultLogsForRuntime(base, logs.value))
}

function setSystemdServiceName(serviceName: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'systemd', service_name: serviceName }
  const oldDefault = systemdLogTarget(runtime.value.service_name)
  const shouldSyncLogs = logs.value.type === 'journalctl' && (!logs.value.target || logs.value.target === oldDefault)
  const nextLogs = shouldSyncLogs ? { ...logs.value, type: 'journalctl' as const, target: systemdLogTarget(serviceName) } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setDockerContainer(container: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'docker', container }
  const shouldSyncLogs = logs.value.type === 'docker' && (!logs.value.target || logs.value.target === runtime.value.container)
  const nextLogs = shouldSyncLogs ? { ...logs.value, type: 'docker' as const, target: container } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function setNginxDomain(domain: string) {
  const nextRuntime: RuntimeConfig = { ...runtime.value, type: 'nginx_static', domain }
  const shouldSyncLogs = logs.value.type === 'nginx' && (!logs.value.target || logs.value.target === runtime.value.domain)
  const nextLogs = shouldSyncLogs ? { ...logs.value, type: 'nginx' as const, target: domain } : logs.value
  patchRuntimeAndLogs(nextRuntime, nextLogs)
}

function toggleLogOverride() {
  showLogOverride.value = !showLogOverride.value
}

function toggleControlAdvanced() {
  showControlAdvanced.value = !showControlAdvanced.value
}

function shouldShowLogOverride() {
  return showLogOverride.value || runtime.value.type === 'external'
}

function shouldShowControlAdvanced() {
  return showControlAdvanced.value && props.modelValue.location === 'remote'
}

function canShowControlAdvanced() {
  return props.modelValue.location === 'remote'
}

function runtimeHint(type: RuntimeType) {
  if (type === 'command') return 'Agent 直接执行命令，并接管进程生命周期和输出日志。'
  if (type === 'systemd') return '服务已经由 systemd 托管，这里只需要告诉 Agent 服务名。'
  if (type === 'docker') return '服务已经跑在容器里，这里只需要容器名。'
  if (type === 'nginx_static') return '静态资源由 Nginx 托管，发布目录等变量放在项目级流水线里。'
  return '服务由外部系统托管，SuperDebug 只负责观测日志。'
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
      <div class="dep-heading">运行位置</div>
      <div class="dep-location">
        <label class="dep-choice" title="在运行 SuperDebug 的本机启动">
          <input
            type="radio"
            data-test="dep-location-local"
            :checked="modelValue.location === 'local'"
            @change="setLocation('local')"
          /> 本地
        </label>
        <label class="dep-choice" title="由目标主机上的 agent 接管">
          <input
            type="radio"
            data-test="dep-location-remote"
            :checked="modelValue.location === 'remote'"
            @change="setLocation('remote')"
          /> 远程主机
        </label>
      </div>
      <label class="dep-read-only">
        <input
          type="checkbox"
          data-test="dep-read-only"
          :checked="modelValue.read_only === true"
          @change="patch({ read_only: ($event.target as HTMLInputElement).checked })"
        />
        只看日志
      </label>
    </section>

    <section v-if="modelValue.location === 'remote'" class="dep-block">
      <div class="dep-heading">运行节点</div>
      <div v-if="hosts.length === 0" class="dep-hint">还没有主机，请先在「主机管理」添加</div>
      <div v-else class="dep-host-list">
        <label v-for="h in hosts" :key="h.id" class="dep-host">
          <input
            type="checkbox"
            :checked="(modelValue.host_ids ?? []).includes(h.id)"
            @change="toggleHost(h.id, ($event.target as HTMLInputElement).checked)"
          /> {{ h.name }}
        </label>
      </div>
    </section>

    <section class="dep-block">
      <div class="dep-heading">运行接管</div>
      <select
        v-if="modelValue.location === 'remote'"
        class="dep-input"
        data-test="dep-runtime-type"
        :value="runtime.type"
        @change="setRuntimeType(($event.target as HTMLSelectElement).value as RuntimeType)"
      >
        <option value="command">Agent 执行命令</option>
        <option value="systemd">已有 Systemd 服务</option>
        <option value="docker">已有 Docker 容器</option>
        <option value="nginx_static">Nginx 静态站点</option>
        <option value="external">外部托管</option>
      </select>
      <div v-else class="dep-mode-static">Agent 执行命令</div>
      <div class="dep-help">{{ runtimeHint(runtime.type) }}</div>

      <template v-if="runtime.type === 'command'">
        <div class="dep-field">
          <label class="dep-label">启动命令</label>
          <input
            class="dep-input"
            data-test="dep-command"
            placeholder="如：go run ./cmd/server"
            :value="runtime.command"
            @input="patchRuntime({ type: 'command', command: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">工作目录</label>
          <WorkDirInput
            v-if="modelValue.location === 'local'"
            data-test="dep-work-dir"
            :model-value="runtime.working_dir"
            @update:model-value="patchRuntime({ type: 'command', working_dir: $event })"
          />
          <input
            v-else
            class="dep-input"
            data-test="dep-work-dir"
            placeholder="如：/opt/app/current"
            :value="runtime.working_dir"
            @input="patchRuntime({ type: 'command', working_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">环境变量文件</label>
          <input
            class="dep-input"
            data-test="dep-env-file"
            placeholder="如：.env.dev"
            :value="runtime.env_file"
            @input="patchRuntime({ type: 'command', env_file: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-label">环境变量</div>
        <EnvKeyValueEditor :model-value="runtime.env_vars ?? {}" @update:model-value="setEnv" />
      </template>

      <template v-else-if="runtime.type === 'systemd'">
        <div class="dep-field">
          <label class="dep-label">Systemd 服务名</label>
          <input
            class="dep-input"
            data-test="dep-service-name"
            placeholder="如：my-service"
            :value="runtime.service_name"
            @input="setSystemdServiceName(($event.target as HTMLInputElement).value)"
          />
        </div>
      </template>

      <template v-else-if="runtime.type === 'docker'">
        <div class="dep-field">
          <label class="dep-label">容器名</label>
          <input
            class="dep-input"
            data-test="dep-container"
            placeholder="如：my-container"
            :value="runtime.container"
            @input="setDockerContainer(($event.target as HTMLInputElement).value)"
          />
        </div>
      </template>

      <template v-else-if="runtime.type === 'nginx_static'">
        <div class="dep-field">
          <label class="dep-label">域名</label>
          <input
            class="dep-input"
            data-test="dep-domain"
            placeholder="如：www.example.com"
            :value="runtime.domain"
            @input="setNginxDomain(($event.target as HTMLInputElement).value)"
          />
        </div>
      </template>

      <div v-else-if="runtime.type === 'external'" class="dep-empty-note">无需配置启动方式。</div>

    </section>

    <section class="dep-block">
      <div class="dep-row-head">
        <div>
          <div class="dep-heading">日志采集</div>
          <div class="dep-summary">{{ logSummary }}</div>
        </div>
        <button type="button" class="dep-link-btn" data-test="dep-log-toggle" @click="toggleLogOverride">
          {{ shouldShowLogOverride() ? '收起' : '自定义' }}
        </button>
      </div>

      <div v-if="shouldShowLogOverride()" class="dep-advanced">
        <div class="dep-field">
          <label class="dep-label">日志类型</label>
          <select
            class="dep-input"
            data-test="dep-log-type"
            :value="logs.type"
            @change="patchLogs({ type: ($event.target as HTMLSelectElement).value as LogKind })"
          >
            <option value="process">进程输出</option>
            <option value="journalctl">journalctl</option>
            <option value="docker">docker</option>
            <option value="nginx">nginx</option>
          </select>
        </div>
        <div class="dep-field">
          <label class="dep-label">日志目标</label>
          <input
            class="dep-input"
            data-test="dep-log-target"
            placeholder="如：my-service.service 或 my-container"
            :value="logs.target"
            @input="patchLogs({ target: ($event.target as HTMLInputElement).value })"
          />
        </div>
      </div>
    </section>

    <section v-if="canShowControlAdvanced()" class="dep-block">
      <button type="button" class="dep-link-btn" data-test="dep-control-toggle" @click="toggleControlAdvanced">
        {{ showControlAdvanced ? '收起自定义启停命令' : '自定义启停命令' }}
      </button>
      <div v-if="shouldShowControlAdvanced()" class="dep-advanced">
        <div class="dep-field">
          <label class="dep-label">启动命令</label>
          <input
            class="dep-input"
            data-test="dep-start-command"
            placeholder="如：systemctl start my-service"
            :value="modelValue.start_command"
            @input="patch({ start_command: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">停止命令</label>
          <input
            class="dep-input"
            data-test="dep-stop-command"
            placeholder="如：systemctl stop my-service"
            :value="modelValue.stop_command"
            @input="patch({ stop_command: ($event.target as HTMLInputElement).value })"
          />
        </div>
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
.dep-field {
  margin-top: 8px;
}
.dep-location {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 6px;
}
.dep-choice,
.dep-host {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.dep-read-only {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 8px;
}
.dep-input {
  display: block;
  width: 100%;
  min-height: 30px;
  padding: 5px 8px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.dep-heading {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
}
.dep-label {
  font-size: 11px;
  color: var(--text-tertiary);
  margin: 0 0 4px;
  display: block;
}
.dep-hint {
  font-size: 11px;
  color: var(--status-failed);
  margin-top: 6px;
}
.dep-help,
.dep-empty-note,
.dep-summary {
  font-size: 11px;
  color: var(--text-tertiary);
  line-height: 1.5;
}
.dep-help {
  margin-top: 6px;
}
.dep-mode-static {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-secondary);
}
.dep-host-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin-top: 6px;
}
.dep-host {
  font-size: 12px;
  color: var(--text-secondary);
}
.dep-row-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.dep-link-btn {
  padding: 0;
  background: transparent;
  border: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 11px;
  line-height: 1.5;
  white-space: nowrap;
}
.dep-advanced {
  margin-top: 8px;
  padding-left: 10px;
  border-left: 2px solid var(--border-secondary);
}
</style>
