/**
 * TemplateContentModal 测试模板 YAML 只读查看。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TemplateContentModal from '@/components/Settings/TemplateContentModal.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('TemplateContentModal', () => {
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
})
