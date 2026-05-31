/**
 * 流水线模板 Store 测试。
 *
 * 职责：
 *   - 验证模板列表加载
 *   - 验证导入模板后刷新列表
 *
 * 边界：
 *   - 不解析模板 YAML
 *   - 不访问真实 agent API
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/agent'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'

describe('pipelineTemplate store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('loads templates', async () => {
    vi.spyOn(api, 'listPipelineTemplates').mockResolvedValue({
      items: [{ source: 'builtin', id: 'go-binary-build', name: 'Go Build', version: '1.0.0', digest: 'sha256:x' }],
    })

    const store = usePipelineTemplateStore()
    await store.loadTemplates()

    expect(store.templates).toHaveLength(1)
    expect(store.templates[0].id).toBe('go-binary-build')
  })

  it('refreshes templates after import', async () => {
    const importTemplate = vi.spyOn(api, 'importPipelineTemplate').mockResolvedValue({
      source: 'user',
      id: 'custom-deploy',
      name: 'Custom Deploy',
      version: '1.0.0',
      digest: 'sha256:y',
    })
    vi.spyOn(api, 'listPipelineTemplates').mockResolvedValue({
      items: [{ source: 'user', id: 'custom-deploy', name: 'Custom Deploy', version: '1.0.0', digest: 'sha256:y' }],
    })

    const store = usePipelineTemplateStore()
    await store.importTemplate('/tmp/custom-deploy.yaml')

    expect(importTemplate).toHaveBeenCalledWith('/tmp/custom-deploy.yaml')
    expect(store.templates[0].source).toBe('user')
  })
})
