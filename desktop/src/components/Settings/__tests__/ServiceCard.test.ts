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
    debug_credentials: [],
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
  it('offers every Agent language runtime provider language in service identity', () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [] },
    })

    const options = wrapper.findAll('[data-test="service-language"] option')

    expect(options.map(option => option.attributes('value') ?? '')).toEqual([
      '',
      'go',
      'node',
      'python',
      'java',
      'kotlin',
      'rust',
      'cpp',
    ])
    expect(options.at(-1)?.text()).toBe('C/C++')
  })

  it('renders the deployment editor as the mock-style two column service surface', () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [], projectPath: '/repo' },
    })

    expect(wrapper.find('[data-test="service-config-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="deployment-layout-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="deployment-main-column"] [data-test="runtime-target-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="deployment-side-column"] [data-test="service-debug-credentials"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="deployment-side-column"] [data-test="startup-readiness-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="deployment-side-column"] [data-test="log-source-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="remove-env-config"]').exists()).toBe(true)
  })

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

  it('persists C/C++ as the canonical cpp service language', async () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [] },
    })

    await wrapper.find('[data-test="service-language"]').setValue('cpp')

    const emitted = wrapper.emitted('update:service')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as ConfigDraftService
    expect(last.language).toBe('cpp')
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

  it('emits service debug credential edits', async () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [] },
    })

    await wrapper.find('[data-test="service-debug-credentials"] [data-test="debug-credential-add"]').trigger('click')
    const row = wrapper.find('[data-test="service-debug-credentials"] [data-test="debug-credential-row"]')
    await row.find('[data-test="debug-credential-name"]').setValue('api_key')
    await row.find('[data-test="debug-credential-value"]').setValue('svc-key')
    await row.find('[data-test="debug-credential-desc"]').setValue('服务 API key')

    const last = wrapper.emitted('update:service')!.at(-1)![0] as ConfigDraftService
    expect(last.debug_credentials).toEqual([{
      name: 'api_key',
      value: 'svc-key',
      desc: '服务 API key',
    }])
  })

  it('emits service AI guidance edits', async () => {
    const wrapper = mount(ServiceCard, {
      props: { service: service(), envName: 'dev', hosts: [] },
    })

    await wrapper.find('[data-test="service-ai-note"]').setValue('服务说明')
    await wrapper.find('[data-test="service-auth-hint"]').setValue('POST /api/login')

    const last = wrapper.emitted('update:service')!.at(-1)![0] as ConfigDraftService
    expect(last.ai_note).toBe('服务说明')
    expect(last.auth_hint).toBe('POST /api/login')
  })
})
