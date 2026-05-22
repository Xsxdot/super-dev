<!--
LogSourceFormModal：远程监听任务新建与编辑表单。

职责：
  - 收集 LogSource 的任务名称、采集类型、关联 host_ids、tags 和 extra_args
  - 根据 name + type 实时预览采集命令
  - 支持安全参数配置（固定参数名 + 可编辑值）
  - 将 payload 交由父组件保存

边界：
  - 不直接调用 API
  - 命令预览为纯前端拼接，不请求后端
  - extra_args 只支持预定义的安全参数集合，防止命令注入
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import TagInput from '@/components/Settings/TagInput.vue'
import type { LogSource, LogSourceCreatePayload, LogSourceType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: LogSource | null
}>()

const emit = defineEmits<{
  submit: [payload: LogSourceCreatePayload]
  cancel: []
}>()

const store = useRemoteStore()
const name = ref('')
const type = ref<LogSourceType>('journalctl')
const hostIds = ref<Set<string>>(new Set())
const tags = ref<string[]>([])

interface SafeParam {
  flag: string
  enabled: boolean
  value: string
  valueType: 'input' | 'select'
  options?: string[]
  types: LogSourceType[]
}

const safeParams = ref<SafeParam[]>([
  {
    flag: '--since',
    enabled: false,
    value: '1h',
    valueType: 'input',
    types: ['journalctl', 'docker'],
  },
  {
    flag: '--priority',
    enabled: false,
    value: 'err',
    valueType: 'select',
    options: ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'],
    types: ['journalctl'],
  },
  {
    flag: '--output',
    enabled: false,
    value: 'cat',
    valueType: 'select',
    options: ['cat', 'json', 'short', 'verbose'],
    types: ['journalctl'],
  },
  {
    flag: '--tail',
    enabled: false,
    value: '100',
    valueType: 'input',
    types: ['docker'],
  },
])

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    safeParams.value.forEach(p => { p.enabled = false; p.value = defaultValue(p) })
    if (initial) {
      name.value = initial.name
      type.value = initial.type
      hostIds.value = new Set(initial.host_ids)
      tags.value = [...(initial.tags ?? [])]
      restoreFromExtraArgs(initial.extra_args ?? [])
      return
    }
    name.value = ''
    type.value = 'journalctl'
    hostIds.value = new Set()
    tags.value = []
  },
  { immediate: true },
)

function defaultValue(p: SafeParam): string {
  if (p.flag === '--since') return '1h'
  if (p.flag === '--priority') return 'err'
  if (p.flag === '--output') return 'cat'
  if (p.flag === '--tail') return '100'
  return ''
}

function restoreFromExtraArgs(args: string[]) {
  for (let i = 0; i < args.length; i++) {
    const param = safeParams.value.find(p => p.flag === args[i])
    if (param) {
      param.enabled = true
      if (i + 1 < args.length && !args[i + 1].startsWith('-')) {
        param.value = args[i + 1]
        i++
      }
    }
  }
}

const visibleParams = computed(() =>
  safeParams.value.filter(p => p.types.includes(type.value)),
)

const extraArgs = computed<string[]>(() => {
  const args: string[] = []
  for (const p of visibleParams.value) {
    if (p.enabled) {
      args.push(p.flag)
      if (p.value) args.push(p.value)
    }
  }
  return args
})

const previewCommand = computed(() => {
  const n = name.value.trim() || '<任务名称>'
  let base: string[]
  if (type.value === 'journalctl') {
    base = ['journalctl', '-fu', n, '-o', 'cat', '--no-pager']
  } else {
    base = ['docker', 'logs', '-f', n]
  }
  return [...base, ...extraArgs.value].join(' ')
})

const canSubmit = computed(() => name.value.trim().length > 0 && hostIds.value.size > 0)

function toggleHost(id: string) {
  const next = new Set(hostIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  hostIds.value = next
}

function submit() {
  emit('submit', {
    name: name.value.trim(),
    type: type.value,
    host_ids: Array.from(hostIds.value),
    tags: tags.value,
    extra_args: extraArgs.value,
  })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑监听任务' : '新建监听任务' }}</div>

      <div class="field">
        <label>任务名称 <span class="req">*</span></label>
        <input v-model="name" placeholder="nova-api" data-test="logsource-form-name" />
      </div>

      <div class="field">
        <label>采集类型</label>
        <select v-model="type" data-test="logsource-form-type">
          <option value="journalctl">journalctl</option>
          <option value="docker">docker</option>
        </select>
      </div>

      <div class="field">
        <label>关联主机 <span class="req">*</span></label>
        <div v-if="store.hosts.length > 0" class="host-list">
          <label v-for="host in store.hosts" :key="host.id" class="host-row" data-test="logsource-form-host">
            <input type="checkbox" :checked="hostIds.has(host.id)" @change="toggleHost(host.id)" />
            <span class="hname">{{ host.name }}</span>
            <span class="tags">{{ host.tags.join(', ') || '(无标签)' }}</span>
          </label>
        </div>
        <div v-else class="empty">还没有主机。请先到设置页添加。</div>
      </div>

      <div class="field">
        <label>标签</label>
        <TagInput v-model="tags" data-test="logsource-form-tags" />
      </div>

      <div v-if="name.trim()" class="field">
        <label>命令预览</label>
        <code class="cmd-preview">{{ previewCommand }}</code>
      </div>

      <div v-if="name.trim()" class="field">
        <label>安全参数</label>
        <div class="params">
          <div v-for="param in visibleParams" :key="param.flag" class="param-row">
            <input
              :id="`param-${param.flag}`"
              v-model="param.enabled"
              type="checkbox"
            />
            <label :for="`param-${param.flag}`" class="param-flag">{{ param.flag }}</label>
            <template v-if="param.enabled">
              <input
                v-if="param.valueType === 'input'"
                v-model="param.value"
                class="param-val"
                :placeholder="defaultValue(param)"
              />
              <select v-else v-model="param.value" class="param-val">
                <option v-for="opt in param.options" :key="opt" :value="opt">{{ opt }}</option>
              </select>
            </template>
          </div>
        </div>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="!canSubmit"
          data-test="logsource-form-submit"
          @click="submit"
        >
          保存
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal-body {
  width: min(480px, calc(100vw - 32px));
  max-height: 86vh;
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 10px;
  font-size: 14px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  margin-bottom: 12px;
}
.field > label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req { color: var(--status-failed); }
.field input,
.field select {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.host-list {
  max-height: 200px;
  overflow-y: auto;
  border: 1px solid var(--border-secondary);
}
.host-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px;
  cursor: pointer;
  font-size: 12px;
}
.host-row:hover { background: var(--bg-secondary); }
.hname { font-weight: 600; }
.tags, .empty {
  color: var(--text-tertiary);
  font-size: 11px;
}
.empty { padding: 12px; text-align: center; }
.cmd-preview {
  display: block;
  padding: 6px 8px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  word-break: break-all;
  white-space: pre-wrap;
}
.params {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px 8px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
}
.param-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.param-flag {
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  color: var(--text-secondary);
  min-width: 80px;
}
.param-val {
  flex: 1;
  padding: 2px 6px;
  font-size: 11px;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
button {
  padding: 5px 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
  font-size: 12px;
}
button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
button:disabled { cursor: not-allowed; opacity: 0.5; }
</style>
