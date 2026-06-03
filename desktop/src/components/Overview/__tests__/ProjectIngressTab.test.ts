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
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'
import type { Host, Project } from '@/api/agent'

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

const mockedApi = ingressApi as unknown as Record<string, Mock>

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
    ssh_host: '127.0.0.1',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    local_tunnel_port: 0,
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
    mockedApi.createProjectIngress.mockImplementation(async (_projectId: string, payload: Ingress) => ({
      ...payload,
      id: 'ing-new',
    }))
  })

  it('infers upstream IPs and DNS records, then saves project-scoped ingress from Raw Template', async () => {
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
        { type: 'A', name: 'api.example.com', value: '203.0.113.10', ttl: 300 },
        { type: 'A', name: 'api.example.com', value: '203.0.113.11', ttl: 300 },
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
    await wrapper.find('[data-test="proxy-host-edge-a"]').setValue(true)
    await wrapper.find('[data-test="proxy-host-edge-b"]').setValue(true)
    await wrapper.find('[data-test="ingress-infer"]').trigger('click')
    await flush()

    expect((wrapper.find('[data-test="upstream-ip-0"]').element as HTMLInputElement).value).toBe('10.0.0.12')
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
})

function flush() {
  return new Promise(resolve => setTimeout(resolve))
}
