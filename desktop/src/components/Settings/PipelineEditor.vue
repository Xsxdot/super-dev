<!--
PipelineEditor：deployment 流水线（有序 Step 列表）编辑器。

职责：
  - 无 pipeline 时提供「配置流水线」入口（emit { steps: [] }）
  - 有 pipeline 时渲染可增删、可上下移的步骤卡片
  - 按 action 切换显示 run（command/work_dir）或 sync（from/to）字段
  - 选择 sync action 时自动强制 scope 为 fan-out，防止 local+sync 非法组合

边界：
  - 不做校验（校验在 configDraft.validateDraft）
  - Step ID 为空时本地补 step-{n}
-->
<script setup lang="ts">
import type { Pipeline, PipelineStep, StepAction } from '@/api/agent'

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

// patchAction 切换 action 时处理联动逻辑：
// 切到 sync 时后端 local_executor 会直接报错，故同时强制把 scope 改为 fan-out。
// 切回 run 时不干预 scope，让用户自行决定在哪执行。
function patchAction(i: number, action: StepAction) {
  const steps = props.modelValue!.steps.map((s, k) => {
    if (k !== i) return s
    // sync 只允许在 fan-out 下运行，强制修正 scope
    const scope = action === 'sync' ? 'fan-out' : s.scope
    return { ...s, action, scope }
  })
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
        <label class="step-field-label">步骤名</label>
        <input
          class="step-input" placeholder="步骤名"
          :value="step.name" @input="patch(i, 'name', ($event.target as HTMLInputElement).value)"
        />
        <div class="step-dims">
          <div class="step-dim-row">
            <span class="step-dim-label">在哪执行</span>
            <label title="在本机执行一次（如打包、构建产物）">
              <input type="radio" :checked="step.scope === 'local'" :disabled="step.action === 'sync'" @change="patch(i, 'scope', 'local')" />
              本机一次
            </label>
            <label title="对每台目标主机并行执行（如分发、重启）">
              <input type="radio" :checked="step.scope === 'fan-out'" @change="patch(i, 'scope', 'fan-out')" />
              每台目标机
            </label>
          </div>
          <div class="step-dim-row">
            <span class="step-dim-label">做什么</span>
            <label title="运行一条 shell 命令">
              <input type="radio" :checked="step.action === 'run'" @change="patchAction(i, 'run')" />
              执行命令
            </label>
            <label title="把本地文件传到各目标主机">
              <input type="radio" :checked="step.action === 'sync'" @change="patchAction(i, 'sync')" />
              同步文件
            </label>
          </div>
        </div>
        <template v-if="step.action === 'run'">
          <label class="step-field-label">执行命令</label>
          <input class="step-input" placeholder="命令" :value="step.command" @input="patch(i, 'command', ($event.target as HTMLInputElement).value)" />
          <label class="step-field-label">工作目录</label>
          <input class="step-input" placeholder="工作目录" :value="step.work_dir" @input="patch(i, 'work_dir', ($event.target as HTMLInputElement).value)" />
        </template>
        <template v-else>
          <label class="step-field-label">源路径</label>
          <input class="step-input" placeholder="同步源路径" :value="step.sync_from" @input="patch(i, 'sync_from', ($event.target as HTMLInputElement).value)" />
          <label class="step-field-label">目标路径</label>
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
.step-field-label {
  display: block;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 2px;
  font-weight: 600;
}
.step-dims {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 6px;
}
.step-dim-row {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 11px;
  color: var(--text-secondary);
}
.step-dim-label {
  font-weight: 600;
  min-width: 56px;
  font-size: 11px;
  color: var(--text-secondary);
}
</style>
