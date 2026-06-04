/**
 * Ingress store。
 *
 * 职责：
 *   - 缓存入口声明、DNS provider、预演结果、落地状态和孤儿资源报告
 *   - 封装入口声明保存、应用、预演和孤儿资源删除动作
 *
 * 边界：
 *   - 不渲染 UI
 *   - 不拼装表单默认值，表单组件负责把用户输入转换为 Ingress
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  ingressApi,
  type AppliedState,
  type ApplyIngressOptions,
  type DNSProviderConfig,
  type InferDefaultsRequest,
  type Ingress,
  type OrphanReport,
  type PreviewResult,
} from '@/api/ingress'

function normalizeDNSProviders(providers: DNSProviderConfig[] | null | undefined): DNSProviderConfig[] {
  return Array.isArray(providers) ? providers : []
}

export const useIngressStore = defineStore('ingress', () => {
  const ingresses = ref<Ingress[]>([])
  const dnsProviders = ref<DNSProviderConfig[]>([])
  const previews = ref<Record<string, PreviewResult>>({})
  const appliedStates = ref<Record<string, AppliedState>>({})
  const orphanReports = ref<Record<string, OrphanReport>>({})
  const loading = ref(false)
  const error = ref('')

  async function loadAll() {
    loading.value = true
    error.value = ''
    try {
      const [nextIngresses, nextProviders] = await Promise.all([
        ingressApi.listIngresses(),
        ingressApi.listDNSProviders(),
      ])
      ingresses.value = nextIngresses
      dnsProviders.value = normalizeDNSProviders(nextProviders)
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function loadProject(projectId: string) {
    loading.value = true
    error.value = ''
    try {
      const [nextIngresses, nextProviders] = await Promise.all([
        ingressApi.listProjectIngresses(projectId),
        ingressApi.listDNSProviders(),
      ])
      ingresses.value = nextIngresses
      dnsProviders.value = normalizeDNSProviders(nextProviders)
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function loadDNSProviders() {
    loading.value = true
    error.value = ''
    try {
      dnsProviders.value = normalizeDNSProviders(await ingressApi.listDNSProviders())
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function saveIngress(payload: Ingress) {
    const saved = payload.id
      ? await ingressApi.updateIngress(payload.id, payload)
      : await ingressApi.createIngress(payload)
    upsertLocalIngress(saved)
    return saved
  }

  async function saveProjectIngress(projectId: string, payload: Ingress) {
    const saved = payload.id
      ? await ingressApi.updateProjectIngress(projectId, payload.id, payload)
      : await ingressApi.createProjectIngress(projectId, payload)
    upsertLocalIngress(saved)
    return saved
  }

  async function deleteProjectIngress(projectId: string, id: string) {
    await ingressApi.deleteProjectIngress(projectId, id)
    ingresses.value = ingresses.value.filter(item => item.id !== id)
    delete previews.value[id]
    delete appliedStates.value[id]
    delete orphanReports.value[id]
  }

  async function deleteIngress(id: string) {
    await ingressApi.deleteIngress(id)
    ingresses.value = ingresses.value.filter(item => item.id !== id)
    delete previews.value[id]
    delete appliedStates.value[id]
    delete orphanReports.value[id]
  }

  async function previewIngress(id: string) {
    const preview = await ingressApi.previewIngress(id)
    previews.value = { ...previews.value, [id]: preview }
    return preview
  }

  async function previewProjectIngress(projectId: string, id: string) {
    const preview = await ingressApi.previewProjectIngress(projectId, id)
    previews.value = { ...previews.value, [id]: preview }
    return preview
  }

  async function applyIngress(id: string, options: ApplyIngressOptions = {}) {
    const state = await ingressApi.applyIngress(id, options)
    appliedStates.value = { ...appliedStates.value, [id]: state }
    return state
  }

  async function applyProjectIngress(projectId: string, id: string, options: ApplyIngressOptions = {}) {
    const state = await ingressApi.applyProjectIngress(projectId, id, options)
    appliedStates.value = { ...appliedStates.value, [id]: state }
    return state
  }

  async function detectOrphans(id: string) {
    const report = await ingressApi.detectOrphans(id)
    orphanReports.value = { ...orphanReports.value, [id]: report }
    return report
  }

  async function detectProjectOrphans(projectId: string, id: string) {
    const report = await ingressApi.detectProjectOrphans(projectId, id)
    orphanReports.value = { ...orphanReports.value, [id]: report }
    return report
  }

  async function removeOrphans(id: string, report: OrphanReport) {
    await ingressApi.removeOrphans(id, report)
    orphanReports.value = { ...orphanReports.value, [id]: { configs: [], records: [] } }
  }

  async function removeProjectOrphans(projectId: string, id: string, report: OrphanReport) {
    await ingressApi.removeProjectOrphans(projectId, id, report)
    orphanReports.value = { ...orphanReports.value, [id]: { configs: [], records: [] } }
  }

  async function inferDefaults(projectId: string, payload: InferDefaultsRequest) {
    return ingressApi.inferDefaults(projectId, payload)
  }

  async function saveDNSProvider(payload: DNSProviderConfig) {
    const saved = await ingressApi.upsertDNSProvider(payload)
    const index = dnsProviders.value.findIndex(provider => provider.id === saved.id)
    if (index >= 0) dnsProviders.value[index] = saved
    else dnsProviders.value.push(saved)
    return saved
  }

  async function deleteDNSProvider(id: string) {
    await ingressApi.deleteDNSProvider(id)
    dnsProviders.value = dnsProviders.value.filter(provider => provider.id !== id)
  }

  function upsertLocalIngress(saved: Ingress) {
    const index = ingresses.value.findIndex(item => item.id === saved.id)
    if (index >= 0) ingresses.value[index] = saved
    else ingresses.value.push(saved)
  }

  return {
    ingresses,
    dnsProviders,
    previews,
    appliedStates,
    orphanReports,
    loading,
    error,
    loadAll,
    loadProject,
    loadDNSProviders,
    saveIngress,
    saveProjectIngress,
    deleteIngress,
    deleteProjectIngress,
    previewIngress,
    previewProjectIngress,
    applyIngress,
    applyProjectIngress,
    detectOrphans,
    detectProjectOrphans,
    removeOrphans,
    removeProjectOrphans,
    inferDefaults,
    saveDNSProvider,
    deleteDNSProvider,
  }
})
