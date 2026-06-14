import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SchemaFieldInput from '@/components/Settings/SchemaFieldInput.vue'
import type { RuntimeDiagnostic, RuntimeSchemaField } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'

function field(overrides: Partial<RuntimeSchemaField>): RuntimeSchemaField {
  return {
    key: 'program',
    name: {
      key: 'runtime.go.program.name',
      default: 'Go entry package',
      values: { 'zh-CN': 'Go 入口包' },
    },
    desc: {
      key: 'runtime.go.program.desc',
      default: 'Main package used by go run.',
      values: { 'zh-CN': '用于 go run 的 main 包' },
    },
    type: 'string',
    required: true,
    ...overrides,
  }
}

describe('SchemaFieldInput', () => {
  it('prefers schema localized values and emits string updates', async () => {
    const wrapper = mount(SchemaFieldInput, {
      props: {
        field: field({ key: 'program' }),
        value: './cmd/api',
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.text()).toContain('Go 入口包')
    expect(wrapper.text()).toContain('用于 go run 的 main 包')

    await wrapper.get('[data-test="schema-field-program"]').setValue('./cmd/worker')

    expect(wrapper.emitted('update:value')?.at(-1)?.[0]).toBe('./cmd/worker')
  })

  it('emits typed values for boolean, number, and string array fields', async () => {
    const booleanWrapper = mount(SchemaFieldInput, {
      props: { field: field({ key: 'watch', type: 'boolean', required: false }), value: false },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await booleanWrapper.get('[data-test="schema-field-watch"]').setValue(true)
    expect(booleanWrapper.emitted('update:value')?.at(-1)?.[0]).toBe(true)

    const numberWrapper = mount(SchemaFieldInput, {
      props: { field: field({ key: 'port', type: 'number', required: false }), value: 3000 },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await numberWrapper.get('[data-test="schema-field-port"]').setValue('5173')
    expect(numberWrapper.emitted('update:value')?.at(-1)?.[0]).toBe(5173)

    const arrayWrapper = mount(SchemaFieldInput, {
      props: { field: field({ key: 'args', type: 'string_array', required: false }), value: ['--debug'] },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await arrayWrapper.get('[data-test="schema-field-args-0"]').setValue('--trace')
    expect(arrayWrapper.emitted('update:value')?.at(-1)?.[0]).toEqual(['--trace'])

    await arrayWrapper.get('[data-test="schema-field-args-add"]').trigger('click')
    expect(arrayWrapper.emitted('update:value')?.at(-1)?.[0]).toEqual(['--debug', ''])
  })

  it('renders diagnostics for the field', () => {
    const diagnostics: RuntimeDiagnostic[] = [
      { severity: 'error', field: 'program', code: 'required', message: 'program is required' },
      { severity: 'warning', field: 'other', code: 'other', message: 'other warning' },
    ]

    const wrapper = mount(SchemaFieldInput, {
      props: {
        field: field({ key: 'program' }),
        value: '',
        diagnostics,
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('program is required')
    expect(wrapper.text()).not.toContain('other warning')
  })
})
