/**
 * ProjectSetupModal 测试项目环境配置引导弹窗。
 *
 * 职责：
 *   - 验证步骤 1 显示两个入口按钮
 *   - 验证从头创建路径展示 service 行
 *   - 验证从头创建确认后 emit done
 *   - 验证从 launch.json 导入路径调用 API 并展示结果
 *   - 验证从 launch.json 导入确认后 emit done
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不访问真实文件系统
 */
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ProjectSetupModal from '@/components/Settings/ProjectSetupModal.vue'
import type { Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      getVscodeLaunch: vi.fn().mockResolvedValue([
        { name: 'web', command: 'go run .', work_dir: '/proj' },
      ]),
      putProjectSetup: vi.fn().mockResolvedValue({
        id: 'proj-1',
        name: 'myapp',
        root_path: '/proj',
        services: [
          {
            id: 'svc-1',
            project_id: 'proj-1',
            name: 'web',
            command: 'go run .',
            work_dir: '/proj',
            required: false,
            order: 0,
            status: '',
          },
        ],
        selected_service_ids: [],
        environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
      }),
    },
  }
})

vi.mock('@/stores/agent', () => ({
  useAgentStore: () => ({
    reloadProject: vi.fn().mockResolvedValue(undefined),
  }),
}))

const mockProject: Project = {
  id: 'proj-1',
  name: 'myapp',
  root_path: '/proj',
  services: [
    {
      id: 'svc-1',
      project_id: 'proj-1',
      name: 'web',
      command: 'go run .',
      work_dir: '/proj',
      required: false,
      order: 0,
      status: '',
    },
  ],
  selected_service_ids: [],
  environments: [],
}

describe('ProjectSetupModal', () => {
  it('步骤 1 显示两个入口按钮', () => {
    const wrapper = mount(ProjectSetupModal, {
      props: { visible: true, project: mockProject },
    })

    expect(wrapper.find('[data-test="setup-from-scratch"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="setup-from-launch"]').exists()).toBe(true)
  })

  it('从头创建：展示 service 行', async () => {
    const wrapper = mount(ProjectSetupModal, {
      props: { visible: true, project: mockProject },
    })

    await wrapper.find('[data-test="setup-from-scratch"]').trigger('click')
    await nextTick()

    const rows = wrapper.findAll('[data-test="setup-service-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('web')
  })

  it('从头创建：确认后 emit done', async () => {
    const wrapper = mount(ProjectSetupModal, {
      props: { visible: true, project: mockProject },
    })

    await wrapper.find('[data-test="setup-from-scratch"]').trigger('click')
    await nextTick()
    await wrapper.find('[data-test="setup-confirm"]').trigger('click')
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.emitted('done')).toBeTruthy()
  })

  it('从 launch.json 导入：调用 API 并展示结果', async () => {
    const wrapper = mount(ProjectSetupModal, {
      props: { visible: true, project: mockProject },
    })

    await wrapper.find('[data-test="setup-from-launch"]').trigger('click')
    await new Promise(resolve => setTimeout(resolve))
    await nextTick()

    const rows = wrapper.findAll('[data-test="launch-import-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('web')
  })

  it('从 launch.json 导入：确认后 emit done', async () => {
    const wrapper = mount(ProjectSetupModal, {
      props: { visible: true, project: mockProject },
    })

    await wrapper.find('[data-test="setup-from-launch"]').trigger('click')
    await new Promise(resolve => setTimeout(resolve))
    await nextTick()
    await wrapper.find('[data-test="setup-confirm"]').trigger('click')
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.emitted('done')).toBeTruthy()
  })
})
