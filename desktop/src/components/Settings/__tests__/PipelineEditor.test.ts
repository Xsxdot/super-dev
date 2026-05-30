import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PipelineEditor from '@/components/Settings/PipelineEditor.vue'
import type { Pipeline } from '@/api/agent'

describe('PipelineEditor', () => {
  it('无 pipeline 时展示「配置流水线」入口', () => {
    const wrapper = mount(PipelineEditor, { props: { modelValue: undefined } })
    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(true)
  })

  it('点击启用 emit 含空 steps 的 pipeline', async () => {
    const wrapper = mount(PipelineEditor, { props: { modelValue: undefined } })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted![0][0]).toEqual({ steps: [] })
  })

  it('有 steps 时渲染步骤卡片，添加步骤增加一行', async () => {
    const pipeline: Pipeline = { steps: [{ id: 's1', name: 'build', scope: 'local', action: 'run', command: 'make' }] }
    const wrapper = mount(PipelineEditor, { props: { modelValue: pipeline } })
    expect(wrapper.findAll('[data-test="step-card"]')).toHaveLength(1)
    await wrapper.find('[data-test="step-add"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Pipeline
    expect(last.steps).toHaveLength(2)
  })

  it('选择同步文件时 scope 自动锁定为每台目标机', async () => {
    const pipeline: Pipeline = { steps: [{ id: 's1', name: 'step1', scope: 'local', action: 'run', command: 'make' }] }
    const wrapper = mount(PipelineEditor, { props: { modelValue: pipeline } })
    // 找到「同步文件」radio 并点击
    const syncRadio = wrapper.findAll('input[type="radio"]').find(r => {
      const label = r.element.closest('label')
      return label?.textContent?.includes('同步文件')
    })
    await syncRadio!.trigger('change')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Pipeline
    expect(last.steps[0].action).toBe('sync')
    expect(last.steps[0].scope).toBe('fan-out')
  })

  it('scope=local 选项在 action=sync 时被禁用', () => {
    const pipeline: Pipeline = { steps: [{ id: 's1', name: 'step1', scope: 'fan-out', action: 'sync', command: '' }] }
    const wrapper = mount(PipelineEditor, { props: { modelValue: pipeline } })
    const localRadio = wrapper.findAll('input[type="radio"]').find(r => {
      const label = r.element.closest('label')
      return label?.textContent?.includes('本机一次')
    })
    expect((localRadio!.element as HTMLInputElement).disabled).toBe(true)
  })
})
