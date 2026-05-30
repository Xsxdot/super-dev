<!--
DeploymentForm：单份 deployment 的编辑表单（最大组件，职责单一）。

职责：
  - location 切换 local/remote（本地 / 远程）
  - local：命令 / 工作目录 / 环境变量（EnvKeyValueEditor）
  - remote：主机多选 / 日志类型 / 日志目标 / 启停命令
  - pipeline：折叠的 PipelineEditor
边界：
  - 不做校验、不发请求；变更整份 emit 给父层草稿
-->
<script setup lang="ts">
import type { Deployment, LogSourceType, Pipeline } from '@/api/agent'
import PipelineEditor from './PipelineEditor.vue'
import EnvKeyValueEditor from './EnvKeyValueEditor.vue'

const props = defineProps<{
  modelValue: Deployment
  hosts: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{ 'update:modelValue': [Deployment] }>()

// patch 生成新对象后整份 emit，不做本地 ref，保持单向数据流
function patch(partial: Partial<Deployment>) {
  emit('update:modelValue', { ...props.modelValue, ...partial })
}

function toggleHost(id: string, checked: boolean) {
  const set = new Set(props.modelValue.host_ids ?? [])
  if (checked) set.add(id)
  else set.delete(id)
  patch({ host_ids: [...set] })
}

function setPipeline(pipeline: Pipeline | undefined) {
  patch({ pipeline })
}

function setEnv(env: Record<string, string>) {
  patch({ env })
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
            @change="patch({ location: 'local' })"
          /> 本地
        </label>
        <label title="通过 SSH 在目标主机上运行">
          <input
            type="radio"
            data-test="dep-location-remote"
            :checked="modelValue.location === 'remote'"
            @change="patch({ location: 'remote' })"
          /> 远程
        </label>
      </div>
    </div>

    <!-- local 模式：命令 + 工作目录 + 环境变量 -->
    <template v-if="modelValue.location === 'local'">
      <div class="dep-field">
        <label class="dep-label">启动命令</label>
        <input
          class="dep-input"
          data-test="dep-command"
          placeholder="如：go run ./cmd/server"
          :value="modelValue.command"
          @input="patch({ command: ($event.target as HTMLInputElement).value })"
        />
      </div>
      <div class="dep-field">
        <label class="dep-label">工作目录</label>
        <input
          class="dep-input"
          data-test="dep-work-dir"
          placeholder="如：/home/user/project"
          :value="modelValue.work_dir"
          @input="patch({ work_dir: ($event.target as HTMLInputElement).value })"
        />
      </div>
      <div class="dep-label">环境变量</div>
      <EnvKeyValueEditor :model-value="modelValue.env ?? {}" @update:model-value="setEnv" />
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

      <div class="dep-label">日志类型</div>
      <select
        class="dep-input"
        data-test="dep-log-type"
        :value="modelValue.log_type ?? 'journalctl'"
        @change="patch({ log_type: ($event.target as HTMLSelectElement).value as LogSourceType })"
      >
        <option value="journalctl">journalctl</option>
        <option value="docker">docker</option>
      </select>

      <div class="dep-field">
        <label class="dep-label">日志目标（服务名/容器名）</label>
        <input
          class="dep-input"
          data-test="dep-log-target"
          placeholder="如：my-service 或 my-container"
          :value="modelValue.log_target"
          @input="patch({ log_target: ($event.target as HTMLInputElement).value })"
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

    <!-- 部署流水线（可选） -->
    <div class="dep-label">部署流水线（可选）</div>
    <PipelineEditor :model-value="modelValue.pipeline" @update:model-value="setPipeline" />
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
