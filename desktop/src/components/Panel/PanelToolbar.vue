<script setup lang="ts">
import { ref, computed } from 'vue'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useLogStore } from '@/stores/log'
import { useAgentStore } from '@/stores/agent'
import RuleManagerModal from './RuleManagerModal.vue'
const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
}>()

const emit = defineEmits<{
  endBookmark: []
}>()

const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const logStore = useLogStore()
const agentStore = useAgentStore()

const chipInput = ref('')
const showRules = ref(false)
const rulesInitialMode = ref<'list' | 'current'>('list')
const panel = computed(() => filterStore.getPanel(props.panelId))
const bookmark = computed(() => bookmarkStore.getBookmark(props.panelId))
const rules = computed(() => props.projectId ? (filterStore.projectRules[props.projectId] ?? []) : [])

function submitChip() {
  const parts = chipInput.value.split(/[,;\t\n]+/).map(s => s.trim()).filter(Boolean)
  for (const p of parts) {
    filterStore.addChip(props.panelId, p, panel.value.nextChipType)
  }
  chipInput.value = ''
}

function scopeServiceIds(): string[] {
  if (props.serviceId) return [props.serviceId]
  if (!props.projectId) return []
  const project = agentStore.projectById(props.projectId)
  return project?.services.map(s => s.id) ?? []
}

function closeActiveFoldsForScope() {
  for (const serviceId of scopeServiceIds()) {
    logStore.closeActiveFoldForService(serviceId)
  }
}

function startBookmark() {
  closeActiveFoldsForScope()
  bookmarkStore.startBookmark(props.panelId, props.serviceId)
}
function endBookmark() {
  emit('endBookmark')
}

function fillChipInput(text: string) {
  chipInput.value = text.trim()
}

defineExpose({ fillChipInput })

function openRuleManager(mode: 'list' | 'current') {
  if (!props.projectId) return
  rulesInitialMode.value = mode
  showRules.value = true
}

function clearBookmark() {
  bookmarkStore.clearBookmark(props.panelId)
}
async function copyBookmark() {
  const text = bookmarkStore.formatBookmark(props.panelId)
  if (!text.trim()) return
  await navigator.clipboard.writeText(text)
}

function resolveExportPath(selected: string, defaultName: string): string {
  if (/\.(log|txt)$/i.test(selected)) return selected
  const sep = selected.includes('\\') ? '\\' : '/'
  return selected.endsWith(sep) ? `${selected}${defaultName}` : `${selected}${sep}${defaultName}`
}

async function exportBookmark() {
  const text = bookmarkStore.formatBookmark(props.panelId)
  if (!text.trim()) {
    window.alert('书签区间内没有可导出的日志')
    return
  }

  const defaultName = `superdev-log-${Date.now()}.log`
  const { save } = await import('@tauri-apps/plugin-dialog')
  const selected = await save({
    defaultPath: defaultName,
    title: '导出书签日志',
    filters: [{ name: 'Log', extensions: ['log', 'txt'] }],
  })
  if (!selected) return

  const filePath = resolveExportPath(selected, defaultName)
  try {
    const { writeTextFile } = await import('@tauri-apps/plugin-fs')
    await writeTextFile(filePath, text)
  } catch (err) {
    console.error('[SuperDev] export bookmark failed:', err)
    window.alert(`导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}
</script>

<template>
  <div class="toolbar">
    <!-- 过滤区 -->
    <div class="filter-area">
      <svg class="search-icon" width="12" height="12" viewBox="0 0 16 16" fill="none">
        <circle cx="6.5" cy="6.5" r="4.5" stroke="#6e7681" stroke-width="1.5"/>
        <line x1="10.5" y1="10.5" x2="14" y2="14" stroke="#6e7681" stroke-width="1.5" stroke-linecap="round"/>
      </svg>

      <!-- include/exclude picker -->
      <div class="segmented">
        <button
          :class="{ active: panel.nextChipType === 'include' }"
          @click="filterStore.setNextChipType(panelId, 'include')"
        >包含</button>
        <button
          :class="{ active: panel.nextChipType === 'exclude' }"
          @click="filterStore.setNextChipType(panelId, 'exclude')"
        >排除</button>
      </div>

      <!-- 关键词输入 -->
      <input
        v-model="chipInput"
        class="chip-input"
        :placeholder="panel.chips.length ? '添加关键词…' : '关键词过滤，回车添加'"
        @keydown.enter="submitChip"
      />

      <!-- 临时 chips -->
      <div
        v-for="chip in panel.chips"
        :key="chip.id"
        class="chip"
        :class="chip.type"
      >
        <button class="chip-type" @click="filterStore.toggleChipType(panelId, chip.id)">
          {{ chip.type === 'include' ? '+' : '−' }}
        </button>
        <span>{{ chip.keyword }}</span>
        <button class="chip-remove" @click="filterStore.removeChip(panelId, chip.id)">✕</button>
      </div>

      <!-- AND/OR toggle -->
      <button v-if="panel.chips.length > 1" class="logic-btn" @click="filterStore.toggleLogic(panelId)">
        {{ panel.logic.toUpperCase() }}
      </button>
    </div>

    <!-- 持久规则快捷开关区 -->
    <template v-if="rules.length">
      <div class="rules-divider" />
      <div class="rules-area">
        <span class="rules-label">规则</span>
        <button
          v-for="rule in rules"
          :key="rule.id"
          class="rule-chip"
          :class="{ enabled: rule.enabled }"
          @click="filterStore.toggleRule(projectId!, rule.id)"
          :title="rule.enabled ? '点击禁用' : '点击启用'"
        >
          <span class="rule-arrow">{{ rule.type === 'include' ? '↑' : '↓' }}</span>
          <span :class="{ strikethrough: !rule.enabled }">{{ rule.name || rule.keywords[0] }}</span>
        </button>
      </div>
    </template>

    <div class="flex-1" />

    <!-- 操作区 -->
    <button
      class="rules-btn"
      title="管理过滤规则"
      :disabled="!projectId"
      @click="openRuleManager('list')"
    >
      ⚙
    </button>
    <button
      v-if="panel.chips.length && projectId"
      class="save-rule-btn"
      title="保存当前过滤为规则"
      @click="openRuleManager('current')"
    >
      保存为规则
    </button>
    <RuleManagerModal
      v-if="showRules && projectId"
      :project-id="projectId"
      :panel-id="panelId"
      :initial-mode="rulesInitialMode"
      @close="showRules = false"
    />

    <div class="divider" />

    <!-- 书签区 -->
    <template v-if="!bookmark || bookmark.state === 'idle'">
      <button class="bookmark-btn start" title="开始书签录制" @click="startBookmark">⏺</button>
    </template>
    <template v-else-if="bookmark.state === 'recording'">
      <span class="record-count">● {{ bookmark.lockedLogs.length }} 条</span>
      <button class="bookmark-btn stop" title="结束录制" @click="endBookmark">⏹</button>
    </template>
    <template v-else>
      <span class="done-count">{{ bookmark.lockedLogs.length }} 条</span>
      <button class="icon-btn" title="复制" @click="copyBookmark">⎘</button>
      <button class="icon-btn" title="导出" @click="exportBookmark">↑</button>
      <button class="icon-btn" title="清除" @click="clearBookmark">✕</button>
      <button class="bookmark-btn start" title="重新开始" @click="startBookmark">⏺</button>
    </template>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
  overflow-x: auto;
  min-height: 32px;
}
.filter-area {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  flex-shrink: 1;
  overflow: visible;
}
.search-icon { flex-shrink: 0; }
.segmented {
  display: flex;
  border: 1px solid var(--border);
  border-radius: 4px;
  overflow: hidden;
  flex-shrink: 0;
}
.segmented button {
  padding: 2px 7px;
  font-size: 10px;
  background: transparent;
  border: none;
  color: var(--text-secondary);
  cursor: pointer;
}
.segmented button.active {
  background: rgba(31,111,235,0.2);
  color: #58a6ff;
}
.chip-input {
  flex: 1;
  min-width: 80px;
  max-width: 150px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 7px;
  font-size: 10px;
  color: var(--text-primary);
  outline: none;
}
.chip {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 5px;
  border-radius: 4px;
  font-size: 10px;
  flex-shrink: 0;
}
.chip.include { background: rgba(31,111,235,0.12); border: 1px solid rgba(31,111,235,0.3); }
.chip.exclude { background: rgba(210,153,34,0.12); border: 1px solid rgba(210,153,34,0.3); }
.chip-type {
  background: transparent;
  border: none;
  font-size: 9px;
  font-weight: 700;
  cursor: pointer;
  padding: 0;
}
.chip.include .chip-type { color: #58a6ff; }
.chip.exclude .chip-type { color: #d29922; }
.chip-remove {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 8px;
  cursor: pointer;
  padding: 0;
}
.logic-btn {
  padding: 2px 6px;
  background: var(--bg-overlay);
  border: none;
  border-radius: 4px;
  color: var(--text-secondary);
  font-size: 10px;
  font-weight: 700;
  cursor: pointer;
  flex-shrink: 0;
}
.rules-divider {
  width: 1px;
  height: 14px;
  background: var(--border);
  flex-shrink: 0;
  margin: 0 4px;
}
.rules-area {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.rules-label {
  font-size: 9px;
  font-weight: 600;
  color: var(--text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  white-space: nowrap;
}
.rule-chip {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 10px;
  cursor: pointer;
  border: 1px solid transparent;
  background: rgba(255,255,255,0.04);
  color: var(--text-secondary);
  flex-shrink: 0;
}
.rule-chip.enabled {
  background: rgba(63,185,80,0.10);
  border-color: rgba(63,185,80,0.30);
  color: #3fb950;
}
.rule-arrow { font-size: 9px; }
.strikethrough { text-decoration: line-through; opacity: 0.5; }

.divider { width: 1px; height: 14px; background: var(--border); flex-shrink: 0; margin: 0 2px; }
.flex-1 { flex: 1; }

.icon-btn {
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 11px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 3px;
  flex-shrink: 0;
  white-space: nowrap;
}
.icon-btn:hover { color: var(--text-primary); background: var(--bg-overlay); }

.rules-btn {
  width: 28px;
  height: 24px;
  border-radius: 5px;
  border: 1px solid var(--border);
  background: var(--bg-overlay);
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}
.rules-btn:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: rgba(88, 166, 255, 0.45);
}
.rules-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.save-rule-btn {
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--bg-overlay);
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  padding: 4px 8px;
  font-size: 11px;
  white-space: nowrap;
}
.save-rule-btn:hover {
  color: var(--text-primary);
  border-color: rgba(88, 166, 255, 0.45);
}

.bookmark-btn {
  background: transparent;
  border: none;
  font-size: 15px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
}
.bookmark-btn.start { color: #3fb950; }
.bookmark-btn.stop { color: #f85149; }

.record-count {
  padding: 2px 8px;
  background: rgba(248,81,73,0.1);
  border: 1px solid rgba(248,81,73,0.3);
  border-radius: 4px;
  color: #f85149;
  font-size: 10px;
  font-weight: 700;
  flex-shrink: 0;
}
.done-count {
  color: var(--text-secondary);
  font-size: 10px;
  flex-shrink: 0;
}
</style>
