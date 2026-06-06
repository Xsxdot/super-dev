/**
 * ProjectIngressTab 测试项目级入口配置管理。
 *
 * 职责：
 *   - 验证入口配置只围绕当前项目加载和保存
 *   - 验证 upstream 可由推断填 IP、端口由用户手动填写
 *   - 验证 Raw Template 随结构化 nginx 配置生成并作为保存内容
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不验证 nginx 语法或 DNS provider 凭据
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import ProjectIngressTab from '@/components/Overview/ProjectIngressTab.vue'
import { ingressApi, type Ingress } from '@/api/ingress'
import { certApi } from '@/api/cert'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'
import type { Host, Project } from '@/api/agent'

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@/api/ingress', async () => {
  const actual = await vi.importActual<typeof import('@/api/ingress')>('@/api/ingress')
  return {
    ...actual,
    ingressApi: {
      ...actual.ingressApi,
      listProjectIngresses: vi.fn(),
      createProjectIngress: vi.fn(),
      updateProjectIngress: vi.fn(),
      deleteProjectIngress: vi.fn(),
      previewProjectIngress: vi.fn(),
      applyProjectIngress: vi.fn(),
      detectProjectOrphans: vi.fn(),
      removeProjectOrphans: vi.fn(),
      inferDefaults: vi.fn(),
      listDNSProviders: vi.fn(),
    },
  }
})

vi.mock('@/api/cert', async () => {
  const actual = await vi.importActual<typeof import('@/api/cert')>('@/api/cert')
  return {
    ...actual,
    certApi: {
      listCertificates: vi.fn(),
      getACMEAccount: vi.fn(),
      matchCertificate: vi.fn(),
    },
  }
})

const mockedApi = ingressApi as unknown as Record<string, Mock>
const certMock = certApi as unknown as Record<string, Mock>

function makeProject(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [{ id: 'svc-api', project_id: 'p1', name: 'api', status: '', required: false, order: 0 }],
    environments: [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 0 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 1 },
    ],
    pipelines: [{
      id: 'deploy-prod',
      name: 'Deploy Prod',
      roles: { api_targets: { hosts: ['app-a', 'app-b'] } },
      pipeline: { deploy: [] },
    }],
  }
}

function makeHost(overrides: Partial<Host>): Host {
  return {
    id: 'host',
    name: 'host',
    private_ip: '127.0.0.1',
    tags: [],
    ...overrides,
  }
}

describe('ProjectIngressTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.listProjectIngresses.mockResolvedValue([])
    mockedApi.listDNSProviders.mockResolvedValue([
      { id: 'cloudflare-prod', name: 'Cloudflare Prod', type: 'cloudflare' },
    ])
    certMock.listCertificates.mockResolvedValue([])
    certMock.getACMEAccount.mockResolvedValue({ email: '', directory_url: '' })
    certMock.matchCertificate.mockResolvedValue(null)
    mockedApi.createProjectIngress.mockImplementation(async (_projectId: string, payload: Ingress) => ({
      ...payload,
      id: 'ing-new',
    }))
  })

  it('infers upstreams while keeping DNS records driven by proxy nodes', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      makeHost({ id: 'edge-a', name: 'edge-a', public_ip: '203.0.113.10', private_ip: '10.0.0.10' }),
      makeHost({ id: 'edge-b', name: 'edge-b', public_ip: '203.0.113.11', private_ip: '10.0.0.11' }),
      makeHost({ id: 'app-a', name: 'app-a', private_ip: '10.0.0.12' }),
      makeHost({ id: 'app-b', name: 'app-b', private_ip: '10.0.0.13' }),
    ]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    mockedApi.inferDefaults.mockResolvedValue({
      upstreams: [
        { host_id: 'app-a', ip: '10.0.0.12', port: '' },
        { host_id: 'app-b', ip: '10.0.0.13', port: '' },
      ],
      dns_records: [
        { type: 'A', name: 'api.example.com', value: '198.51.100.200', ttl: 300 },
        { type: 'A', name: 'api.example.com', value: '198.51.100.201', ttl: 300 },
      ],
      requires_port_input: true,
    })

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    expect(mockedApi.listProjectIngresses).toHaveBeenCalledWith('p1')

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-name"]').setValue('api')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await wrapper.find('[data-test="source-env"]').setValue('prod')
    await wrapper.find('[data-test="source-pipeline"]').setValue('deploy-prod')
    await wrapper.find('[data-test="source-role"]').setValue('api_targets')
    await wrapper.find('[data-test="proxy-host-selector"]').trigger('click')
    await wrapper.find('[data-test="proxy-host-edge-a"]').setValue(true)
    await wrapper.find('[data-test="proxy-host-edge-b"]').setValue(true)

    expect(wrapper.find('[data-test="dns-record-node-0"]').exists()).toBe(false)
    expect((wrapper.find('[data-test="dns-record-value-0"]').element as HTMLInputElement).value).toBe('203.0.113.10')
    expect((wrapper.find('[data-test="dns-record-value-1"]').element as HTMLInputElement).value).toBe('203.0.113.11')

    await wrapper.find('[data-test="ingress-infer"]').trigger('click')
    await flush()

    expect((wrapper.find('[data-test="upstream-ip-0"]').element as HTMLInputElement).value).toBe('10.0.0.12')
    expect((wrapper.find('[data-test="upstream-ip-1"]').element as HTMLInputElement).value).toBe('10.0.0.13')
    expect((wrapper.find('[data-test="dns-record-value-0"]').element as HTMLInputElement).value).toBe('203.0.113.10')
    expect((wrapper.find('[data-test="dns-record-value-1"]').element as HTMLInputElement).value).toBe('203.0.113.11')

    await wrapper.find('[data-test="upstream-port-0"]').setValue('8080')
    await wrapper.find('[data-test="upstream-port-1"]').setValue('8081')
    await flush()

    const rawTemplate = (wrapper.find('[data-test="nginx-raw-template"]').element as HTMLTextAreaElement).value
    expect(rawTemplate).toContain('server 10.0.0.12:8080;')
    expect(rawTemplate).toContain('server 10.0.0.13:8081;')

    await wrapper.find('[data-test="ingress-save"]').trigger('click')

    expect(mockedApi.createProjectIngress).toHaveBeenCalledWith('p1', expect.objectContaining({
      project_id: 'p1',
      name: 'api',
      domain: 'api.example.com',
      source_hint: { env_name: 'prod', pipeline_id: 'deploy-prod', role: 'api_targets' },
      proxy: { provider: 'nginx', host_ids: ['edge-a', 'edge-b'] },
      upstreams: [
        { host_id: 'app-a', ip: '10.0.0.12', port: 8080 },
        { host_id: 'app-b', ip: '10.0.0.13', port: 8081 },
      ],
      dns: {
        provider: 'manual',
        records: [
          { type: 'A', name: 'api.example.com', value: '203.0.113.10', ttl: 300 },
          { type: 'A', name: 'api.example.com', value: '203.0.113.11', ttl: 300 },
        ],
      },
      proxy_options: expect.objectContaining({
        raw_template: expect.stringContaining('server 10.0.0.12:8080;'),
      }),
    }))
  })

  it('starts with no reverse proxy nodes and generates DNS records from selected proxy nodes', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      makeHost({ id: 'edge-a', name: 'edge-a', public_ip: '203.0.113.10', private_ip: '10.0.0.10' }),
      makeHost({ id: 'edge-b', name: 'edge-b', public_ip: '203.0.113.11', private_ip: '10.0.0.11' }),
      makeHost({ id: 'app-a', name: 'app-a', private_ip: '10.0.0.12' }),
    ]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await flush()

    expect(wrapper.find('[data-test="dns-record-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dns-record-value-0"]').exists()).toBe(false)

    await wrapper.find('[data-test="proxy-host-selector"]').trigger('click')
    expect((wrapper.find('[data-test="proxy-host-edge-a"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.find('[data-test="proxy-host-edge-b"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.find('[data-test="proxy-host-app-a"]').exists()).toBe(false)

    await wrapper.find('[data-test="proxy-host-edge-a"]').setValue(true)
    await wrapper.find('[data-test="proxy-host-edge-b"]').setValue(true)
    await flush()

    expect((wrapper.find('[data-test="dns-record-value-0"]').element as HTMLInputElement).value).toBe('203.0.113.10')
    expect((wrapper.find('[data-test="dns-record-value-1"]').element as HTMLInputElement).value).toBe('203.0.113.11')

    await wrapper.find('[data-test="proxy-host-edge-b"]').setValue(false)
    await flush()

    expect(wrapper.find('[data-test="dns-record-value-1"]').exists()).toBe(false)
    expect((wrapper.find('[data-test="dns-record-value-0"]').element as HTMLInputElement).value).toBe('203.0.113.10')
  })

  it('renders the compact visual structure for the ingress form', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      makeHost({ id: 'edge-a', name: 'edge-a', public_ip: '203.0.113.10', private_ip: '10.0.0.10' }),
      makeHost({ id: 'app-a', name: 'app-a', private_ip: '10.0.0.12' }),
    ]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')

    expect(wrapper.find('[data-test="ingress-flow-strip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-settings-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-provider-segments"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-provider-nginx"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-provider-traefik"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="proxy-provider-custom"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tls-toggle-field"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="websocket-toggle-field"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-settings-grid"] [data-test="proxy-host-selector"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="proxy-settings-grid"] .template-jump').exists()).toBe(false)
    expect(wrapper.find('[data-test="dns-result-row"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dns-record-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dns-record-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="upstream-source-layout"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="upstream-card-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="raw-template-section"]').exists()).toBe(true)
  })

  it('closes host selectors when clicking outside the dropdowns', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      makeHost({ id: 'edge-a', name: 'edge-a', public_ip: '203.0.113.10', private_ip: '10.0.0.10' }),
      makeHost({ id: 'app-a', name: 'app-a', private_ip: '10.0.0.12' }),
    ]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="proxy-host-selector"]').trigger('click')
    expect(wrapper.find('[data-test="proxy-host-menu"]').exists()).toBe(true)

    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    await flush()

    expect(wrapper.find('[data-test="proxy-host-menu"]').exists()).toBe(false)

    await wrapper.find('[data-test="upstream-host-selector"]').trigger('click')
    expect(wrapper.find('[data-test="upstream-host-menu"]').exists()).toBe(true)

    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    await flush()

    expect(wrapper.find('[data-test="upstream-host-menu"]').exists()).toBe(false)
  })

  it('keeps certificate selection usable and enables HTTPS when a certificate is chosen', async () => {
    const remote = useRemoteStore()
    remote.hosts = [makeHost({ id: 'edge-a', name: 'edge-a' })]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    certMock.listCertificates.mockResolvedValue([
      { id: 'cert-api', domains: ['api.example.com'], issuer: 'acme', status: 'active', auto_renew: true },
    ])

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')

    const certSelect = wrapper.find('[data-test="ingress-cert-select"]')
    expect((certSelect.element as HTMLSelectElement).disabled).toBe(false)

    await certSelect.setValue('cert-api')
    await flush()

    expect((wrapper.find('[data-test="ingress-tls-enabled"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('propagates a newly entered upstream port to other empty inferred upstreams after blur', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      makeHost({ id: 'edge-a', name: 'edge-a', public_ip: '203.0.113.10', private_ip: '10.0.0.10' }),
      makeHost({ id: 'app-a', name: 'app-a', private_ip: '10.0.0.12' }),
      makeHost({ id: 'app-b', name: 'app-b', private_ip: '10.0.0.13' }),
    ]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    mockedApi.inferDefaults.mockResolvedValue({
      upstreams: [
        { host_id: 'app-a', ip: '10.0.0.12', port: '' },
        { host_id: 'app-b', ip: '10.0.0.13', port: '' },
      ],
      dns_records: [
        { type: 'A', name: 'api.example.com', value: '203.0.113.10', ttl: 300 },
      ],
      requires_port_input: true,
    })

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()

    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await wrapper.find('[data-test="source-env"]').setValue('prod')
    await wrapper.find('[data-test="source-pipeline"]').setValue('deploy-prod')
    await wrapper.find('[data-test="source-role"]').setValue('api_targets')
    await wrapper.find('[data-test="ingress-infer"]').trigger('click')
    await flush()

    await wrapper.find('[data-test="upstream-port-0"]').setValue('8000')
    await flush()

    expect((wrapper.find('[data-test="upstream-port-0"]').element as HTMLInputElement).value).toBe('8000')
    expect((wrapper.find('[data-test="upstream-port-1"]').element as HTMLInputElement).value).toBe('')

    await wrapper.find('[data-test="upstream-port-0"]').trigger('blur')
    await flush()

    expect((wrapper.find('[data-test="upstream-port-0"]').element as HTMLInputElement).value).toBe('8000')
    expect((wrapper.find('[data-test="upstream-port-1"]').element as HTMLInputElement).value).toBe('8000')
  })

  it('auto-selects matching active certificate when HTTPS is enabled', async () => {
    const remote = useRemoteStore()
    remote.hosts = [makeHost({ id: 'edge-a', name: 'edge-a' })]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    certMock.listCertificates.mockResolvedValue([
      { id: 'cert-wild', domains: ['*.example.com'], issuer: 'acme', status: 'active', auto_renew: true },
    ])
    certMock.getACMEAccount.mockResolvedValue({ email: 'ops@example.com', directory_url: '' })
    certMock.matchCertificate.mockResolvedValue({ id: 'cert-wild', domains: ['*.example.com'], issuer: 'acme', status: 'active', auto_renew: true })

    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()
    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await wrapper.find('[data-test="ingress-tls-enabled"]').setValue(true)
    await flush()

    expect((wrapper.find('[data-test="ingress-cert-select"]').element as HTMLSelectElement).value).toBe('cert-wild')
  })

  it('shows SSL settings link when no matching certificate exists', async () => {
    certMock.listCertificates.mockResolvedValue([])
    certMock.getACMEAccount.mockResolvedValue({ email: '', directory_url: '' })
    certMock.matchCertificate.mockResolvedValue(null)
    const wrapper = mount(ProjectIngressTab, {
      props: { project: makeProject() },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flush()
    await wrapper.find('[data-test="project-ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await wrapper.find('[data-test="ingress-tls-enabled"]').setValue(true)
    await flush()

    expect(wrapper.find('[data-test="ingress-cert-missing"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="ingress-cert-request"]').exists()).toBe(true)
  })
})

function flush() {
  return new Promise(resolve => setTimeout(resolve))
}
