<!--
IngressManagerTab：入口配置管理标签页。

职责：
  - 展示入口声明列表和关键落地状态
  - 提供入口声明新建、编辑、预演、应用和孤儿资源确认删除入口
  - 管理可配置 DNS provider 的创建入口

边界：
  - 不直接调用 fetch，所有请求通过 ingress store
  - 不执行 DNS、证书或 nginx 业务逻辑
-->
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIngressStore } from '@/stores/ingress'
import { useRemoteStore } from '@/stores/remote'
import type { Host } from '@/api/agent'
import type { DNSProviderConfig, DNSRecord, Ingress, OrphanConfig } from '@/api/ingress'

const store = useIngressStore()
const remoteStore = useRemoteStore()
const { t } = useI18n()

const formOpen = ref(false)
const providerOpen = ref(false)
const previewIngressId = ref('')
const orphanIngressId = ref('')
const saving = ref(false)
const actionError = ref('')
const formError = ref('')
const providerError = ref('')
const selectedConfigKeys = ref<string[]>([])
const selectedRecordKeys = ref<string[]>([])

const draft = reactive<Ingress>(emptyIngress())
const providerDraft = reactive({
  id: '',
  name: '',
  type: 'cloudflare' as DNSProviderConfig['type'],
  zone_id: '',
  api_token: '',
  access_key_id: '',
  access_key_secret: '',
})

const hostOptions = computed<Host[]>(() => {
  if (remoteStore.hosts.length > 0) return remoteStore.hosts
  return [{
    id: 'self',
    name: t('ingress.hosts.self'),
    ssh_host: '127.0.0.1',
    ssh_port: 22,
    ssh_user: '',
    remote_agent_port: 57017,
    local_tunnel_port: 0,
    tags: [],
    is_self: true,
  }]
})

const dnsProviderOptions = computed(() => [
  { id: 'manual', label: t('ingress.providers.manual') },
  ...store.dnsProviders.map(provider => ({
    id: provider.id ?? provider.name,
    label: provider.name || provider.id || provider.type,
  })),
])

const sortedIngresses = computed(() =>
  [...store.ingresses].sort((a, b) => a.domain.localeCompare(b.domain)),
)

const currentPreview = computed(() =>
  previewIngressId.value ? store.previews[previewIngressId.value] : undefined,
)

const currentOrphanReport = computed(() =>
  orphanIngressId.value ? store.orphanReports[orphanIngressId.value] : undefined,
)

onMounted(async () => {
  actionError.value = ''
  try {
    await Promise.all([store.loadAll(), remoteStore.loadHosts()])
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
})

function emptyIngress(): Ingress {
  return {
    name: '',
    domain: '',
    host_ids: ['self'],
    backend: '',
    proxy_provider: 'nginx',
    proxy_options: {
      websocket: false,
      proxy_timeout: '60s',
      extra_locations: [],
      raw_template: '',
    },
    tls: {
      enabled: false,
      cert_provider: 'acme',
    },
    dns: {
      provider: 'manual',
      record: { type: 'A', name: '', value: '', ttl: 300 },
    },
  }
}

function openCreate() {
  assignDraft(emptyIngress())
  draft.host_ids = [hostOptions.value[0]?.id ?? 'self']
  formError.value = ''
  formOpen.value = true
}

function openEdit(ingress: Ingress) {
  assignDraft({
    ...emptyIngress(),
    ...ingress,
    host_ids: [...ingress.host_ids],
    proxy_options: { ...emptyIngress().proxy_options, ...ingress.proxy_options },
    tls: { ...emptyIngress().tls, ...ingress.tls },
    dns: {
      provider: ingress.dns.provider,
      record: { ...emptyIngress().dns.record, ...ingress.dns.record },
    },
  })
  formError.value = ''
  formOpen.value = true
}

function assignDraft(next: Ingress) {
  Object.assign(draft, next)
}

function isHostSelected(id: string): boolean {
  return draft.host_ids.includes(id)
}

function toggleHost(id: string, checked: boolean) {
  draft.host_ids = checked
    ? [...new Set([...draft.host_ids, id])]
    : draft.host_ids.filter(hostID => hostID !== id)
}

function hostLabel(id: string): string {
  return remoteStore.hostById(id)?.name || hostOptions.value.find(host => host.id === id)?.name || id
}

function dnsProviderLabel(id: string): string {
  return dnsProviderOptions.value.find(option => option.id === id)?.label || id
}

function normalizeDraft(): Ingress {
  const record: DNSRecord = {
    ...draft.dns.record,
    name: draft.dns.record.name || draft.domain,
    ttl: Number(draft.dns.record.ttl) || undefined,
  }
  return {
    ...draft,
    host_ids: [...draft.host_ids],
    dns: { provider: draft.dns.provider, record },
    tls: { ...draft.tls },
    proxy_options: {
      websocket: Boolean(draft.proxy_options?.websocket),
      proxy_timeout: draft.proxy_options?.proxy_timeout || undefined,
      extra_locations: draft.proxy_options?.extra_locations ?? [],
      raw_template: draft.proxy_options?.raw_template || undefined,
    },
  }
}

async function submitIngress() {
  formError.value = ''
  if (!draft.domain.trim()) {
    formError.value = t('ingress.validation.domainRequired')
    return
  }
  if (!draft.backend.trim()) {
    formError.value = t('ingress.validation.backendRequired')
    return
  }
  if (draft.host_ids.length === 0) {
    formError.value = t('ingress.validation.hostRequired')
    return
  }

  saving.value = true
  try {
    await store.saveIngress(normalizeDraft())
    formOpen.value = false
  } catch (error) {
    formError.value = error instanceof Error ? error.message : String(error)
  } finally {
    saving.value = false
  }
}

async function previewIngress(ingress: Ingress) {
  if (!ingress.id) return
  actionError.value = ''
  try {
    await store.previewIngress(ingress.id)
    previewIngressId.value = ingress.id
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

async function applyIngress(ingress: Ingress) {
  if (!ingress.id) return
  actionError.value = ''
  try {
    await store.applyIngress(ingress.id, {
      confirmed_dns_value: ingress.dns.record.value || store.previews[ingress.id]?.dns_value_decision.value,
    })
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

async function detectOrphans(ingress: Ingress) {
  if (!ingress.id) return
  actionError.value = ''
  try {
    const report = await store.detectOrphans(ingress.id)
    orphanIngressId.value = ingress.id
    selectedConfigKeys.value = report.configs.map(configKey)
    selectedRecordKeys.value = report.records.map(recordKey)
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

async function deleteIngress(ingress: Ingress) {
  if (!ingress.id) return
  if (!confirm(t('ingress.deleteConfirm', { domain: ingress.domain }))) return
  actionError.value = ''
  try {
    await store.deleteIngress(ingress.id)
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

function openDNSProvider() {
  Object.assign(providerDraft, {
    id: '',
    name: '',
    type: 'cloudflare',
    zone_id: '',
    api_token: '',
    access_key_id: '',
    access_key_secret: '',
  })
  providerError.value = ''
  providerOpen.value = true
}

async function submitProvider() {
  providerError.value = ''
  if (!providerDraft.name.trim()) {
    providerError.value = t('ingress.validation.providerNameRequired')
    return
  }
  const secrets: Record<string, string> = providerDraft.type === 'cloudflare'
    ? { api_token: providerDraft.api_token }
    : { access_key_id: providerDraft.access_key_id, access_key_secret: providerDraft.access_key_secret }
  try {
    await store.saveDNSProvider({
      id: providerDraft.id || undefined,
      name: providerDraft.name,
      type: providerDraft.type,
      zone_id: providerDraft.zone_id || undefined,
      secrets,
    })
    providerOpen.value = false
  } catch (error) {
    providerError.value = error instanceof Error ? error.message : String(error)
  }
}

function configKey(config: OrphanConfig): string {
  return `${config.host_id}\u0000${config.path}`
}

function recordKey(record: DNSRecord): string {
  return `${record.id ?? ''}\u0000${record.type}\u0000${record.name}\u0000${record.value}`
}

function toggleConfigSelection(key: string, checked: boolean) {
  selectedConfigKeys.value = checked
    ? [...new Set([...selectedConfigKeys.value, key])]
    : selectedConfigKeys.value.filter(item => item !== key)
}

function toggleRecordSelection(key: string, checked: boolean) {
  selectedRecordKeys.value = checked
    ? [...new Set([...selectedRecordKeys.value, key])]
    : selectedRecordKeys.value.filter(item => item !== key)
}

async function removeSelectedOrphans() {
  if (!orphanIngressId.value || !currentOrphanReport.value) return
  const configs = currentOrphanReport.value.configs.filter(item => selectedConfigKeys.value.includes(configKey(item)))
  const records = currentOrphanReport.value.records.filter(item => selectedRecordKeys.value.includes(recordKey(item)))
  try {
    await store.removeOrphans(orphanIngressId.value, { configs, records })
    orphanIngressId.value = ''
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}
</script>

<template>
  <section class="ingress-manager">
    <header class="pane-header">
      <div>
        <h1>{{ t('ingress.title') }}</h1>
        <p>{{ t('ingress.subtitle') }}</p>
      </div>
      <div class="toolbar">
        <button type="button" class="secondary" data-test="ingress-provider-add" @click="openDNSProvider">
          {{ t('ingress.providers.add') }}
        </button>
        <button type="button" class="primary" data-test="ingress-add" @click="openCreate">
          + {{ t('ingress.add') }}
        </button>
      </div>
    </header>

    <div v-if="actionError" class="error">{{ actionError }}</div>

    <div v-if="store.loading" class="empty">{{ t('common.loading') }}</div>
    <div v-else-if="sortedIngresses.length === 0" class="empty">{{ t('ingress.empty') }}</div>
    <table v-else class="ingress-table">
      <thead>
        <tr>
          <th>{{ t('ingress.fields.domain') }}</th>
          <th>{{ t('ingress.fields.backend') }}</th>
          <th>{{ t('ingress.fields.hosts') }}</th>
          <th>{{ t('ingress.fields.dnsProvider') }}</th>
          <th>{{ t('ingress.fields.tls') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in sortedIngresses" :key="item.id || item.domain" data-test="ingress-row">
          <td>
            <div class="domain">{{ item.domain }}</div>
            <div class="muted">{{ item.name || item.id }}</div>
          </td>
          <td class="mono">{{ item.backend }}</td>
          <td>
            <span v-for="hostID in item.host_ids" :key="hostID" class="chip">{{ hostLabel(hostID) }}</span>
          </td>
          <td>{{ dnsProviderLabel(item.dns.provider) }}</td>
          <td>
            <span class="status-dot" :class="{ enabled: item.tls.enabled }"></span>
            {{ item.tls.enabled ? t('ingress.tls.enabled') : t('ingress.tls.disabled') }}
          </td>
          <td class="row-actions">
            <button type="button" @click="previewIngress(item)">{{ t('ingress.actions.preview') }}</button>
            <button type="button" @click="applyIngress(item)">{{ t('ingress.actions.apply') }}</button>
            <button type="button" @click="detectOrphans(item)">{{ t('ingress.actions.detectOrphans') }}</button>
            <button type="button" @click="openEdit(item)">{{ t('common.edit') }}</button>
            <button type="button" class="danger" @click="deleteIngress(item)">{{ t('common.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="formOpen" class="modal-backdrop">
      <section class="modal ingress-form">
        <header class="modal-header">
          <h2>{{ draft.id ? t('ingress.edit') : t('ingress.add') }}</h2>
          <button type="button" class="icon-btn" @click="formOpen = false">×</button>
        </header>
        <div class="form-grid">
          <label>
            <span>{{ t('ingress.fields.name') }}</span>
            <input data-test="ingress-name" v-model="draft.name" />
          </label>
          <label>
            <span>{{ t('ingress.fields.domain') }}</span>
            <input data-test="ingress-domain" v-model="draft.domain" placeholder="api.example.com" />
          </label>
          <label class="wide">
            <span>{{ t('ingress.fields.backend') }}</span>
            <input data-test="ingress-backend" v-model="draft.backend" placeholder="127.0.0.1:8080" />
          </label>
          <div class="wide">
            <span class="field-label">{{ t('ingress.fields.hosts') }}</span>
            <div class="host-list">
              <label v-for="host in hostOptions" :key="host.id" class="check-row">
                <input
                  :data-test="`ingress-host-${host.id}`"
                  type="checkbox"
                  :checked="isHostSelected(host.id)"
                  @change="toggleHost(host.id, ($event.target as HTMLInputElement).checked)"
                />
                {{ host.name || host.id }}
              </label>
            </div>
          </div>
          <label>
            <span>{{ t('ingress.fields.dnsProvider') }}</span>
            <select v-model="draft.dns.provider" data-test="ingress-dns-provider">
              <option v-for="provider in dnsProviderOptions" :key="provider.id" :value="provider.id">
                {{ provider.label }}
              </option>
            </select>
          </label>
          <label>
            <span>{{ t('ingress.fields.recordType') }}</span>
            <select v-model="draft.dns.record.type">
              <option value="A">A</option>
              <option value="AAAA">AAAA</option>
              <option value="CNAME">CNAME</option>
            </select>
          </label>
          <label>
            <span>{{ t('ingress.fields.dnsValue') }}</span>
            <input data-test="ingress-dns-value" v-model="draft.dns.record.value" placeholder="203.0.113.10" />
          </label>
          <label>
            <span>{{ t('ingress.fields.ttl') }}</span>
            <input v-model.number="draft.dns.record.ttl" type="number" min="60" />
          </label>
          <label class="check-row">
            <input v-model="draft.tls.enabled" type="checkbox" />
            {{ t('ingress.fields.enableTLS') }}
          </label>
          <label class="check-row">
            <input v-model="draft.proxy_options!.websocket" type="checkbox" />
            {{ t('ingress.fields.websocket') }}
          </label>
          <label>
            <span>{{ t('ingress.fields.proxyTimeout') }}</span>
            <input v-model="draft.proxy_options!.proxy_timeout" placeholder="60s" />
          </label>
          <label class="wide">
            <span>{{ t('ingress.fields.rawTemplate') }}</span>
            <textarea v-model="draft.proxy_options!.raw_template" rows="5" />
          </label>
        </div>
        <div v-if="formError" class="error">{{ formError }}</div>
        <footer class="modal-actions">
          <button type="button" class="secondary" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary" data-test="ingress-save" :disabled="saving" @click="submitIngress">
            {{ saving ? t('common.loading') : t('common.save') }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="providerOpen" class="modal-backdrop">
      <section class="modal provider-form">
        <header class="modal-header">
          <h2>{{ t('ingress.providers.add') }}</h2>
          <button type="button" class="icon-btn" @click="providerOpen = false">×</button>
        </header>
        <div class="form-grid">
          <label>
            <span>{{ t('ingress.providers.id') }}</span>
            <input v-model="providerDraft.id" placeholder="cloudflare-prod" />
          </label>
          <label>
            <span>{{ t('ingress.providers.name') }}</span>
            <input v-model="providerDraft.name" />
          </label>
          <label>
            <span>{{ t('ingress.providers.type') }}</span>
            <select v-model="providerDraft.type">
              <option value="cloudflare">Cloudflare</option>
              <option value="aliyun">Aliyun</option>
            </select>
          </label>
          <label v-if="providerDraft.type === 'cloudflare'">
            <span>{{ t('ingress.providers.zoneID') }}</span>
            <input v-model="providerDraft.zone_id" />
          </label>
          <label v-if="providerDraft.type === 'cloudflare'" class="wide">
            <span>API Token</span>
            <input v-model="providerDraft.api_token" type="password" />
          </label>
          <label v-if="providerDraft.type === 'aliyun'">
            <span>AccessKey ID</span>
            <input v-model="providerDraft.access_key_id" />
          </label>
          <label v-if="providerDraft.type === 'aliyun'">
            <span>AccessKey Secret</span>
            <input v-model="providerDraft.access_key_secret" type="password" />
          </label>
        </div>
        <div v-if="providerError" class="error">{{ providerError }}</div>
        <footer class="modal-actions">
          <button type="button" class="secondary" @click="providerOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary" @click="submitProvider">{{ t('common.save') }}</button>
        </footer>
      </section>
    </div>

    <div v-if="currentPreview" class="modal-backdrop">
      <section class="modal preview-modal">
        <header class="modal-header">
          <h2>{{ t('ingress.preview.title') }}</h2>
          <button type="button" class="icon-btn" @click="previewIngressId = ''">×</button>
        </header>
        <div class="preview-section">
          <div class="section-title">{{ t('ingress.preview.dns') }}</div>
          <div class="mono">
            {{ currentPreview.dns_record.type }} {{ currentPreview.dns_record.name }} → {{ currentPreview.dns_record.value }}
          </div>
          <div v-for="line in currentPreview.manual_instructions" :key="line" class="muted">{{ line }}</div>
        </div>
        <div class="preview-section">
          <div class="section-title">{{ t('ingress.preview.config') }}</div>
          <pre v-for="(content, hostID) in currentPreview.rendered_config_by_host" :key="hostID">{{ hostLabel(hostID) }}&#10;{{ content }}</pre>
        </div>
      </section>
    </div>

    <div v-if="currentOrphanReport" class="modal-backdrop">
      <section class="modal orphan-modal">
        <header class="modal-header">
          <h2>{{ t('ingress.orphans.title') }}</h2>
          <button type="button" class="icon-btn" @click="orphanIngressId = ''">×</button>
        </header>
        <div v-if="currentOrphanReport.configs.length === 0 && currentOrphanReport.records.length === 0" class="empty">
          {{ t('ingress.orphans.empty') }}
        </div>
        <div v-else class="orphan-list">
          <label v-for="config in currentOrphanReport.configs" :key="configKey(config)" class="check-row">
            <input
              type="checkbox"
              :checked="selectedConfigKeys.includes(configKey(config))"
              @change="toggleConfigSelection(configKey(config), ($event.target as HTMLInputElement).checked)"
            />
            <span class="mono">{{ hostLabel(config.host_id) }} {{ config.path }}</span>
          </label>
          <label v-for="record in currentOrphanReport.records" :key="recordKey(record)" class="check-row">
            <input
              type="checkbox"
              :checked="selectedRecordKeys.includes(recordKey(record))"
              @change="toggleRecordSelection(recordKey(record), ($event.target as HTMLInputElement).checked)"
            />
            <span class="mono">{{ record.type }} {{ record.name }} → {{ record.value }}</span>
          </label>
        </div>
        <footer class="modal-actions">
          <button type="button" class="secondary" @click="orphanIngressId = ''">{{ t('common.close') }}</button>
          <button type="button" class="danger primary-like" @click="removeSelectedOrphans">{{ t('ingress.orphans.removeSelected') }}</button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.ingress-manager {
  width: 100%;
}
.pane-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
h1,
h2 {
  margin: 0;
}
h1 {
  font-size: 18px;
}
h2 {
  font-size: 15px;
}
p {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
}
.toolbar,
.row-actions,
.modal-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
button {
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 5px 9px;
  cursor: pointer;
  font-size: 11px;
}
.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.secondary {
  background: var(--bg-overlay);
  color: var(--text-secondary);
}
.danger {
  color: var(--status-failed);
  background: transparent;
}
.primary-like {
  border-color: var(--status-failed);
}
.icon-btn {
  width: 26px;
  height: 26px;
  padding: 0;
  background: transparent;
  color: var(--text-secondary);
}
.empty,
.error {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  padding: 14px;
  background: var(--bg-elevated);
  color: var(--text-tertiary);
  font-size: 12px;
}
.error {
  margin-bottom: 10px;
  color: var(--status-failed);
}
.ingress-table {
  width: 100%;
  border-collapse: collapse;
  border: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
}
th,
td {
  padding: 10px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
  vertical-align: top;
  font-size: 12px;
}
th {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 500;
}
.domain {
  font-weight: 600;
}
.muted {
  color: var(--text-tertiary);
  font-size: 11px;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.chip {
  display: inline-flex;
  margin: 0 4px 4px 0;
  padding: 2px 6px;
  border-radius: 999px;
  background: var(--bg-overlay);
  color: var(--text-secondary);
  font-size: 11px;
}
.status-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 5px;
  border-radius: 50%;
  background: var(--border);
}
.status-dot.enabled {
  background: var(--status-running);
}
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.36);
}
.modal {
  width: min(760px, calc(100vw - 40px));
  max-height: calc(100vh - 42px);
  overflow: auto;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.25);
}
.provider-form {
  width: min(520px, calc(100vw - 40px));
}
.modal-header,
.modal-actions {
  padding: 12px 14px;
}
.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--border-secondary);
}
.modal-actions {
  justify-content: flex-end;
  border-top: 1px solid var(--border-secondary);
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  padding: 14px;
}
label,
.field-label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
.wide {
  grid-column: 1 / -1;
}
input,
select,
textarea {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  padding: 6px 8px;
  font-size: 12px;
}
textarea {
  resize: vertical;
}
.host-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.check-row {
  flex-direction: row;
  align-items: center;
  gap: 6px;
}
.check-row input {
  width: auto;
}
.preview-section {
  padding: 14px;
  border-bottom: 1px solid var(--border-secondary);
}
.section-title {
  margin-bottom: 8px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}
pre {
  max-height: 280px;
  overflow: auto;
  margin: 8px 0 0;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 11px;
  line-height: 1.5;
}
.orphan-list {
  padding: 14px;
}
</style>
