// filterStore 按 panelId 维护临时 chip 过滤状态，按 projectId 缓存 LogRule。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogRule, type LogEntry } from '@/api/agent'

export type ChipType = 'include' | 'exclude'
export type ChipLogic = 'and' | 'or'

export interface FilterChip {
  id: string
  keyword: string
  type: ChipType
}

export interface RuleDraft {
  name: string
  type: ChipType
  keywords: string[]
  logic: ChipLogic
  enabled: boolean
}

export interface PanelRuleDraft {
  name: string
  type: ChipType
  logic: ChipLogic
  enabled: boolean
}

interface PanelFilter {
  chips: FilterChip[]
  logic: ChipLogic
  nextChipType: ChipType
}

function cleanKeywords(keywords: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const keyword of keywords) {
    const trimmed = keyword.trim()
    const key = trimmed.toLowerCase()
    if (!trimmed || seen.has(key)) continue
    seen.add(key)
    out.push(trimmed)
  }
  return out
}

export const useFilterStore = defineStore('filter', () => {
  // 每个面板的临时 chip 状态
  const panelFilters = ref<Record<string, PanelFilter>>({})
  // 每个项目的 LogRule 缓存
  const projectRules = ref<Record<string, LogRule[]>>({})

  function getPanel(panelId: string): PanelFilter {
    if (!panelFilters.value[panelId]) {
      panelFilters.value[panelId] = { chips: [], logic: 'or', nextChipType: 'include' }
    }
    return panelFilters.value[panelId]
  }

  function addChip(panelId: string, keyword: string, type: ChipType) {
    const trimmed = keyword.trim()
    if (!trimmed) return
    const panel = getPanel(panelId)
    if (panel.chips.some(c => c.keyword.toLowerCase() === trimmed.toLowerCase())) return
    panel.chips.push({ id: uuidv4(), keyword: trimmed, type })
  }

  function removeChip(panelId: string, chipId: string) {
    const panel = getPanel(panelId)
    panel.chips = panel.chips.filter(c => c.id !== chipId)
  }

  function toggleChipType(panelId: string, chipId: string) {
    const panel = getPanel(panelId)
    const chip = panel.chips.find(c => c.id === chipId)
    if (chip) chip.type = chip.type === 'include' ? 'exclude' : 'include'
  }

  function toggleLogic(panelId: string) {
    const panel = getPanel(panelId)
    panel.logic = panel.logic === 'and' ? 'or' : 'and'
  }

  function setNextChipType(panelId: string, type: ChipType) {
    getPanel(panelId).nextChipType = type
  }

  function clearChips(panelId: string) {
    if (panelFilters.value[panelId]) {
      panelFilters.value[panelId].chips = []
    }
  }

  function removePanel(panelId: string) {
    delete panelFilters.value[panelId]
  }

  async function loadProjectRules(projectId: string) {
    const rules = await api.getProjectRules(projectId)
    projectRules.value[projectId] = rules
  }

  async function saveProjectRules(projectId: string, rules: LogRule[]) {
    const saved = await api.putProjectRules(projectId, rules)
    projectRules.value[projectId] = saved
  }

  async function createRule(projectId: string, draft: RuleDraft) {
    const current = projectRules.value[projectId] ?? []
    const keywords = cleanKeywords(draft.keywords)
    if (keywords.length === 0) return
    const rule: LogRule = {
      id: uuidv4(),
      name: draft.name.trim() || keywords[0] || '未命名规则',
      type: draft.type,
      keywords,
      logic: draft.logic,
      enabled: draft.enabled,
    }
    await saveProjectRules(projectId, [...current, rule])
  }

  async function updateRule(projectId: string, ruleId: string, draft: RuleDraft) {
    const current = projectRules.value[projectId] ?? []
    const next = current
      .map(rule => {
        if (rule.id !== ruleId) return rule
        const keywords = cleanKeywords(draft.keywords)
        return {
          ...rule,
          name: draft.name.trim() || keywords[0] || rule.name,
          type: draft.type,
          keywords,
          logic: draft.logic,
          enabled: draft.enabled,
        }
      })
      .filter(rule => rule.keywords.length > 0)
    await saveProjectRules(projectId, next)
  }

  async function deleteRule(projectId: string, ruleId: string) {
    const current = projectRules.value[projectId] ?? []
    await saveProjectRules(projectId, current.filter(rule => rule.id !== ruleId))
  }

  async function savePanelChipsAsRule(projectId: string, panelId: string, draft: PanelRuleDraft) {
    const panel = getPanel(panelId)
    const keywords = panel.chips
      .filter(chip => chip.type === draft.type)
      .map(chip => chip.keyword)
    await createRule(projectId, {
      ...draft,
      keywords,
    })
  }

  async function toggleRule(projectId: string, ruleId: string) {
    const rules = projectRules.value[projectId]
    if (!rules) return
    const next = rules.map(rule =>
      rule.id === ruleId ? { ...rule, enabled: !rule.enabled } : rule,
    )
    await saveProjectRules(projectId, next)
  }

  // 核心过滤函数：先应用 LogRule，再应用 chip
  function applyFilters<T extends LogEntry>(panelId: string, projectId: string | null, logs: T[]): T[] {
    let result = logs

    // 应用项目级 LogRule
    const rules = projectId ? (projectRules.value[projectId] ?? []) : []
    const enabledRules = rules.filter(r => r.enabled)

    for (const rule of enabledRules) {
      if (rule.type === 'include') {
        result = result.filter(log =>
          rule.logic === 'and'
            ? rule.keywords.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
            : rule.keywords.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
        )
      } else {
        result = result.filter(log =>
          rule.logic === 'and'
            ? !rule.keywords.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
            : !rule.keywords.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
        )
      }
    }

    // 应用面板临时 chip
    const panel = panelFilters.value[panelId]
    if (!panel || panel.chips.length === 0) return result

    const includes = panel.chips.filter(c => c.type === 'include').map(c => c.keyword)
    const excludes = panel.chips.filter(c => c.type === 'exclude').map(c => c.keyword)
    const logic = panel.logic

    if (includes.length > 0) {
      result = result.filter(log =>
        logic === 'and'
          ? includes.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
          : includes.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
      )
    }
    if (excludes.length > 0) {
      result = result.filter(log =>
        logic === 'and'
          ? !excludes.every(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
          : !excludes.some(kw => log.message.toLowerCase().includes(kw.toLowerCase()))
      )
    }

    return result
  }

  return {
    panelFilters,
    projectRules,
    getPanel,
    addChip,
    removeChip,
    toggleChipType,
    toggleLogic,
    setNextChipType,
    clearChips,
    removePanel,
    loadProjectRules,
    saveProjectRules,
    createRule,
    updateRule,
    deleteRule,
    savePanelChipsAsRule,
    toggleRule,
    applyFilters,
  }
})
