<!--
项目过滤规则管理弹层

职责：
  - 管理当前项目的 LogRule 列表
  - 支持从当前面板临时 chip 保存为项目规则

边界：
  - 不执行日志过滤计算
  - 不管理跨服务搜索页的隐藏服务状态
-->
<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useFilterStore, type ChipLogic, type ChipType } from '@/stores/filter'
import type { LogRule } from '@/api/agent'

const props = withDefaults(defineProps<{
  projectId: string
  panelId: string
  initialMode?: 'list' | 'current'
}>(), {
  initialMode: 'list',
})

const emit = defineEmits<{
  close: []
}>()

const filterStore = useFilterStore()
const editingRuleId = ref<string | null>(null)
const savingFromPanel = ref(false)
const form = reactive({
  name: '',
  type: 'include' as ChipType,
  keywords: [] as string[],
  logic: 'or' as ChipLogic,
  enabled: true,
})

// tag 输入器状态
const tagInput = ref('')
const tagInputEl = ref<HTMLInputElement | null>(null)

const rules = computed(() => filterStore.projectRules[props.projectId] ?? [])
const panel = computed(() => filterStore.getPanel(props.panelId))
const hasCurrentChips = computed(() => panel.value.chips.length > 0)

function resetForm() {
  editingRuleId.value = null
  savingFromPanel.value = false
  form.name = ''
  form.type = 'include'
  form.keywords = []
  form.logic = 'or'
  form.enabled = true
  tagInput.value = ''
}

function startNewRule() {
  resetForm()
}

function startEdit(rule: LogRule) {
  editingRuleId.value = rule.id
  savingFromPanel.value = false
  form.name = rule.name
  form.type = rule.type
  form.keywords = [...rule.keywords]
  form.logic = rule.logic
  form.enabled = rule.enabled
  tagInput.value = ''
}

function startSaveCurrentFilter() {
  resetForm()
  savingFromPanel.value = true
  const firstType = panel.value.chips[0]?.type ?? 'include'
  form.type = firstType
  form.logic = panel.value.logic
  form.keywords = panel.value.chips
    .filter(chip => chip.type === firstType)
    .map(chip => chip.keyword)
}

// 将输入框当前值提交为 tag
function commitTag() {
  const raw = tagInput.value.replace(/,|;/g, '').trim()
  if (!raw) return
  if (!form.keywords.some(k => k.toLowerCase() === raw.toLowerCase())) {
    form.keywords.push(raw)
  }
  tagInput.value = ''
}

function onTagInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    commitTag()
  } else if (e.key === 'Backspace' && tagInput.value === '' && form.keywords.length > 0) {
    form.keywords.pop()
  }
}

// 输入逗号/分号时触发提交
function onTagInputInput() {
  if (/[,;]/.test(tagInput.value)) {
    commitTag()
  }
}

function removeKeyword(index: number) {
  form.keywords.splice(index, 1)
}

function focusTagInput() {
  nextTick(() => tagInputEl.value?.focus())
}

async function saveRule() {
  commitTag()
  const keywords = form.keywords.filter(Boolean)

  if (savingFromPanel.value) {
    await filterStore.savePanelChipsAsRule(props.projectId, props.panelId, {
      name: form.name,
      type: form.type,
      keywords,
      logic: form.logic,
      enabled: form.enabled,
    })
    resetForm()
    return
  }

  const draft = {
    name: form.name,
    type: form.type,
    keywords,
    logic: form.logic,
    enabled: form.enabled,
  }
  if (editingRuleId.value) {
    await filterStore.updateRule(props.projectId, editingRuleId.value, draft)
  } else {
    await filterStore.createRule(props.projectId, draft)
  }
  resetForm()
}

async function deleteEditingRule() {
  if (!editingRuleId.value) return
  await filterStore.deleteRule(props.projectId, editingRuleId.value)
  resetForm()
}

onMounted(() => {
  if (props.initialMode === 'current' && hasCurrentChips.value) {
    startSaveCurrentFilter()
  }
})
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <section class="modal" role="dialog" aria-modal="true" aria-label="过滤规则">
      <header class="modal-header">
        <h2>过滤规则</h2>
        <button class="close-btn" title="关闭" @click="emit('close')">×</button>
      </header>

      <div class="modal-body">
        <div class="rule-list">
          <button
            v-for="rule in rules"
            :key="rule.id"
            class="rule-row"
            :class="{ disabled: !rule.enabled }"
            @click="startEdit(rule)"
          >
            <span class="rule-name">{{ rule.name }}</span>
            <span class="rule-meta">{{ rule.type === 'include' ? '包含' : '排除' }} · {{ rule.logic.toUpperCase() }}</span>
            <span class="rule-keywords">{{ rule.keywords.join(', ') }}</span>
          </button>
          <div v-if="rules.length === 0" class="empty-rules">暂无项目规则</div>
        </div>

        <form class="rule-form" @submit.prevent="saveRule">
          <div class="form-actions">
            <button type="button" data-test="new-rule" @click="startNewRule">新建规则</button>
            <button
              type="button"
              data-test="save-current-filter"
              :disabled="!hasCurrentChips"
              @click="startSaveCurrentFilter"
            >
              从当前过滤保存
            </button>
          </div>

          <label>
            名称
            <input data-test="rule-name" v-model="form.name" />
          </label>

          <div class="field">
            <span class="field-label">关键词</span>
            <div class="tag-input" @click="focusTagInput">
              <span
                v-for="(kw, i) in form.keywords"
                :key="i"
                class="tag"
              >
                {{ kw }}
                <button type="button" class="tag-remove" @click.stop="removeKeyword(i)">×</button>
              </span>
              <input
                ref="tagInputEl"
                data-test="rule-keywords"
                class="tag-text-input"
                v-model="tagInput"
                placeholder="输入后回车添加…"
                @keydown="onTagInputKeydown"
                @input="onTagInputInput"
                @blur="commitTag"
              />
            </div>
          </div>

          <div class="form-row">
            <div class="field">
              <span class="field-label">类型</span>
              <div class="toggle-group">
                <button
                  type="button"
                  :class="{ active: form.type === 'include' }"
                  @click="form.type = 'include'"
                >包含</button>
                <button
                  type="button"
                  :class="{ active: form.type === 'exclude' }"
                  @click="form.type = 'exclude'"
                >排除</button>
              </div>
            </div>

            <div class="field">
              <span class="field-label">逻辑</span>
              <div class="toggle-group">
                <button
                  type="button"
                  :class="{ active: form.logic === 'or' }"
                  @click="form.logic = 'or'"
                >OR</button>
                <button
                  type="button"
                  :class="{ active: form.logic === 'and' }"
                  @click="form.logic = 'and'"
                >AND</button>
              </div>
            </div>

            <label class="enabled-check">
              <input type="checkbox" v-model="form.enabled" />
              启用
            </label>
          </div>

          <div class="form-footer">
            <button
              v-if="editingRuleId"
              type="button"
              class="danger"
              @click="deleteEditingRule"
            >
              删除
            </button>
            <span class="spacer" />
            <button type="button" data-test="save-rule" class="primary" @click="saveRule">保存</button>
          </div>
        </form>
      </div>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 1000;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal {
  width: min(720px, calc(100vw - 40px));
  max-height: min(620px, calc(100vh - 40px));
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
}
.modal-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-secondary);
}
h2 {
  margin: 0;
  font-size: 15px;
}
.close-btn {
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 18px;
  cursor: pointer;
}
.modal-body {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) minmax(280px, 1.2fr);
  min-height: 380px;
  overflow: hidden;
}
.rule-list {
  border-right: 1px solid var(--border-secondary);
  overflow-y: auto;
  padding: 8px;
}
.rule-row {
  width: 100%;
  display: grid;
  grid-template-columns: 1fr;
  gap: 3px;
  text-align: left;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}
.rule-row:hover {
  background: var(--bg-overlay);
  border-color: var(--border);
}
.rule-row.disabled {
  opacity: 0.5;
}
.rule-name {
  font-weight: 600;
  font-size: 12px;
}
.rule-meta,
.rule-keywords,
.empty-rules {
  color: var(--text-tertiary);
  font-size: 10px;
}
.rule-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
}
.form-actions,
.form-row,
.form-footer {
  display: flex;
  align-items: center;
  gap: 8px;
}
.form-row {
  align-items: flex-end;
}
/* 通用字段容器（替代 <label> 用于非 input 字段） */
.field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.field-label {
  color: var(--text-secondary);
  font-size: 11px;
}
label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
input {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 6px 8px;
  font-size: 12px;
}
/* tag 输入器容器 */
.tag-input {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 4px 6px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  align-items: center;
  min-height: 34px;
  cursor: text;
}
.tag-input:focus-within {
  border-color: var(--accent);
}
.tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  background: color-mix(in srgb, var(--accent) 15%, transparent);
  border: 1px solid color-mix(in srgb, var(--accent) 40%, transparent);
  border-radius: 4px;
  padding: 1px 6px;
  font-size: 11px;
  color: var(--text-primary);
  white-space: nowrap;
}
.tag-remove {
  background: none;
  border: none;
  color: var(--text-tertiary);
  padding: 0;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.tag-remove:hover {
  color: var(--text-primary);
}
.tag-text-input {
  flex: 1;
  min-width: 80px;
  background: none;
  border: none;
  outline: none;
  color: var(--text-primary);
  font-size: 11px;
  padding: 2px 2px;
}
/* toggle 按钮组 */
.toggle-group {
  display: flex;
  border: 1px solid var(--border);
  border-radius: 5px;
  overflow: hidden;
}
.toggle-group button {
  padding: 4px 11px;
  font-size: 11px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  border: none;
  border-radius: 0;
  cursor: pointer;
}
.toggle-group button + button {
  border-left: 1px solid var(--border);
}
.toggle-group button.active {
  background: var(--accent);
  color: #fff;
}
button {
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--bg-overlay);
  color: var(--text-secondary);
  padding: 5px 9px;
  cursor: pointer;
  font-size: 11px;
}
button:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.enabled-check {
  flex-direction: row;
  align-items: center;
  padding-bottom: 1px;
}
.enabled-check input {
  width: 13px;
  height: 13px;
}
.spacer {
  flex: 1;
}
.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.danger {
  color: var(--status-failed);
}
</style>
