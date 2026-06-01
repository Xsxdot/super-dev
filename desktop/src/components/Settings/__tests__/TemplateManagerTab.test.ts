/**
 * TemplateManagerTab 测试流水线模板管理页签。
 *
 * 职责：
 *   - 验证模板列表展示
 *   - 验证导入按钮触发回调
 *
 * 边界：
 *   - 不打开真实文件选择器
 *   - 不调用 agent 导入接口
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import TemplateManagerTab from '@/components/Settings/TemplateManagerTab.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('TemplateManagerTab', () => {
  it('展示模板列表', () => {
    const wrapper = mount(TemplateManagerTab, {
      props: {
        templates: [{ source: 'builtin', id: 'go-binary-build', name: 'Go Build', version: '1.0.0', digest: 'sha256:x' }],
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.text()).toContain('Go Build')
    expect(wrapper.text()).toContain('builtin')
  })

  it('点击导入触发 import', async () => {
    const onImport = vi.fn()
    const wrapper = mount(TemplateManagerTab, {
      props: { templates: [], onImport },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="template-import"]').trigger('click')

    expect(onImport).toHaveBeenCalled()
  })

  it('点击查看触发 onView', async () => {
    const onView = vi.fn()
    const template = { source: 'builtin', id: 'go-binary-build', name: 'Go Build', version: '1.0.0', digest: 'sha256:x' } as const
    const wrapper = mount(TemplateManagerTab, {
      props: { templates: [template], onView },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="template-view-go-binary-build"]').trigger('click')

    expect(onView).toHaveBeenCalledWith(template)
  })

  it('英文 locale 下渲染模板管理文案', () => {
    const wrapper = mount(TemplateManagerTab, {
      props: { templates: [] },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Templates')
    expect(wrapper.text()).toContain('Manage reusable deployment pipeline templates')
    expect(wrapper.text()).toContain('Import Template')
    expect(wrapper.text()).toContain('No templates yet')
  })
})
