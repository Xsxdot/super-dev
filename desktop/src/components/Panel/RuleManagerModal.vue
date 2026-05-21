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
import { computed, onMounted, reactive, ref } from 'vue'
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
  keywords: '',
  logic: 'or' as ChipLogic,
  enabled: true,
})

const rules = computed(() => filterStore.projectRules[props.projectId] ?? [])
const panel = computed(() => filterStore.getPanel(props.panelId))
const hasCurrentChips = computed(() => panel.value.chips.length > 0)

function resetForm() {
  editingRuleId.value = null
  savingFromPanel.value = false
  form.name = ''
  form.type = 'include'
  form.keywords = ''
  form.logic = 'or'
  form.enabled = true
}

function startNewRule() {
  resetForm()
}

function startEdit(rule: LogRule) {
  editingRuleId.value = rule.id
  savingFromPanel.value = false
  form.name = rule.name
  form.type = rule.type
  form.keywords = rule.keywords.join(', ')
  form.logic = rule.logic
  form.enabled = rule.enabled
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
    .join(', ')
}

function splitKeywords(): string[] {
  return form.keywords.split(/[,;\t\n]+/).map(s => s.trim()).filter(Boolean)
}

async function saveRule() {
  if (savingFromPanel.value) {
    await filterStore.savePanelChipsAsRule(props.projectId, props.panelId, {
      name: form.name,
      type: form.type,
      keywords: splitKeywords(),
      logic: form.logic,
      enabled: form.enabled,
    })
    resetForm()
    return
  }

  const draft = {
    name: form.name,
    type: form.type,
    keywords: splitKeywords(),
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
          <label>
            关键词
            <textarea data-test="rule-keywords" v-model="form.keywords" />
          </label>
          <div class="form-row">
            <label>
              类型
              <select v-model="form.type">
                <option value="include">包含</option>
                <option value="exclude">排除</option>
              </select>
            </label>
            <label>
              逻辑
              <select v-model="form.logic">
                <option value="or">OR</option>
                <option value="and">AND</option>
              </select>
            </label>
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
label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
input,
textarea,
select {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 6px 8px;
  font-size: 12px;
}
textarea {
  min-height: 86px;
  resize: vertical;
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
