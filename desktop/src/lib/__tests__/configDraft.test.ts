import { describe, it, expect } from 'vitest'
import { projectToDraft, draftToPayload, validateDraft } from '@/lib/configDraft'
import type { Project } from '@/api/agent'

function makeProject(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [
      {
        id: 's1', project_id: 'p1', name: 'web', status: '', command: '', work_dir: '',
        required: false, order: 0,
        deployments: [
          { id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '/tmp/demo', env: { A: '1' }, status: '' },
        ],
      },
    ],
    selected_service_ids: [],
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
  }
}

describe('configDraft', () => {
  it('projectToDraft 深拷贝，修改草稿不影响原对象', () => {
    const p = makeProject()
    const draft = projectToDraft(p)
    draft.services[0].name = 'changed'
    expect(p.services[0].name).toBe('web')
  })

  it('draftToPayload 拍平为 SetupPayload，忽略空 key 的 env 变量', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].env = { A: '1', '': 'ignored' }
    const payload = draftToPayload(draft)
    expect(payload.environments).toHaveLength(1)
    expect(payload.services[0].name).toBe('web')
    expect(payload.services[0].deployments[0].env).toEqual({ A: '1' })
  })

  it('validateDraft：env 名称为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.environments[0].name = ''
    expect(validateDraft(draft)).toContain('环境名称不能为空')
  })

  it('validateDraft：service 名称重复报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services.push({ ...draft.services[0], id: 's2' })
    expect(validateDraft(draft).some(e => e.includes('服务名'))).toBe(true)
  })

  it('validateDraft：local deployment 命令为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].command = ''
    expect(validateDraft(draft).some(e => e.includes('命令'))).toBe(true)
  })

  it('validateDraft：remote deployment 未选 host 报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0] = {
      id: 'd1', env_name: 'dev', location: 'remote', host_ids: [], status: '',
    } as never
    expect(validateDraft(draft).some(e => e.includes('主机'))).toBe(true)
  })

  it('validateDraft：合法草稿返回空数组', () => {
    expect(validateDraft(projectToDraft(makeProject()))).toEqual([])
  })
})
