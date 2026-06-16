import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ServiceCard from '@/components/Settings/ServiceCard.vue'
import type { ConfigDraftService } from '@/lib/configDraft'

function service(): ConfigDraftService {
  return {
    id: 'svc-api',
    name: 'api',
    required: true,
    order: 1,
    language: 'go',
    deployments: [{
      id: 'dep-api-dev',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'command', command: 'go run .' },
      logs: { type: 'process' },
      status: '',
    }],
  }
}

describe('ServiceCard', () => {
  it('emits service language changes from the service identity area', async () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [] },
    })

    await wrapper.find('[data-test="service-language"]').setValue('python')

    const emitted = wrapper.emitted('update:service')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as ConfigDraftService
    expect(last.language).toBe('python')
  })

  it('enables a new local managed deployment with language runtime defaults', async () => {
    const base = service()
    base.deployments = []
    const wrapper = mount(ServiceCard, {
      props: { service: base, envName: 'dev', hosts: [], projectPath: '/repo' },
    })

    await wrapper.find('[data-test="enable-dep"]').trigger('click')

    const emitted = wrapper.emitted('update:service')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as ConfigDraftService
    expect(last.deployments[0]).toMatchObject({
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: {
        type: 'language',
        cwd: '/repo/api',
        env: {},
        config: { program: '.' },
      },
      logs: { type: 'process' },
    })
  })

  it('does not fall back to command runtime when language is not set', async () => {
    const base = service()
    base.language = undefined
    base.deployments = []
    const wrapper = mount(ServiceCard, {
      props: { service: base, envName: 'dev', hosts: [], projectPath: '/repo' },
    })

    await wrapper.find('[data-test="enable-dep"]').trigger('click')

    const last = wrapper.emitted('update:service')!.at(-1)![0] as ConfigDraftService
    expect(last.deployments[0].runtime).toEqual({
      type: 'language',
      cwd: '/repo/api',
      env: {},
      config: {},
    })
  })
})
