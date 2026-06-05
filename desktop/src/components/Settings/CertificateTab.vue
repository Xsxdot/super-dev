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
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useCertStore } from '@/stores/cert'
import { useIngressStore } from '@/stores/ingress'
import { useRemoteStore } from '@/stores/remote'
import type { CertificateCreatePayload, CertificateDeployPayload, CertificateIssuer, ManagedCertificate } from '@/api/cert'

const ISSUE_POLL_INTERVAL_MS = 1000
const ISSUE_POLL_ATTEMPTS = 90
const ACCOUNT_NOTICE_RESET_MS = 1800
const LETS_ENCRYPT_PRODUCTION = 'https://acme-v02.api.letsencrypt.org/directory'
const LETS_ENCRYPT_STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory'
const CUSTOM_ACME_DIRECTORY = '__custom_acme_directory__'

const certStore = useCertStore()
const ingressStore = useIngressStore()
const remoteStore = useRemoteStore()
const { t } = useI18n()

const formOpen = ref(false)
const deployOpen = ref(false)
const selectedCert = ref<ManagedCertificate | null>(null)
const saving = ref(false)
const error = ref('')
const accountNotice = ref('')
const accountDraft = reactive({
  email: '',
  directory_option: LETS_ENCRYPT_PRODUCTION,
  custom_directory_url: '',
})
const certDraft = reactive({
  domains: [''],
  issuer: 'acme' as CertificateIssuer,
  dns_provider: '',
  auto_renew: true,
  cert_pem: '',
  key_pem: '',
})
const deployHostIDs = ref<string[]>([])
const deployDraft = reactive({
  cert_path: '',
  key_path: '',
  post_deploy_command: '',
})

const certificates = computed(() => certStore.certificates)
const dnsProviders = computed(() => Array.isArray(ingressStore.dnsProviders) ? ingressStore.dnsProviders : [])
const hosts = computed(() => remoteStore.hosts)
const hasDNSProviders = computed(() => dnsProviders.value.length > 0)
let accountNoticeTimer: ReturnType<typeof setTimeout> | null = null

onMounted(async () => {
  try {
    await Promise.all([certStore.loadAll(), ingressStore.loadDNSProviders(), remoteStore.loadHosts()])
    accountDraft.email = certStore.acmeAccount.email
    applyAccountDirectory(certStore.acmeAccount.directory_url)
    certDraft.dns_provider = defaultDNSProviderID()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
})

onBeforeUnmount(() => {
  clearAccountNoticeTimer()
})

function clearAccountNoticeTimer() {
  if (!accountNoticeTimer) return
  clearTimeout(accountNoticeTimer)
  accountNoticeTimer = null
}

function scheduleAccountNoticeReset() {
  clearAccountNoticeTimer()
  accountNoticeTimer = setTimeout(() => {
    accountNotice.value = ''
    accountNoticeTimer = null
  }, ACCOUNT_NOTICE_RESET_MS)
}

function defaultDNSProviderID() {
  return dnsProviders.value[0]?.id ?? ''
}

function applyAccountDirectory(directoryURL?: string) {
  const normalized = (directoryURL ?? '').trim() || LETS_ENCRYPT_PRODUCTION
  if (normalized === LETS_ENCRYPT_PRODUCTION || normalized === LETS_ENCRYPT_STAGING) {
    accountDraft.directory_option = normalized
    accountDraft.custom_directory_url = ''
    return
  }
  accountDraft.directory_option = CUSTOM_ACME_DIRECTORY
  accountDraft.custom_directory_url = normalized
}

function selectedDirectoryURL() {
  if (accountDraft.directory_option !== CUSTOM_ACME_DIRECTORY) {
    return accountDraft.directory_option
  }
  return accountDraft.custom_directory_url.trim()
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
  accountNotice.value = ''
  clearAccountNoticeTimer()
  try {
    const directoryURL = selectedDirectoryURL()
    if (!directoryURL) {
      error.value = t('settings.certificates.customDirectoryRequired')
      return
    }
    await certStore.saveACMEAccount({
      email: accountDraft.email.trim(),
      directory_url: directoryURL,
    })
    accountDraft.email = certStore.acmeAccount.email
    applyAccountDirectory(certStore.acmeAccount.directory_url)
    accountNotice.value = t('settings.certificates.acmeAccountSaved')
    scheduleAccountNoticeReset()
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
  if (certDraft.issuer === 'acme' && !certDraft.dns_provider) {
    error.value = t('settings.certificates.dnsProviderRequired')
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
      startIssuePolling(created.id)
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
  const existing = cert.deployments?.[0]
  deployDraft.cert_path = existing?.cert_path || defaultCertPath(cert)
  deployDraft.key_path = existing?.key_path || defaultKeyPath(cert)
  deployDraft.post_deploy_command = existing?.post_deploy_command || ''
  error.value = ''
  deployOpen.value = true
}

async function submitDeploy() {
  if (!selectedCert.value) return
  if (deployHostIDs.value.length === 0) {
    error.value = t('settings.certificates.selectHostRequired')
    return
  }
  saving.value = true
  error.value = ''
  try {
    const payload: CertificateDeployPayload = {
      deployments: deployHostIDs.value.map(hostID => ({
        host_id: hostID,
        cert_path: deployDraft.cert_path.trim(),
        key_path: deployDraft.key_path.trim(),
        post_deploy_command: deployDraft.post_deploy_command.trim(),
      })),
    }
    await certStore.deployCertificate(selectedCert.value.id, payload)
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

function primaryDomain(cert: ManagedCertificate) {
  return cert.domains[0] ?? cert.material?.domain ?? 'certificate'
}

function defaultCertPath(cert: ManagedCertificate) {
  return `/etc/superdev/ingress/certs/${primaryDomain(cert)}/fullchain.pem`
}

function defaultKeyPath(cert: ManagedCertificate) {
  return `/etc/superdev/ingress/certs/${primaryDomain(cert)}/privkey.pem`
}

function deploymentStatusText(status?: string) {
  if (status === 'failed') return t('settings.certificates.deploymentFailed')
  if (status === 'succeeded') return t('settings.certificates.deploymentSucceeded')
  return ''
}

async function copyText(value: string) {
  if (!value) return
  await navigator.clipboard?.writeText(value)
}

function certStatusText(status: ManagedCertificate['status']) {
  const keyByStatus: Record<ManagedCertificate['status'], string> = {
    pending: 'settings.certificates.statusPending',
    active: 'settings.certificates.statusActive',
    expiring: 'settings.certificates.statusExpiring',
    failed: 'settings.certificates.statusFailed',
  }
  return t(keyByStatus[status])
}

function startIssuePolling(certID: string) {
  void certStore.issueCertificate(certID, {
    intervalMs: ISSUE_POLL_INTERVAL_MS,
    maxAttempts: ISSUE_POLL_ATTEMPTS,
  }).catch((err) => {
    error.value = err instanceof Error ? err.message : String(err)
  })
}
</script>

<template>
  <section class="certificate-tab">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.certificates.title') }}</h1>
      </div>
      <button type="button" class="settings-btn settings-btn-primary" data-test="cert-add" @click="openCreate">
        + {{ t('settings.certificates.add') }}
      </button>
    </header>

    <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>

    <section class="settings-section account-section">
      <h2 class="account-title">{{ t('settings.certificates.acmeAccount') }}</h2>
      <div class="settings-form-grid account-grid">
        <label class="settings-field">
          <span class="settings-field-label">{{ t('settings.certificates.email') }}</span>
          <input v-model="accountDraft.email" class="settings-input" data-test="acme-email" />
        </label>
        <label class="settings-field">
          <span class="settings-field-label">{{ t('settings.certificates.directory') }}</span>
          <select v-model="accountDraft.directory_option" class="settings-select" data-test="acme-directory">
            <option :value="LETS_ENCRYPT_PRODUCTION">{{ t('settings.certificates.leProduction') }}</option>
            <option :value="LETS_ENCRYPT_STAGING">{{ t('settings.certificates.leStaging') }}</option>
            <option :value="CUSTOM_ACME_DIRECTORY">{{ t('settings.certificates.customDirectory') }}</option>
          </select>
        </label>
        <label v-if="accountDraft.directory_option === CUSTOM_ACME_DIRECTORY" class="settings-field">
          <span class="settings-field-label">{{ t('settings.certificates.customDirectory') }}</span>
          <input v-model="accountDraft.custom_directory_url" class="settings-input" data-test="acme-custom-directory" />
        </label>
        <button
          type="button"
          class="settings-btn settings-btn-primary save-account"
          :disabled="saving"
          data-test="acme-save"
          @click="saveAccount"
        >
          {{ t('common.save') }}
        </button>
      </div>
      <p
        v-if="accountNotice"
        class="settings-alert settings-alert-success account-notice"
        role="status"
        aria-live="polite"
        data-test="acme-save-notice"
      >
        {{ accountNotice }}
      </p>
    </section>

    <div v-if="!hasDNSProviders" class="settings-alert settings-alert-warning" data-test="cert-dns-notice">
      {{ t('settings.certificates.dnsProviderRequired') }}
    </div>

    <div class="settings-surface settings-surface-scroll">
      <table class="settings-table certificate-table">
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
                {{ certStatusText(cert.status) }}
              </span>
            </td>
            <td class="settings-mono">{{ certExpiresAt(cert) }}</td>
            <td>
              <div v-if="cert.deployments?.length" class="deployment-list">
                <div v-for="deployment in cert.deployments" :key="deployment.host_id" class="deployment-item">
                  <div class="deployment-host">
                    <span>{{ deployment.host_id }}</span>
                    <span v-if="deploymentStatusText(deployment.status)" class="deployment-status">
                      {{ deploymentStatusText(deployment.status) }}
                    </span>
                  </div>
                  <div class="deployment-path">
                    <span class="settings-mono">{{ deployment.cert_path }}</span>
                    <button
                      type="button"
                      class="settings-btn settings-btn-text copy-btn"
                      :data-test="`cert-copy-cert-${deployment.host_id}`"
                      @click="copyText(deployment.cert_path)"
                    >
                      {{ t('settings.certificates.copy') }}
                    </button>
                  </div>
                  <div class="deployment-path">
                    <span class="settings-mono">{{ deployment.key_path }}</span>
                    <button
                      type="button"
                      class="settings-btn settings-btn-text copy-btn"
                      :data-test="`cert-copy-key-${deployment.host_id}`"
                      @click="copyText(deployment.key_path)"
                    >
                      {{ t('settings.certificates.copy') }}
                    </button>
                  </div>
                  <div v-if="deployment.last_error" class="deployment-error">{{ deployment.last_error }}</div>
                </div>
              </div>
              <span v-else>-</span>
            </td>
            <td class="actions">
              <button
                v-if="cert.status === 'failed'"
                type="button"
                class="settings-btn settings-btn-secondary"
                :disabled="saving"
                @click="issueCertificate(cert)"
              >
                {{ t('settings.certificates.retry') }}
              </button>
              <button
                v-if="cert.status === 'active' || cert.status === 'expiring'"
                type="button"
                class="settings-btn settings-btn-secondary"
                :disabled="saving"
                @click="renewCertificate(cert)"
              >
                {{ t('settings.certificates.renew') }}
              </button>
              <button
                type="button"
                class="settings-btn settings-btn-secondary"
                :disabled="cert.status !== 'active'"
                :data-test="`cert-deploy-${cert.id}`"
                @click="openDeploy(cert)"
              >
                {{ t('settings.certificates.deploy') }}
              </button>
              <button type="button" class="settings-btn settings-btn-danger" @click="deleteCertificate(cert)">
                {{ t('common.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="formOpen" class="settings-modal-backdrop" @click.self="formOpen = false">
      <section class="settings-modal">
        <header class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.certificates.add') }}</h2>
          <button type="button" class="settings-btn settings-btn-icon settings-btn-ghost" @click="formOpen = false">×</button>
        </header>

        <div class="settings-modal-body">
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.certificates.issuer') }}</span>
            <select v-model="certDraft.issuer" class="settings-select">
              <option value="acme">ACME</option>
              <option value="manual">{{ t('settings.certificates.manualCert') }}</option>
            </select>
          </label>

          <div class="domain-list">
            <label v-for="(_, index) in certDraft.domains" :key="index" class="settings-field">
              <span class="settings-field-label">{{ t('settings.certificates.domains') }}</span>
              <div class="domain-row">
                <input
                  v-model="certDraft.domains[index]"
                  class="settings-input"
                  :data-test="`cert-domain-${index}`"
                  placeholder="api.example.com"
                />
                <button type="button" class="settings-btn settings-btn-icon settings-btn-ghost" @click="removeDomain(index)">×</button>
              </div>
            </label>
            <button type="button" class="settings-btn settings-btn-secondary add-domain" @click="addDomain">
              + {{ t('settings.certificates.addDomain') }}
            </button>
          </div>

          <template v-if="certDraft.issuer === 'acme'">
            <div v-if="!hasDNSProviders" class="settings-alert settings-alert-warning" data-test="cert-dns-empty">
              {{ t('settings.certificates.dnsProviderRequired') }}
            </div>
            <label v-else class="settings-field">
              <span class="settings-field-label">{{ t('settings.certificates.dnsProvider') }}</span>
              <select v-model="certDraft.dns_provider" class="settings-select" data-test="cert-dns-provider">
                <option v-for="provider in dnsProviders" :key="provider.id || provider.name" :value="provider.id">
                  {{ provider.name }}
                </option>
              </select>
            </label>
            <label class="settings-field inline-check">
              <input v-model="certDraft.auto_renew" type="checkbox" />
              {{ t('settings.certificates.autoRenew') }}
            </label>
          </template>

          <template v-else>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.certificates.manualCert') }}</span>
              <textarea v-model="certDraft.cert_pem" class="settings-textarea" rows="5" />
            </label>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.certificates.privateKey') }}</span>
              <textarea v-model="certDraft.key_pem" class="settings-textarea" rows="5" />
            </label>
          </template>
        </div>

        <footer class="settings-modal-footer">
          <button type="button" class="settings-btn" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-primary"
            :disabled="saving || (certDraft.issuer === 'acme' && !hasDNSProviders)"
            data-test="cert-submit"
            @click="submitCertificate"
          >
            {{ t('common.save') }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="deployOpen" class="settings-modal-backdrop" @click.self="deployOpen = false">
      <section class="settings-modal">
        <header class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.certificates.deployHosts') }}</h2>
          <button type="button" class="settings-btn settings-btn-icon settings-btn-ghost" @click="deployOpen = false">×</button>
        </header>

        <div class="settings-modal-body">
          <div class="host-list">
            <label v-for="host in hosts" :key="host.id" class="settings-field inline-check host-row">
              <input
                v-model="deployHostIDs"
                type="checkbox"
                :value="host.id"
                :data-test="`cert-deploy-host-${host.id}`"
              />
              <span>{{ host.name || host.id }}</span>
            </label>
          </div>

          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.certificates.certPath') }}</span>
            <input
              v-model="deployDraft.cert_path"
              class="settings-input settings-mono"
              data-test="cert-deploy-cert-path"
            />
          </label>

          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.certificates.keyPath') }}</span>
            <input
              v-model="deployDraft.key_path"
              class="settings-input settings-mono"
              data-test="cert-deploy-key-path"
            />
          </label>

          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.certificates.postDeployCommand') }}</span>
            <textarea
              v-model="deployDraft.post_deploy_command"
              class="settings-textarea settings-mono"
              rows="3"
              data-test="cert-deploy-command"
            />
          </label>
        </div>

        <footer class="settings-modal-footer">
          <button type="button" class="settings-btn" @click="deployOpen = false">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-primary"
            :disabled="saving"
            data-test="cert-deploy-submit"
            @click="submitDeploy"
          >
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
.account-title {
  margin: 0 0 12px;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 650;
}
.account-grid {
  align-items: flex-end;
}
.save-account {
  min-width: 72px;
}
.actions {
  text-align: right;
  white-space: nowrap;
}
.actions button {
  margin-left: 6px;
}
.status-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}
.deployment-list {
  display: grid;
  gap: 8px;
  min-width: 240px;
}
.deployment-item {
  display: grid;
  gap: 3px;
}
.deployment-host,
.deployment-path {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.deployment-host {
  color: var(--text-primary);
  font-weight: 500;
}
.deployment-status {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 400;
}
.deployment-path .settings-mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.deployment-error {
  color: var(--status-failed);
}
.copy-btn {
  flex: 0 0 auto;
  font-size: 11px;
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
  background: transparent;
  border: 1px solid rgba(47, 129, 247, 0.28);
  border-top-color: var(--accent);
  animation: cert-spin 0.8s linear infinite;
}
.status-dot.expiring {
  background: #d29922;
}
.status-dot.failed {
  background: var(--status-failed);
}
.account-notice {
  margin: 8px 0 0;
}
.add-domain {
  width: fit-content;
  margin-top: 8px;
}
.settings-modal-body {
  display: grid;
  gap: 10px;
}
.domain-list {
  display: grid;
  gap: 10px;
}
.domain-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.domain-row input {
  flex: 1;
}
.inline-check {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 6px;
}
.inline-check input {
  width: auto;
}
.host-list {
  display: grid;
}
.host-row {
  padding: 7px 0;
  border-bottom: 1px solid var(--border-secondary);
}
.host-row:last-child {
  border-bottom: none;
}
@keyframes cert-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
