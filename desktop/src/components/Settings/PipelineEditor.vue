<!--
PipelineEditor：deployment 流水线（有序 Step 列表）编辑器。

职责：
  - 无 pipeline 时提供「配置流水线」入口（emit { steps: [] }）
  - 有 pipeline 时渲染可增删、可上下移的步骤卡片
  - 按 action 切换显示 run（command/work_dir）或 sync（from/to）字段
边界：
  - 不做校验（校验在 configDraft.validateDraft）
  - Step ID 为空时本地补 step-{n}
-->
<script setup lang="ts">
import type { Pipeline, PipelineStep } from '@/api/agent'

const props = defineProps<{ modelValue?: Pipeline }>()
const emit = defineEmits<{ 'update:modelValue': [Pipeline | undefined] }>()

function enable() {
  emit('update:modelValue', { steps: [] })
}

function update(steps: PipelineStep[]) {
  emit('update:modelValue', { steps })
}

function addStep() {
  const steps = [...(props.modelValue?.steps ?? [])]
  steps.push({ id: crypto.randomUUID(), name: '', scope: 'local', action: 'run', command: '' })
  update(steps)
}

function delStep(i: number) {
  const steps = [...(props.modelValue!.steps)]
  steps.splice(i, 1)
  update(steps)
}

function move(i: number, delta: number) {
  const steps = [...(props.modelValue!.steps)]
  const j = i + delta
  if (j < 0 || j >= steps.length) return
  ;[steps[i], steps[j]] = [steps[j], steps[i]]
  update(steps)
}

function patch(i: number, field: keyof PipelineStep, value: string) {
  const steps = props.modelValue!.steps.map((s, k) => (k === i ? { ...s, [field]: value } : s))
  update(steps)
}

function disable() {
  emit('update:modelValue', undefined)
}
</script>

<template>
  <div class="pipeline-editor">
    <button v-if="!modelValue" type="button" class="pl-enable" data-test="pipeline-enable" @click="enable">
      + 配置流水线
    </button>
    <template v-else>
      <div class="pl-head">
        <span>流水线步骤</span>
        <button type="button" class="pl-disable" @click="disable">移除流水线</button>
      </div>
      <div v-for="(step, i) in modelValue.steps" :key="i" class="step-card" data-test="step-card">
        <div class="step-toolbar">
          <button type="button" @click="move(i, -1)">▲</button>
          <button type="button" @click="move(i, 1)">▼</button>
          <button type="button" data-test="step-del" @click="delStep(i)">✕</button>
        </div>
        <input
          class="step-input" placeholder="步骤名"
          :value="step.name" @input="patch(i, 'name', ($event.target as HTMLInputElement).value)"
        />
        <div class="step-radios">
          <label><input type="radio" :checked="step.scope === 'local'" @change="patch(i, 'scope', 'local')" /> local</label>
          <label><input type="radio" :checked="step.scope === 'fan-out'" @change="patch(i, 'scope', 'fan-out')" /> fan-out</label>
          <label><input type="radio" :checked="step.action === 'run'" @change="patch(i, 'action', 'run')" /> run</label>
          <label><input type="radio" :checked="step.action === 'sync'" @change="patch(i, 'action', 'sync')" /> sync</label>
        </div>
        <template v-if="step.action === 'run'">
          <input class="step-input" placeholder="命令" :value="step.command" @input="patch(i, 'command', ($event.target as HTMLInputElement).value)" />
          <input class="step-input" placeholder="工作目录" :value="step.work_dir" @input="patch(i, 'work_dir', ($event.target as HTMLInputElement).value)" />
        </template>
        <template v-else>
          <input class="step-input" placeholder="同步源路径" :value="step.sync_from" @input="patch(i, 'sync_from', ($event.target as HTMLInputElement).value)" />
          <input class="step-input" placeholder="同步目标路径" :value="step.sync_to" @input="patch(i, 'sync_to', ($event.target as HTMLInputElement).value)" />
        </template>
      </div>
      <button type="button" class="step-add" data-test="step-add" @click="addStep">+ 添加步骤</button>
    </template>
  </div>
</template>

<style scoped>
.pl-enable, .step-add {
  padding: 3px 8px;
  font-size: 11px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
}
.pl-head {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
.pl-disable {
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.step-card {
  border: 1px solid var(--border-secondary);
  padding: 8px;
  margin-bottom: 6px;
}
.step-toolbar {
  display: flex;
  gap: 4px;
  margin-bottom: 4px;
}
.step-toolbar button {
  padding: 1px 6px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}
.step-input {
  display: block;
  width: 100%;
  margin-bottom: 4px;
  padding: 3px 6px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.step-radios {
  display: flex;
  gap: 10px;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
</style>
