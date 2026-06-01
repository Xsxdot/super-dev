<!--
DeploymentForm：单份 deployment 的编辑表单（最大组件，职责单一）。

职责：
  - location 切换 local/remote（本地 / 远程）
  - local：命令 / 工作目录（WorkDirInput）/ 环境变量（EnvKeyValueEditor）
  - remote：主机多选 / 日志类型 / 日志目标 / 启停命令
边界：
  - 不做校验、不发请求；变更整份 emit 给父层草稿
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { Deployment, LogConfig, LogKind, RuntimeConfig, RuntimeType } from '@/api/agent'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'
import WorkDirInput from './WorkDirInput.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
  /** 工作目录默认值，新建流水线步骤时自动填入 */
  defaultWorkDir?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()

// patch 生成新对象后整份 emit，不做本地 ref，保持单向数据流
function patch(partial: Partial<Deployment>) {
  emit('update:modelValue', { ...props.modelValue, ...partial })
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

function inferLogs(runtime: RuntimeConfig): LogConfig {
  if (props.modelValue.logs) return props.modelValue.logs
  return {
    type: props.modelValue.log_type ?? defaultLogKind(runtime.type),
    target: props.modelValue.log_target,
    extra_args: props.modelValue.extra_args,
  }
}

const runtime = computed(() => inferRuntime())
const logs = computed(() => inferLogs(runtime.value))

function legacyLogType(kind: LogKind) {
  return kind === 'journalctl' || kind === 'docker' ? kind : undefined
}

function patchRuntime(partial: Partial<RuntimeConfig>) {
  const next: RuntimeConfig = { ...runtime.value, ...partial, type: partial.type ?? runtime.value.type }
  patch({
    runtime: next,
    command: next.type === 'command' ? next.command : props.modelValue.command,
    work_dir: next.type === 'command' ? next.working_dir : props.modelValue.work_dir,
    env_file: next.type === 'command' ? next.env_file : props.modelValue.env_file,
    env: next.type === 'command' ? next.env_vars : props.modelValue.env,
  })
}

function patchLogs(partial: Partial<LogConfig>) {
  const next: LogConfig = { ...logs.value, ...partial, type: partial.type ?? logs.value.type }
  patch({
    logs: next,
    log_type: legacyLogType(next.type),
    log_target: next.target,
    extra_args: next.extra_args,
  })
}

function setLocation(location: Deployment['location']) {
  if (location === 'local') {
    patch({
      location,
      runtime: runtime.value.type === 'command' ? runtime.value : { type: 'command', command: '' },
      logs: { type: 'process' },
    })
    return
  }
  patch({
    location,
    runtime: runtime.value,
    logs: logs.value.type === 'process' ? { type: defaultLogKind(runtime.value.type) } : logs.value,
  })
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
    base.release_dir = runtime.value.release_dir
    base.current_dir = runtime.value.current_dir
    base.exec_start = runtime.value.exec_start
  } else if (type === 'docker') {
    base.container = runtime.value.container ?? logs.value.target
  } else if (type === 'nginx_static') {
    base.domain = runtime.value.domain
    base.release_dir = runtime.value.release_dir
    base.current_dir = runtime.value.current_dir
  }
  patch({
    runtime: base,
    logs: { ...logs.value, type: defaultLogKind(type) },
  })
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
    <!-- location 切换 -->
    <div class="dep-section">
      <div class="dep-label">运行方式</div>
      <div class="dep-location">
        <label title="在运行 SuperDev 的本机启动">
          <input
            type="radio"
            data-test="dep-location-local"
            :checked="modelValue.location === 'local'"
            @change="setLocation('local')"
          /> 本地
        </label>
        <label title="通过 SSH 在目标主机上运行">
          <input
            type="radio"
            data-test="dep-location-remote"
            :checked="modelValue.location === 'remote'"
            @change="setLocation('remote')"
          /> 远程
        </label>
      </div>
    </div>

    <label class="dep-read-only">
      <input
        type="checkbox"
        data-test="dep-read-only"
        :checked="modelValue.read_only === true"
        @change="patch({ read_only: ($event.target as HTMLInputElement).checked })"
      />
      只读（仅查看日志）
    </label>

    <!-- local 模式：命令 + 工作目录 + 环境变量 -->
    <template v-if="modelValue.location === 'local'">
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
          data-test="dep-work-dir"
          :model-value="runtime.working_dir"
          @update:model-value="patchRuntime({ type: 'command', working_dir: $event })"
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

    <!-- remote 模式：主机多选 / 日志配置 / 启停命令 -->
    <template v-else>
      <div class="dep-label">目标主机</div>
      <div v-if="hosts.length === 0" class="dep-hint">还没有主机，请先在「主机管理」添加</div>
      <label v-for="h in hosts" :key="h.id" class="dep-host">
        <input
          type="checkbox"
          :checked="(modelValue.host_ids ?? []).includes(h.id)"
          @change="toggleHost(h.id, ($event.target as HTMLInputElement).checked)"
        /> {{ h.name }}
      </label>

      <div class="dep-label">运行基座</div>
      <select
        class="dep-input"
        data-test="dep-runtime-type"
        :value="runtime.type"
        @change="setRuntimeType(($event.target as HTMLSelectElement).value as RuntimeType)"
      >
        <option value="command">执行命令</option>
        <option value="systemd">Systemd</option>
        <option value="docker">Docker</option>
        <option value="nginx_static">Nginx 静态资源</option>
        <option value="external">外部托管</option>
      </select>

      <template v-if="runtime.type === 'command'">
        <div class="dep-field">
          <label class="dep-label">执行命令</label>
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
          <input
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
            @input="patchRuntime({ service_name: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">Release 目录</label>
          <input
            class="dep-input"
            data-test="dep-release-dir"
            placeholder="如：/opt/app/releases"
            :value="runtime.release_dir"
            @input="patchRuntime({ release_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">Current 目录</label>
          <input
            class="dep-input"
            data-test="dep-current-dir"
            placeholder="如：/opt/app/current"
            :value="runtime.current_dir"
            @input="patchRuntime({ current_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">启动入口</label>
          <input
            class="dep-input"
            data-test="dep-exec-start"
            placeholder="如：/opt/app/current/app -config prod.yaml"
            :value="runtime.exec_start"
            @input="patchRuntime({ exec_start: ($event.target as HTMLInputElement).value })"
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
            @input="patchRuntime({ container: ($event.target as HTMLInputElement).value })"
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
            @input="patchRuntime({ domain: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">Release 目录</label>
          <input
            class="dep-input"
            data-test="dep-release-dir"
            placeholder="如：/opt/frontend/releases"
            :value="runtime.release_dir"
            @input="patchRuntime({ release_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
        <div class="dep-field">
          <label class="dep-label">Current 目录</label>
          <input
            class="dep-input"
            data-test="dep-current-dir"
            placeholder="如：/opt/frontend/current"
            :value="runtime.current_dir"
            @input="patchRuntime({ current_dir: ($event.target as HTMLInputElement).value })"
          />
        </div>
      </template>

      <div class="dep-label">日志类型</div>
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

      <div class="dep-field">
        <label class="dep-label">日志目标（服务名/容器名）</label>
        <input
          class="dep-input"
          data-test="dep-log-target"
          placeholder="如：my-service 或 my-container"
          :value="logs.target"
          @input="patchLogs({ target: ($event.target as HTMLInputElement).value })"
        />
      </div>
      <div class="dep-field">
        <label class="dep-label">启动命令（可选）</label>
        <input
          class="dep-input"
          data-test="dep-start-command"
          placeholder="如：systemctl start my-service"
          :value="modelValue.start_command"
          @input="patch({ start_command: ($event.target as HTMLInputElement).value })"
        />
      </div>
      <div class="dep-field">
        <label class="dep-label">停止命令（可选）</label>
        <input
          class="dep-input"
          data-test="dep-stop-command"
          placeholder="如：systemctl stop my-service"
          :value="modelValue.stop_command"
          @input="patch({ stop_command: ($event.target as HTMLInputElement).value })"
        />
      </div>
    </template>
  </div>
</template>

<style scoped>
.dep-form {
  padding: 8px 0;
}
.dep-section {
  margin-bottom: 8px;
}
.dep-field {
  margin-bottom: 6px;
}
.dep-location {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--text-secondary);
}
.dep-read-only {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
.dep-input {
  display: block;
  width: 100%;
  padding: 4px 8px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.dep-label {
  font-size: 11px;
  color: var(--text-tertiary);
  margin: 8px 0 4px;
  display: block;
}
.dep-hint {
  font-size: 11px;
  color: var(--status-failed);
  margin-bottom: 6px;
}
.dep-host {
  display: block;
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 3px;
}
</style>
