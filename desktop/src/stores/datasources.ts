// datasources.ts 管理设置页使用的数据源与临时租约状态。
//
// 职责：
//   - 读取数据源/租约列表并提供 CRUD、探测、回收、对账与试跑 action
//   - 把失败写入 error 并继续抛出，让组件能展示且调用方能决定后续动作
//
// 边界：
//   - 不保存密码，不在 store 中拼接连接字符串
//   - 不决定审批和配额，全部交给 agent
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  dataSourceApi,
  type DataSource,
  type DataSourceInput,
  type DataSourceProbeResult,
  type DryRunResult,
  type Lease,
  type ReconcileReport,
} from '@/api/datasources'

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

/** useDataSourceStore 提供数据源设置页的可见状态与异步 action。 */
export const useDataSourceStore = defineStore('datasources', () => {
  const sources = ref<DataSource[]>([])
  const activeLeases = ref<Lease[]>([])
  const loading = ref(false)
  const error = ref('')
  const lastReconcile = ref<ReconcileReport | null>(null)

  async function load() {
    loading.value = true
    error.value = ''
    try {
      const [nextSources, nextLeases] = await Promise.all([dataSourceApi.list(), dataSourceApi.leases()])
      sources.value = nextSources
      activeLeases.value = nextLeases
    } catch (err) {
      error.value = messageOf(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function create(payload: DataSourceInput) {
    try {
      const saved = await dataSourceApi.create(payload)
      sources.value = [...sources.value.filter(source => source.id !== saved.id), saved]
      error.value = ''
      return saved
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function update(id: string, payload: DataSourceInput) {
    try {
      const saved = await dataSourceApi.update(id, payload)
      sources.value = sources.value.map(source => source.id === id ? saved : source)
      error.value = ''
      return saved
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function remove(id: string, force = false) {
    try {
      await dataSourceApi.remove(id, force)
      sources.value = sources.value.filter(source => source.id !== id)
      error.value = ''
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function probe(id: string): Promise<DataSourceProbeResult> {
    try {
      const result = await dataSourceApi.probe(id)
      sources.value = sources.value.map(source => source.id === id ? { ...source, probe: result } : source)
      error.value = ''
      return result
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function leases() {
    try {
      activeLeases.value = await dataSourceApi.leases()
      error.value = ''
      return activeLeases.value
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function reclaim(leaseId: string) {
    try {
      await dataSourceApi.reclaim(leaseId)
      activeLeases.value = activeLeases.value.filter(lease => lease.id !== leaseId)
      error.value = ''
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function reconcile() {
    try {
      const report = await dataSourceApi.reconcile()
      lastReconcile.value = report
      await leases()
      error.value = ''
      return report
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  async function dryRun(projectId: string): Promise<DryRunResult> {
    try {
      const result = await dataSourceApi.dryRun(projectId)
      error.value = ''
      return result
    } catch (err) {
      error.value = messageOf(err)
      throw err
    }
  }

  return {
    sources,
    activeLeases,
    loading,
    error,
    lastReconcile,
    load,
    create,
    update,
    remove,
    probe,
    leases,
    reclaim,
    reconcile,
    dryRun,
  }
})
