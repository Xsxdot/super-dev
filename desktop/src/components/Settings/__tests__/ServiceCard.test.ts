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
})
