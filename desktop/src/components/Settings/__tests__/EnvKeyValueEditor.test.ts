import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EnvKeyValueEditor from '@/components/Settings/EnvKeyValueEditor.vue'

describe('EnvKeyValueEditor', () => {
  it('展示已有变量为行', () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: { A: '1', B: '2' } } })
    expect(wrapper.findAll('[data-test="env-row"]')).toHaveLength(2)
  })

  it('点击添加变量 emit 含新空行的对象', async () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: {} } })
    await wrapper.find('[data-test="env-add"]').trigger('click')
    const rows = wrapper.findAll('[data-test="env-row"]')
    expect(rows).toHaveLength(1)
  })

  it('删除行 emit 移除该 key 的对象', async () => {
    const wrapper = mount(EnvKeyValueEditor, { props: { modelValue: { A: '1' } } })
    await wrapper.find('[data-test="env-del"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    expect(emitted![emitted!.length - 1][0]).toEqual({})
  })
})
