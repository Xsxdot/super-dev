import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect, vi } from 'vitest'
import { useFilterStore } from '../filter'
import { api as agentApi, type LogEntry, type LogRule } from '@/api/agent'

function makeLog(message: string, id = 1): LogEntry {
  return { id, service_id: 'svc', run_id: 'run', timestamp: '', level: 'INFO', message, stream: 'stdout' }
}

describe('filterStore', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    setActivePinia(createPinia())
  })

  it('addChip 添加不重复的 chip', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    store.addChip('p1', 'error', 'include')  // 重复，不添加
    expect(store.getPanel('p1').chips).toHaveLength(1)
  })

  it('applyFilters：include chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    const logs = [makeLog('error occurred'), makeLog('info message')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })

  it('applyFilters：exclude chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'debug', 'exclude')
    const logs = [makeLog('debug info'), makeLog('error occurred')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })

  it('createRule 新增项目规则并持久化', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.createRule('proj-1', {
      name: 'Errors',
      type: 'include',
      keywords: ['error'],
      logic: 'or',
      enabled: true,
    })

    expect(store.projectRules['proj-1']).toHaveLength(1)
    expect(store.projectRules['proj-1'][0].name).toBe('Errors')
    expect(agentApi.putProjectRules).toHaveBeenCalledWith('proj-1', store.projectRules['proj-1'])
  })

  it('savePanelChipsAsRule 将当前面板 chip 保存为项目规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    store.addChip('panel-1', 'error', 'include')
    store.addChip('panel-1', 'timeout', 'include')
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.savePanelChipsAsRule('proj-1', 'panel-1', {
      name: 'Errors and timeouts',
      type: 'include',
      logic: 'or',
      enabled: true,
    })

    const saved = store.projectRules['proj-1'][0]
    expect(saved.keywords).toEqual(['error', 'timeout'])
    expect(saved.name).toBe('Errors and timeouts')
  })

  it('deleteRule 删除项目规则并持久化', async () => {
    const store = useFilterStore()
    const rule: LogRule = {
      id: 'rule-1',
      name: 'Noise',
      type: 'exclude',
      keywords: ['health'],
      logic: 'or',
      enabled: true,
    }
    store.projectRules['proj-1'] = [rule]
    vi.spyOn(agentApi, 'putProjectRules').mockImplementation(async (_projectId, rules) => rules)

    await store.deleteRule('proj-1', 'rule-1')

    expect(store.projectRules['proj-1']).toEqual([])
  })
})
