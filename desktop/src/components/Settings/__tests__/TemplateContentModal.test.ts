/**
 * TemplateContentModal 测试模板 YAML 只读查看。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TemplateContentModal from '@/components/Settings/TemplateContentModal.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('TemplateContentModal', () => {
  it('uses shared settings modal shell and footer actions', () => {
    const wrapper = mount(TemplateContentModal, {
      props: {
        open: true,
        title: 'Node Standard Build',
        yaml: 'id: node\n',
        loading: false,
        error: '',
        canApply: true,
        applying: false,
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('.settings-modal-backdrop').exists()).toBe(true)
    expect(wrapper.find('.settings-modal').exists()).toBe(true)
    expect(wrapper.find('.settings-modal-footer').exists()).toBe(true)
    expect(wrapper.find('[data-test="template-apply"]').classes()).toContain('settings-btn-primary')
  })

  it('renders template yaml and close button', async () => {
    const wrapper = mount(TemplateContentModal, {
      props: {
        open: true,
        title: 'Go Build',
        yaml: 'id: go-binary-build\nsteps: []\n',
      },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.text()).toContain('Go Build')
    expect(wrapper.text()).toContain('id: go-binary-build')
    await wrapper.find('[data-test="template-modal-close"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('renders digest and input summary', () => {
    const wrapper = mount(TemplateContentModal, {
      props: {
        open: true,
        title: 'Deploy',
        yaml: 'id: deploy\n',
        detail: {
          source: 'builtin',
          id: 'deploy',
          version: '1.0.0',
          digest: 'sha256:deploy',
          yaml: 'id: deploy\n',
          template: {
            id: 'deploy',
            name: 'Deploy',
            version: '1.0.0',
            inputs: {
              app_name: { label: '应用名', type: 'string', required: true, default: 'api', description: '服务名' },
              env: { label: '环境', type: 'select', options: ['dev', 'prod'] },
            },
            steps: [],
          },
        },
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.text()).toContain('sha256:deploy')
    expect(wrapper.text()).toContain('应用名')
    expect(wrapper.text()).toContain('string')
    expect(wrapper.text()).toContain('必填')
    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).toContain('dev, prod')
  })

  it('only shows apply action when canApply is true', async () => {
    const wrapper = mount(TemplateContentModal, {
      props: { open: true, title: 'Deploy', yaml: 'id: deploy\n', canApply: true },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="template-apply"]').trigger('click')

    expect(wrapper.emitted('apply')).toHaveLength(1)
  })
})
