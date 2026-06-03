<!--
CertificateTab：设置页全局 SSL 证书管理。

职责：
  - 管理全局 ACME 账号
  - 创建、申请、续期、部署和删除托管证书
  - 展示证书状态、过期时间和部署目标

边界：
  - 不编辑项目级 Ingress 声明
  - 不直接验证 DNS provider 凭据
-->
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useCertStore } from '@/stores/cert'
import { useIngressStore } from '@/stores/ingress'
import { useRemoteStore } from '@/stores/remote'
import type { CertificateCreatePayload, CertificateIssuer, ManagedCertificate } from '@/api/cert'

const ISSUE_POLL_INTERVAL_MS = 1000
const ISSUE_POLL_ATTEMPTS = 90
const LETS_ENCRYPT_STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory'

const certStore = useCertStore()
const ingressStore = useIngressStore()
const remoteStore = useRemoteStore()
const { t } = useI18n()

const formOpen = ref(false)
const deployOpen = ref(false)
const selectedCert = ref<ManagedCertificate | null>(null)
const saving = ref(false)
const error = ref('')
const accountDraft = reactive({ email: '', directory_url: '' })
const certDraft = reactive({
  domains: [''],
  issuer: 'acme' as CertificateIssuer,
  dns_provider: '',
  auto_renew: true,
  cert_pem: '',
  key_pem: '',
})
const deployHostIDs = ref<string[]>([])

const certificates = computed(() => certStore.certificates)
const dnsProviders = computed(() => ingressStore.dnsProviders)
const hosts = computed(() => remoteStore.hosts)

onMounted(async () => {
  try {
    await Promise.all([certStore.loadAll(), ingressStore.loadDNSProviders(), remoteStore.loadHosts()])
    accountDraft.email = certStore.acmeAccount.email
    accountDraft.directory_url = certStore.acmeAccount.directory_url ?? ''
    certDraft.dns_provider = defaultDNSProviderID()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
})

function defaultDNSProviderID() {
  return dnsProviders.value[0]?.id ?? ''
}

function openCreate() {
  Object.assign(certDraft, {
    domains: [''],
    issuer: 'acme',
    dns_provider: defaultDNSProviderID(),
    auto_renew: true,
    cert_pem: '',
    key_pem: '',
  })
  error.value = ''
  formOpen.value = true
}

function addDomain() {
  certDraft.domains.push('')
}

function removeDomain(index: number) {
  if (certDraft.domains.length === 1) {
    certDraft.domains[0] = ''
    return
  }
  certDraft.domains.splice(index, 1)
}

function cleanDomains(): string[] {
  return certDraft.domains.map(domain => domain.trim()).filter(Boolean)
}

async function saveAccount() {
  saving.value = true
  error.value = ''
  try {
    await certStore.saveACMEAccount({
      email: accountDraft.email.trim(),
      directory_url: accountDraft.directory_url.trim(),
    })
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function submitCertificate() {
  const domains = cleanDomains()
  if (domains.length === 0) {
    error.value = t('settings.certificates.domainRequired')
    return
  }
  saving.value = true
  error.value = ''
  try {
    const payload: CertificateCreatePayload = {
      domains,
      issuer: certDraft.issuer,
      dns_provider: certDraft.issuer === 'acme' ? certDraft.dns_provider : undefined,
      auto_renew: certDraft.issuer === 'acme' ? certDraft.auto_renew : false,
      material: certDraft.issuer === 'manual'
        ? { domain: domains[0], cert_pem: certDraft.cert_pem, key_pem: certDraft.key_pem, provider: 'manual' }
        : undefined,
    }
    const created = await certStore.createCertificate(payload)
    if (created.id && payload.issuer === 'acme') {
      await certStore.issueCertificate(created.id, {
        intervalMs: ISSUE_POLL_INTERVAL_MS,
        maxAttempts: ISSUE_POLL_ATTEMPTS,
      })
    }
    formOpen.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function issueCertificate(cert: ManagedCertificate) {
  saving.value = true
  error.value = ''
  try {
    await certStore.issueCertificate(cert.id, {
      intervalMs: ISSUE_POLL_INTERVAL_MS,
      maxAttempts: ISSUE_POLL_ATTEMPTS,
    })
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function renewCertificate(cert: ManagedCertificate) {
  saving.value = true
  error.value = ''
  try {
    await certStore.renewCertificate(cert.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function deleteCertificate(cert: ManagedCertificate) {
  error.value = ''
  try {
    await certStore.deleteCertificate(cert.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

function openDeploy(cert: ManagedCertificate) {
  selectedCert.value = cert
  deployHostIDs.value = []
  error.value = ''
  deployOpen.value = true
}

async function submitDeploy() {
  if (!selectedCert.value) return
  saving.value = true
  error.value = ''
  try {
    await certStore.deployCertificate(selectedCert.value.id, deployHostIDs.value)
    deployOpen.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

function certExpiresAt(cert: ManagedCertificate) {
  return cert.material?.expires_at ?? '-'
}

function deploymentText(cert: ManagedCertificate) {
  const hostIDs = cert.deployments?.map(deployment => deployment.host_id).filter(Boolean) ?? []
  return hostIDs.length > 0 ? hostIDs.join(', ') : '-'
}
</script>

<template>
  <section class="certificate-tab">
    <header class="pane-header">
      <h1>{{ t('settings.certificates.title') }}</h1>
      <button type="button" class="primary-btn" data-test="cert-add" @click="openCreate">
        + {{ t('settings.certificates.add') }}
      </button>
    </header>

    <div v-if="error" class="error">{{ error }}</div>

    <section class="account-section">
      <h2>{{ t('settings.certificates.acmeAccount') }}</h2>
      <div class="account-grid">
        <label>
          <span>{{ t('settings.certificates.email') }}</span>
          <input v-model="accountDraft.email" data-test="acme-email" />
        </label>
        <label>
          <span>{{ t('settings.certificates.directory') }}</span>
          <input
            v-model="accountDraft.directory_url"
            list="acme-directory-options"
            :placeholder="t('settings.certificates.leProduction')"
          />
          <datalist id="acme-directory-options">
            <option value="">{{ t('settings.certificates.leProduction') }}</option>
            <option :value="LETS_ENCRYPT_STAGING">{{ t('settings.certificates.leStaging') }}</option>
          </datalist>
        </label>
        <button type="button" class="primary-btn save-account" :disabled="saving" data-test="acme-save" @click="saveAccount">
          {{ t('common.save') }}
        </button>
      </div>
    </section>

    <table class="certificate-table">
      <thead>
        <tr>
          <th>{{ t('settings.certificates.domains') }}</th>
          <th>{{ t('settings.certificates.issuer') }}</th>
          <th>{{ t('settings.certificates.status') }}</th>
          <th>{{ t('settings.certificates.expires') }}</th>
          <th>{{ t('settings.certificates.deployments') }}</th>
          <th>{{ t('settings.certificates.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="cert in certificates" :key="cert.id" data-test="cert-row">
          <td>{{ cert.domains.join(', ') }}</td>
          <td>{{ cert.issuer }}</td>
          <td>
            <span class="status-cell">
              <span class="status-dot" :class="cert.status" />
              {{ cert.status }}
            </span>
          </td>
          <td class="mono">{{ certExpiresAt(cert) }}</td>
          <td>{{ deploymentText(cert) }}</td>
          <td class="actions">
            <button
              v-if="cert.status === 'failed'"
              type="button"
              class="ghost"
              :disabled="saving"
              @click="issueCertificate(cert)"
            >
              {{ t('settings.certificates.retry') }}
            </button>
            <button
              v-if="cert.status === 'active' || cert.status === 'expiring'"
              type="button"
              class="ghost"
              :disabled="saving"
              @click="renewCertificate(cert)"
            >
              {{ t('settings.certificates.renew') }}
            </button>
            <button
              type="button"
              class="ghost"
              :disabled="cert.status !== 'active'"
              :data-test="`cert-deploy-${cert.id}`"
              @click="openDeploy(cert)"
            >
              {{ t('settings.certificates.deploy') }}
            </button>
            <button type="button" class="danger" @click="deleteCertificate(cert)">
              {{ t('common.delete') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="formOpen" class="modal-backdrop" @click.self="formOpen = false">
      <section class="modal">
        <header class="modal-header">
          <h2>{{ t('settings.certificates.add') }}</h2>
          <button type="button" class="icon-btn" @click="formOpen = false">×</button>
        </header>

        <label>
          <span>{{ t('settings.certificates.issuer') }}</span>
          <select v-model="certDraft.issuer">
            <option value="acme">ACME</option>
            <option value="manual">{{ t('settings.certificates.manualCert') }}</option>
          </select>
        </label>

        <div class="domain-list">
          <label v-for="(_, index) in certDraft.domains" :key="index">
            <span>{{ t('settings.certificates.domains') }}</span>
            <div class="domain-row">
              <input v-model="certDraft.domains[index]" :data-test="`cert-domain-${index}`" placeholder="api.example.com" />
              <button type="button" class="icon-btn" @click="removeDomain(index)">×</button>
            </div>
          </label>
          <button type="button" class="ghost add-domain" @click="addDomain">
            + {{ t('settings.certificates.addDomain') }}
          </button>
        </div>

        <template v-if="certDraft.issuer === 'acme'">
          <label>
            <span>{{ t('settings.certificates.dnsProvider') }}</span>
            <select v-model="certDraft.dns_provider" data-test="cert-dns-provider">
              <option v-for="provider in dnsProviders" :key="provider.id || provider.name" :value="provider.id">
                {{ provider.name }}
              </option>
            </select>
          </label>
          <label class="inline-check">
            <input v-model="certDraft.auto_renew" type="checkbox" />
            {{ t('settings.certificates.autoRenew') }}
          </label>
        </template>

        <template v-else>
          <label>
            <span>{{ t('settings.certificates.manualCert') }}</span>
            <textarea v-model="certDraft.cert_pem" rows="5" />
          </label>
          <label>
            <span>{{ t('settings.certificates.privateKey') }}</span>
            <textarea v-model="certDraft.key_pem" rows="5" />
          </label>
        </template>

        <footer>
          <button type="button" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary-btn" :disabled="saving" data-test="cert-submit" @click="submitCertificate">
            {{ t('common.save') }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="deployOpen" class="modal-backdrop" @click.self="deployOpen = false">
      <section class="modal">
        <header class="modal-header">
          <h2>{{ t('settings.certificates.deployHosts') }}</h2>
          <button type="button" class="icon-btn" @click="deployOpen = false">×</button>
        </header>

        <div class="host-list">
          <label v-for="host in hosts" :key="host.id" class="inline-check host-row">
            <input
              v-model="deployHostIDs"
              type="checkbox"
              :value="host.id"
              :data-test="`cert-deploy-host-${host.id}`"
            />
            <span>{{ host.name || host.id }}</span>
          </label>
        </div>

        <footer>
          <button type="button" @click="deployOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary-btn" :disabled="saving" data-test="cert-deploy-submit" @click="submitDeploy">
            {{ t('settings.certificates.deploy') }}
          </button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.certificate-tab {
  width: 100%;
}
.pane-header,
.modal-header,
footer,
.account-grid,
.domain-row,
.status-cell {
  display: flex;
  align-items: center;
}
.pane-header,
.modal-header {
  justify-content: space-between;
  gap: 12px;
}
.pane-header {
  margin-bottom: 16px;
}
h1,
h2 {
  margin: 0;
  font-size: 18px;
}
h2 {
  font-size: 14px;
}
.account-section {
  padding-bottom: 14px;
  margin-bottom: 14px;
  border-bottom: 1px solid var(--border-secondary);
}
.account-grid {
  align-items: flex-end;
  gap: 10px;
  margin-top: 10px;
}
.account-grid label {
  flex: 1;
  min-width: 0;
}
.save-account {
  min-width: 72px;
}
.certificate-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.certificate-table th,
.certificate-table td {
  padding: 7px 8px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
  vertical-align: top;
}
.certificate-table th {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 400;
}
.actions {
  text-align: right;
  white-space: nowrap;
}
.actions button {
  margin-left: 6px;
}
.status-cell {
  gap: 6px;
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-tertiary);
}
.status-dot.active {
  background: var(--status-running);
}
.status-dot.pending {
  background: var(--accent);
}
.status-dot.expiring {
  background: #d29922;
}
.status-dot.failed {
  background: var(--status-failed);
}
.mono {
  font-family: var(--font-mono, monospace);
}
.error {
  padding: 6px 10px;
  margin-bottom: 8px;
  color: var(--status-failed);
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.3);
  font-size: 11px;
}
button {
  padding: 5px 10px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  cursor: pointer;
  font-size: 12px;
}
.primary-btn {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.danger {
  color: var(--status-failed);
}
.ghost {
  color: var(--text-secondary);
}
.icon-btn {
  width: 28px;
  height: 28px;
  padding: 0;
}
.add-domain {
  width: fit-content;
  margin-top: 8px;
}
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal {
  width: min(520px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 10px;
  color: var(--text-secondary);
  font-size: 11px;
}
input,
select,
textarea {
  padding: 6px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
textarea {
  resize: vertical;
  font-family: var(--font-mono, monospace);
}
.domain-list {
  margin-top: 4px;
}
.domain-row {
  gap: 6px;
}
.domain-row input {
  flex: 1;
}
.inline-check {
  flex-direction: row;
  align-items: center;
  gap: 6px;
}
.inline-check input {
  width: auto;
}
.host-list {
  margin-top: 10px;
}
.host-row {
  padding: 7px 0;
  border-bottom: 1px solid var(--border-secondary);
}
.host-row:last-child {
  border-bottom: none;
}
footer {
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}
button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
