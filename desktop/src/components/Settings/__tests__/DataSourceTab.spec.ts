/**
 * DataSourceTab 设置页测试。
 *
 * 职责：验证数据源能力概览、Redis 隔离图、租约回收和对账反馈。
 * 边界：不访问真实 agent；API 与 store 都在测试中隔离。
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DataSourceTab from '@/components/Settings/DataSourceTab.vue'
import { dataSourceApi, type DataSource, type Lease } from '@/api/datasources'
import { useDataSourceStore } from '@/stores/datasources'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/datasources', async () => {
  const actual = await vi.importActual<typeof import('@/api/datasources')>('@/api/datasources')
  return {
    ...actual,
    dataSourceApi: {
      ...actual.dataSourceApi,
      list: vi.fn().mockResolvedValue([]),
      leases: vi.fn().mockResolvedValue([]),
      reclaim: vi.fn().mockResolvedValue(undefined),
      reconcile: vi.fn().mockResolvedValue({ expired_reclaimed: 0, orphans_reclaimed: [] }),
    },
  }
})

const pgSource: DataSource = {
  id: 'pg-1', kind: 'postgres', name: 'local-pg', host: '127.0.0.1', port: 5432,
  probe: { ok: true, capabilities: { createdb: true, createrole: true, pg_signal_backend: true }, facts: {} },
}
const redisSource: DataSource = {
  id: 'redis-1', kind: 'redis', name: 'local-redis', host: '127.0.0.1', port: 6379,
  probe: { ok: true, capabilities: {}, facts: { databases: '16', occupied_dbs: '1,3' } },
}
const lease: Lease = {
  id: 'lease-1', project_id: 'project-1', purpose: 'integration test',
  created_at: '2026-08-21T10:00:00Z', expires_at: '2026-08-21T10:30:00Z', renew_count: 0,
  resources: [{ kind: 'redis', name: 'db2', meta: { db_index: '2' } }],
}

describe('DataSourceTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(dataSourceApi.list).mockResolvedValue([])
    vi.mocked(dataSourceApi.leases).mockResolvedValue([])
  })

  function mountTab(sources: DataSource[] = [pgSource, redisSource], leases: Lease[] = [lease]) {
    const store = useDataSourceStore()
    store.sources = sources
    store.activeLeases = leases
    vi.spyOn(store, 'load').mockResolvedValue(undefined)
    return { wrapper: mount(DataSourceTab, { global: { plugins: [installTestI18n('zh-CN')] } }), store }
  }

  it('renders PG capability badges', async () => {
    const { wrapper } = mountTab()
    await flushPromises()
    expect(wrapper.text()).toContain('CREATEDB')
    expect(wrapper.text()).toContain('CREATEROLE')
    expect(wrapper.text()).toContain('pg_signal_backend')
  })

  it('renders Redis 16-cell occupancy grid and reserves db0', async () => {
    const { wrapper } = mountTab()
    await flushPromises()
    expect(wrapper.findAll('[data-test^="redis-db-cell-"]')).toHaveLength(16)
    expect(wrapper.find('[data-test="redis-db-cell-0"]').classes()).toContain('reserved')
    expect(wrapper.find('[data-test="redis-db-cell-1"]').classes()).toContain('occupied')
    expect(wrapper.find('[data-test="redis-db-cell-2"]').classes()).toContain('lease')
  })

  it('shows lease expiry and reclaims on click', async () => {
    const { wrapper, store } = mountTab()
    const reclaim = vi.spyOn(store, 'reclaim').mockResolvedValue(undefined)
    await flushPromises()
    expect(wrapper.find('[data-test="lease-row-lease-1"]').text()).toContain('integration test')
    expect(wrapper.find('[data-test="lease-row-lease-1"]').text()).toContain('2026')
    await wrapper.find('[data-test="reclaim-lease-lease-1"]').trigger('click')
    expect(reclaim).toHaveBeenCalledWith('lease-1')
  })

  it('reconciles and renders reclaim result', async () => {
    const { wrapper, store } = mountTab()
    const reconcile = vi.spyOn(store, 'reconcile').mockResolvedValue({
      expired_reclaimed: 1,
      orphans_reclaimed: [{ kind: 'postgres', name: 'sdev_eph_ghost', reason: 'orphan' }],
      errors: [],
    })
    await flushPromises()
    await wrapper.find('[data-test="datasource-reconcile"]').trigger('click')
    expect(reconcile).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('sdev_eph_ghost')
  })

  it('never renders a password field or password value', async () => {
    const { wrapper } = mountTab()
    await flushPromises()
    expect(wrapper.text().toLowerCase()).not.toContain('password')
    expect(wrapper.html()).not.toContain('s3cret')
  })
})
