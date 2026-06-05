/**
 * SSL 证书 store。
 *
 * 职责：
 *   - 缓存全局托管证书和 ACME 账号配置
 *   - 封装证书创建、签发轮询、续期、部署、删除和域名匹配动作
 *
 * 边界：
 *   - 不渲染 UI
 *   - 不拼装证书表单默认值，表单组件负责把用户输入转换为 API payload
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  certApi,
  type ACMEAccount,
  type CertificateCreatePayload,
  type CertificateDeployPayload,
  type ManagedCertificate,
} from '@/api/cert'

export interface CertificatePollOptions {
  intervalMs?: number
  maxAttempts?: number
}

const DEFAULT_POLL_INTERVAL_MS = 2000
const DEFAULT_POLL_ATTEMPTS = 45

function wait(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

function normalizeCertificates(certs: ManagedCertificate[] | null | undefined): ManagedCertificate[] {
  return Array.isArray(certs) ? certs : []
}

export const useCertStore = defineStore('cert', () => {
  const certificates = ref<ManagedCertificate[]>([])
  const acmeAccount = ref<ACMEAccount>({ email: '', directory_url: '' })
  const loading = ref(false)
  const error = ref('')

  async function loadAll() {
    loading.value = true
    error.value = ''
    try {
      const [nextCertificates, nextAccount] = await Promise.all([
        certApi.listCertificates(),
        certApi.getACMEAccount(),
      ])
      certificates.value = normalizeCertificates(nextCertificates)
      acmeAccount.value = nextAccount
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function createCertificate(payload: CertificateCreatePayload) {
    const saved = await certApi.createCertificate(payload)
    upsertLocal(saved)
    return saved
  }

  async function issueCertificate(id: string, options: CertificatePollOptions = {}) {
    const issued = await certApi.issueCertificate(id)
    upsertLocal(issued)
    if (issued.status !== 'pending') {
      return issued
    }
    return pollCertificate(id, options)
  }

  async function pollCertificate(id: string, options: CertificatePollOptions = {}) {
    const intervalMs = options.intervalMs ?? DEFAULT_POLL_INTERVAL_MS
    const maxAttempts = options.maxAttempts ?? DEFAULT_POLL_ATTEMPTS
    let latest: ManagedCertificate | undefined

    for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
      await wait(intervalMs)
      latest = await certApi.getCertificate(id)
      upsertLocal(latest)
      // pending 表示后台任务仍在运行，其他状态都应该立即反馈给用户。
      if (latest.status !== 'pending') {
        return latest
      }
    }

    const fallback = latest ?? certificates.value.find(cert => cert.id === id)
    if (fallback) {
      return fallback
    }
    const fetched = await certApi.getCertificate(id)
    upsertLocal(fetched)
    return fetched
  }

  async function renewCertificate(id: string) {
    const renewed = await certApi.renewCertificate(id)
    upsertLocal(renewed)
    return renewed
  }

  async function deployCertificate(id: string, payload: CertificateDeployPayload = { host_ids: [] }) {
    const deployed = await certApi.deployCertificate(id, payload)
    upsertLocal(deployed)
    return deployed
  }

  async function deleteCertificate(id: string) {
    await certApi.deleteCertificate(id)
    certificates.value = certificates.value.filter(cert => cert.id !== id)
  }

  async function saveACMEAccount(payload: ACMEAccount) {
    const saved = await certApi.saveACMEAccount(payload)
    acmeAccount.value = saved
    return saved
  }

  async function matchCertificate(domain: string) {
    return certApi.matchCertificate(domain)
  }

  function upsertLocal(saved: ManagedCertificate) {
    certificates.value = normalizeCertificates(certificates.value)
    const index = certificates.value.findIndex(cert => cert.id === saved.id)
    if (index >= 0) certificates.value[index] = saved
    else certificates.value.push(saved)
  }

  return {
    certificates,
    acmeAccount,
    loading,
    error,
    loadAll,
    createCertificate,
    issueCertificate,
    pollCertificate,
    renewCertificate,
    deployCertificate,
    deleteCertificate,
    saveACMEAccount,
    matchCertificate,
    upsertLocal,
  }
})
