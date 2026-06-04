/**
 * DNSProviderTab 测试设置页全局 DNS Provider 管理。
 *
 * 职责：
 *   - 验证内置 Manual DNS 只读展示
 *   - 验证保存 Cloudflare provider 走 ingress store
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不管理项目级 Ingress 声明
 */
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import DNSProviderTab from '@/components/Settings/DNSProviderTab.vue'
import { ingressApi } from '@/api/ingress'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/ingress', async () => {
  const actual = await vi.importActual<typeof import('@/api/ingress')>('@/api/ingress')
  return {
    ...actual,
    ingressApi: {
      ...actual.ingressApi,
      listDNSProviders: vi.fn().mockResolvedValue([]),
      upsertDNSProvider: vi.fn(),
      deleteDNSProvider: vi.fn(),
    },
  }
})

const mockedApi = ingressApi as unknown as Record<string, Mock>

describe('DNSProviderTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.listDNSProviders.mockResolvedValue([])
  })

  it('renders built-in manual DNS provider as read-only', async () => {
    const wrapper = mount(DNSProviderTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flush()

    expect(wrapper.text()).toContain('手动 DNS')
    expect(wrapper.find('[data-test="dns-provider-manual"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dns-provider-delete-manual"]').exists()).toBe(false)
  })

  it('saves a cloudflare provider', async () => {
    mockedApi.upsertDNSProvider.mockResolvedValue({
      id: 'cloudflare-prod',
      name: 'Cloudflare Prod',
      type: 'cloudflare',
      zone_id: 'zone-1',
    })
    const wrapper = mount(DNSProviderTab, { global: { plugins: [installTestI18n('en-US')] } })
    await flush()

    await wrapper.find('[data-test="dns-provider-add"]').trigger('click')
    await wrapper.find('[data-test="dns-provider-id"]').setValue('cloudflare-prod')
    await wrapper.find('[data-test="dns-provider-name"]').setValue('Cloudflare Prod')
    await wrapper.find('[data-test="dns-provider-type"]').setValue('cloudflare')
    await wrapper.find('[data-test="dns-provider-zone"]').setValue('zone-1')
    await wrapper.find('[data-test="dns-provider-token"]').setValue('secret-token')
    await wrapper.find('[data-test="dns-provider-save"]').trigger('click')

    expect(mockedApi.upsertDNSProvider).toHaveBeenCalledWith(expect.objectContaining({
      id: 'cloudflare-prod',
      name: 'Cloudflare Prod',
      type: 'cloudflare',
      zone_id: 'zone-1',
      secrets: { api_token: 'secret-token' },
    }))
  })

  it('does not render or submit zone id for aliyun providers', async () => {
    mockedApi.upsertDNSProvider.mockResolvedValue({
      id: 'aliyun-prod',
      name: 'Aliyun Prod',
      type: 'aliyun',
    })
    const wrapper = mount(DNSProviderTab, { global: { plugins: [installTestI18n('en-US')] } })
    await flush()

    await wrapper.find('[data-test="dns-provider-add"]').trigger('click')
    await wrapper.find('[data-test="dns-provider-id"]').setValue('aliyun-prod')
    await wrapper.find('[data-test="dns-provider-name"]').setValue('Aliyun Prod')
    await wrapper.find('[data-test="dns-provider-type"]').setValue('aliyun')

    expect(wrapper.find('[data-test="dns-provider-zone"]').exists()).toBe(false)

    await wrapper.find('[data-test="dns-provider-access-key-id"]').setValue('ak')
    await wrapper.find('[data-test="dns-provider-access-key-secret"]').setValue('sk')
    await wrapper.find('[data-test="dns-provider-save"]').trigger('click')

    expect(mockedApi.upsertDNSProvider).toHaveBeenCalledWith({
      id: 'aliyun-prod',
      name: 'Aliyun Prod',
      type: 'aliyun',
      secrets: {
        access_key_id: 'ak',
        access_key_secret: 'sk',
      },
    })
  })

  it('allows cloudflare providers to omit zone id', async () => {
    mockedApi.upsertDNSProvider.mockResolvedValue({
      id: 'cloudflare-prod',
      name: 'Cloudflare Prod',
      type: 'cloudflare',
    })
    const wrapper = mount(DNSProviderTab, { global: { plugins: [installTestI18n('en-US')] } })
    await flush()

    await wrapper.find('[data-test="dns-provider-add"]').trigger('click')
    await wrapper.find('[data-test="dns-provider-id"]').setValue('cloudflare-prod')
    await wrapper.find('[data-test="dns-provider-name"]').setValue('Cloudflare Prod')
    await wrapper.find('[data-test="dns-provider-type"]').setValue('cloudflare')
    await wrapper.find('[data-test="dns-provider-token"]').setValue('secret-token')
    await wrapper.find('[data-test="dns-provider-save"]').trigger('click')

    expect(mockedApi.upsertDNSProvider).toHaveBeenCalledWith({
      id: 'cloudflare-prod',
      name: 'Cloudflare Prod',
      type: 'cloudflare',
      secrets: { api_token: 'secret-token' },
    })
  })

  it('opens a saved provider for editing without clearing existing secrets', async () => {
    mockedApi.listDNSProviders.mockResolvedValue([{
      id: 'cloudflare-prod',
      name: 'Cloudflare Prod',
      type: 'cloudflare',
      zone_id: 'zone-1',
    }])
    mockedApi.upsertDNSProvider.mockResolvedValue({
      id: 'cloudflare-prod',
      name: 'Cloudflare Seven',
      type: 'cloudflare',
      zone_id: 'cn-hangzhou',
    })
    const wrapper = mount(DNSProviderTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flush()

    await wrapper.find('[data-test="dns-provider-edit-cloudflare-prod"]').trigger('click')
    expect((wrapper.find('[data-test="dns-provider-id"]').element as HTMLInputElement).value).toBe('cloudflare-prod')

    await wrapper.find('[data-test="dns-provider-name"]').setValue('Cloudflare Seven')
    await wrapper.find('[data-test="dns-provider-zone"]').setValue('cn-hangzhou')
    await wrapper.find('[data-test="dns-provider-save"]').trigger('click')

    expect(mockedApi.upsertDNSProvider).toHaveBeenCalledWith(expect.objectContaining({
      id: 'cloudflare-prod',
      name: 'Cloudflare Seven',
      type: 'cloudflare',
      zone_id: 'cn-hangzhou',
    }))
    expect(mockedApi.upsertDNSProvider.mock.calls.at(-1)?.[0]).not.toHaveProperty('secrets')
  })
})

function flush() {
  return new Promise(resolve => setTimeout(resolve))
}
