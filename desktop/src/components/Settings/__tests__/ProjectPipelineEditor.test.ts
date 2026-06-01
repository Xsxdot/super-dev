import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import type { Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([{ id: 'h1', name: 'host-1' }]),
      putProjectSetup: vi.fn().mockResolvedValue({ id: 'p1' }),
      listProjects: vi.fn().mockResolvedValue([]),
    },
  }
})

function project(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    variables: { app_name: 'demo' },
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    services: [{
      id: 's1',
      project_id: 'p1',
      name: 'web',
      status: '',
      required: false,
      order: 0,
      deployments: [],
    }],
    pipelines: [],
  }
}

describe('ProjectPipelineEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('渲染项目流水线编辑弹窗', async () => {
    const wrapper = mount(ProjectPipelineEditor, { props: { project: project(), pipelineTemplates: [] } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).toContain('编辑流水线 · demo')
    expect(wrapper.find('[data-test="add-project-pipeline"]').exists()).toBe(true)
  })

  it('保存项目级 pipeline 并保留非流水线配置', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mount(ProjectPipelineEditor, { props: { project: project(), pipelineTemplates: [] } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="add-project-pipeline"]').trigger('click')
    await wrapper.find('[data-test="project-pipeline-name"]').setValue('Deploy Dev')
    await wrapper.find('[data-test="pipeline-config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      variables: { app_name: 'demo' },
      environments: expect.any(Array),
      services: expect.any(Array),
      pipelines: [expect.objectContaining({ name: 'Deploy Dev' })],
    }))
    expect(wrapper.emitted('saved')).toBeTruthy()
  })

  it('点击取消 emit cancel', async () => {
    const wrapper = mount(ProjectPipelineEditor, { props: { project: project(), pipelineTemplates: [] } })
    await wrapper.find('[data-test="pipeline-config-cancel"]').trigger('click')

    expect(wrapper.emitted('cancel')).toBeTruthy()
  })
})
