<!--
ProjectIngressTab：项目概览中的入口配置管理。

职责：
  - 只加载和保存当前项目的 Ingress 声明
  - 提供反向代理节点、upstream、DNS 多记录和 nginx Raw Template 表单
  - 调用项目级推断接口，把流水线 role 推断为可编辑默认值

边界：
  - 不管理全局 DNS Provider 凭据（设置页负责）
  - 不验证 nginx 语法或执行远程 apply 细节
-->
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useCertStore } from '@/stores/cert'
import { useIngressStore } from '@/stores/ingress'
import { useRemoteStore } from '@/stores/remote'
import { buildNginxRawTemplate } from '@/lib/ingressTemplate'
import type { ManagedCertificate } from '@/api/cert'
import type { DNSRecord, Ingress, RecordType, Upstream } from '@/api/ingress'
import type { Host, Project } from '@/api/agent'

const props = defineProps<{ project: Project }>()

const router = useRouter()
const certStore = useCertStore()
const ingressStore = useIngressStore()
const remoteStore = useRemoteStore()
const { t } = useI18n()

interface Draft {
  id?: string
  name: string
  domain: string
  source_hint: {
    env_name: string
    pipeline_id: string
    role: string
  }
  proxy: {
    provider: string
    host_ids: string[]
  }
  upstreams: Upstream[]
  proxy_options: {
    websocket: boolean
    proxy_timeout: string
    raw_template: string
  }
  tls: {
    enabled: boolean
    cert_id: string
  }
  dns: {
    provider: string
    record_type: RecordType
    ttl: number
    records: DNSRecord[]
  }
}

const formOpen = ref(false)
const saving = ref(false)
const loading = ref(false)
const error = ref('')
const formError = ref('')
const warnings = ref<string[]>([])
const previewIngressId = ref('')
const orphanIngressId = ref('')
const matchingCertID = ref('')
const certMatchLoading = ref(false)
let certMatchSeq = 0

const draft = reactive<Draft>(emptyDraft())

const sortedIngresses = computed(() =>
  [...ingressStore.ingresses].sort((a, b) => a.domain.localeCompare(b.domain)),
)
const sortedHosts = computed(() =>
  [...remoteStore.hosts].sort((a, b) => a.name.localeCompare(b.name)),
)
const activeCertificates = computed(() =>
  certStore.certificates.filter(cert => cert.status === 'active'),
)
const dnsProviderOptions = computed(() => [
  { id: 'manual', name: t('settings.dnsProviders.manual'), type: 'manual' },
  ...ingressStore.dnsProviders.map(provider => ({
    id: provider.id ?? provider.name,
    name: provider.name,
    type: provider.type,
  })),
])
const selectedPipeline = computed(() =>
  (props.project.pipelines ?? []).find(pipeline => pipeline.id === draft.source_hint.pipeline_id),
)
const roleOptions = computed(() => Object.keys(selectedPipeline.value?.roles ?? {}))
const selectedProxyHosts = computed(() =>
  sortedHosts.value.filter(host => draft.proxy.host_ids.includes(host.id)),
)
const proxySelectorOpen = ref(false)
const proxyHostQuery = ref('')
const proxyHostFilter = ref<'public' | 'selected' | 'all'>('public')
const upstreamHostQuery = ref('')
const selectedProxyHostIDs = computed(() => new Set(draft.proxy.host_ids))
const filteredProxyHosts = computed(() => {
  const query = proxyHostQuery.value.trim().toLowerCase()
  return sortedHosts.value.filter(host => {
    if (proxyHostFilter.value === 'public' && !host.public_ip?.trim()) return false
    if (proxyHostFilter.value === 'selected' && !selectedProxyHostIDs.value.has(host.id)) return false
    const haystack = [
      host.name,
      host.public_ip ?? '',
      host.private_ip ?? '',
      host.ssh_host,
    ].join(' ').toLowerCase()
    return query === '' || haystack.includes(query)
  })
})
const filteredUpstreamHosts = computed(() => {
  const query = upstreamHostQuery.value.trim().toLowerCase()
  return sortedHosts.value.filter(host => {
    const haystack = [
      host.name,
      host.private_ip ?? '',
      host.public_ip ?? '',
      host.ssh_host,
    ].join(' ').toLowerCase()
    return query === '' || haystack.includes(query)
  })
})

onMounted(async () => {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([
      ingressStore.loadProject(props.project.id),
      remoteStore.loadHosts(),
      certStore.loadAll(),
    ])
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
})

function emptyDraft(): Draft {
  const envName = props?.project?.environments?.[0]?.name ?? 'prod'
  const pipeline = props?.project?.pipelines?.[0]
  const role = Object.keys(pipeline?.roles ?? {})[0] ?? ''
  return {
    name: '',
    domain: '',
    source_hint: {
      env_name: envName,
      pipeline_id: pipeline?.id ?? '',
      role,
    },
    proxy: {
      provider: 'nginx',
      host_ids: [],
    },
    upstreams: [],
    proxy_options: {
      websocket: false,
      proxy_timeout: '60s',
      raw_template: '',
    },
    tls: {
      enabled: false,
      cert_id: '',
    },
    dns: {
      provider: 'manual',
      record_type: 'A',
      ttl: 300,
      records: [],
    },
  }
}

function resetDraft(next: Draft) {
  Object.assign(draft, {
    ...next,
    source_hint: { ...next.source_hint },
    proxy: { ...next.proxy, host_ids: [...next.proxy.host_ids] },
    upstreams: next.upstreams.map(row => ({ ...row })),
    proxy_options: { ...next.proxy_options },
    tls: { ...next.tls },
    dns: {
      ...next.dns,
      ttl: next.dns.ttl,
      records: next.dns.records.map(record => ({ ...record })),
    },
  })
}

function openCreate() {
  warnings.value = []
  formError.value = ''
  matchingCertID.value = ''
  proxySelectorOpen.value = false
  proxyHostQuery.value = ''
  proxyHostFilter.value = 'public'
  upstreamHostQuery.value = ''
  resetDraft(emptyDraft())
  regenerateTemplate()
  formOpen.value = true
}

function openEdit(ingress: Ingress) {
  warnings.value = []
  formError.value = ''
  matchingCertID.value = ''
  const firstRecord = ingress.dns.records?.[0]
  resetDraft({
    id: ingress.id,
    name: ingress.name,
    domain: ingress.domain,
    source_hint: {
      env_name: ingress.source_hint?.env_name ?? props.project.environments?.[0]?.name ?? 'prod',
      pipeline_id: ingress.source_hint?.pipeline_id ?? props.project.pipelines?.[0]?.id ?? '',
      role: ingress.source_hint?.role ?? '',
    },
    proxy: {
      provider: ingress.proxy?.provider ?? 'nginx',
      host_ids: [...(ingress.proxy?.host_ids ?? [])],
    },
    upstreams: (ingress.upstreams ?? []).map(row => ({ ...row })),
    proxy_options: {
      websocket: ingress.proxy_options?.websocket ?? false,
      proxy_timeout: ingress.proxy_options?.proxy_timeout ?? '60s',
      raw_template: ingress.proxy_options?.raw_template ?? '',
    },
    tls: {
      enabled: ingress.tls.enabled,
      cert_id: ingress.tls.cert_id ?? '',
    },
    dns: {
      provider: ingress.dns.provider,
      record_type: firstRecord?.type ?? 'A',
      ttl: Number(firstRecord?.ttl || 300),
      records: (ingress.dns.records?.length ? ingress.dns.records : firstRecord ? [firstRecord] : [])
        .map(record => ({ ...record })),
    },
  })
  proxySelectorOpen.value = false
  proxyHostQuery.value = ''
  proxyHostFilter.value = 'public'
  upstreamHostQuery.value = ''
  formOpen.value = true
  void refreshCertificateMatch()
}

function hostLabel(hostID: string): string {
  const host = remoteStore.hostById(hostID)
  return host?.name ?? hostID
}

function publicAddressForHost(host: Host): string {
  return host.public_ip?.trim() || host.ssh_host.trim()
}

function privateAddressForHost(host: Host): string {
  return host.private_ip?.trim() || host.ssh_host.trim()
}

function hostAddress(host: Host): string {
  const publicPart = host.public_ip ? `public ${host.public_ip}` : ''
  const privatePart = host.private_ip ? `private ${host.private_ip}` : ''
  return [publicPart, privatePart].filter(Boolean).join(' · ') || host.ssh_host
}

function certLabel(cert: ManagedCertificate): string {
  return cert.domains.join(', ')
}

function isProxyHostSelected(hostID: string): boolean {
  return draft.proxy.host_ids.includes(hostID)
}

function toggleProxyHost(hostID: string, checked: boolean) {
  draft.proxy.host_ids = checked
    ? [...new Set([...draft.proxy.host_ids, hostID])]
    : draft.proxy.host_ids.filter(id => id !== hostID)
  syncDNSRecordsFromProxyHosts()
}

function currentDNSTTL(): number {
  return Number(draft.dns.ttl || 300)
}

function knownProxyDNSValues(): Set<string> {
  return new Set(
    sortedHosts.value
      .map(host => publicAddressForHost(host))
      .filter(value => value.trim() !== ''),
  )
}

function syncDNSRecordsFromProxyHosts(preserveManual = true) {
  const ttl = currentDNSTTL()
  const hostRecords = selectedProxyHosts.value
    .map(host => ({
      type: draft.dns.record_type,
      name: draft.domain,
      value: publicAddressForHost(host),
      ttl,
    }))
    .filter(record => record.value.trim() !== '')
  const selectedValues = new Set(hostRecords.map(record => record.value.trim()))
  const knownValues = knownProxyDNSValues()
  const manualRecords = preserveManual
    ? draft.dns.records
        .filter(record => {
          const value = record.value.trim()
          return value === '' || (!selectedValues.has(value) && !knownValues.has(value))
        })
        .map(record => ({
          ...record,
          type: draft.dns.record_type,
          name: record.name.trim() || draft.domain,
          ttl,
        }))
    : []
  draft.dns.records = [...hostRecords, ...manualRecords]
}

function addUpstream() {
  draft.upstreams.push({ ip: '', port: '' })
  regenerateTemplate()
}

function removeUpstream(index: number) {
  draft.upstreams.splice(index, 1)
  regenerateTemplate()
}

function setUpstreamIP(index: number, value: string) {
  const row = draft.upstreams[index]
  row.ip = value
  if (row.host_id) {
    const host = remoteStore.hostById(row.host_id)
    if (host && value.trim() !== privateAddressForHost(host)) delete row.host_id
  }
  regenerateTemplate()
}

function setUpstreamPort(index: number, value: string) {
  const nextPort = value.trim() === '' ? '' : Number(value)
  const row = draft.upstreams[index]
  const shouldPropagate = row.port === '' && nextPort !== ''
  row.port = nextPort
  if (shouldPropagate) {
    const sourceIP = row.ip.trim()
    for (const [rowIndex, sibling] of draft.upstreams.entries()) {
      if (rowIndex === index || sibling.port !== '') continue
      const siblingIP = sibling.ip.trim()
      if (siblingIP === '' || siblingIP === sourceIP) continue
      sibling.port = nextPort
    }
  }
  regenerateTemplate()
}

function addDNSRecord() {
  draft.dns.records.push({
    type: draft.dns.record_type,
    name: draft.domain,
    value: '',
    ttl: currentDNSTTL(),
  })
}

function removeDNSRecord(index: number) {
  draft.dns.records.splice(index, 1)
}

function setRecordType(value: RecordType) {
  draft.dns.record_type = value
  for (const record of draft.dns.records) {
    record.type = value
  }
}

function setDNSTTL(value: string) {
  draft.dns.ttl = Number(value || 300)
  for (const record of draft.dns.records) {
    record.ttl = draft.dns.ttl
  }
}

function syncRecordNames() {
  for (const record of draft.dns.records) {
    record.name = draft.domain
  }
}

function isUpstreamHostSelected(hostID: string): boolean {
  return draft.upstreams.some(row => row.host_id === hostID)
}

function toggleUpstreamHost(hostID: string, checked: boolean) {
  const host = remoteStore.hostById(hostID)
  if (!host) return
  if (checked) {
    if (!isUpstreamHostSelected(hostID)) {
      draft.upstreams.push({ host_id: hostID, ip: privateAddressForHost(host), port: '' })
    }
  } else {
    draft.upstreams = draft.upstreams.filter(row => row.host_id !== hostID)
  }
  regenerateTemplate()
}

function regenerateTemplate() {
  draft.proxy_options.raw_template = buildNginxRawTemplate({
    domain: draft.domain,
    upstreams: draft.upstreams,
    websocket: draft.proxy_options.websocket,
    proxyTimeout: draft.proxy_options.proxy_timeout,
    tlsEnabled: draft.tls.enabled,
  })
}

async function refreshCertificateMatch() {
  const seq = ++certMatchSeq
  const previousMatchID = matchingCertID.value
  matchingCertID.value = ''
  if (!draft.tls.enabled || !draft.domain.trim()) return
  certMatchLoading.value = true
  try {
    const matched = await certStore.matchCertificate(draft.domain.trim())
    if (seq !== certMatchSeq) return
    matchingCertID.value = matched?.id ?? ''
    if (matched?.id && (!draft.tls.cert_id || draft.tls.cert_id === previousMatchID)) {
      draft.tls.cert_id = matched.id
    } else if (!matched?.id && draft.tls.cert_id === previousMatchID) {
      draft.tls.cert_id = ''
    }
  } catch (err) {
    if (seq !== certMatchSeq) return
    formError.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (seq === certMatchSeq) {
      certMatchLoading.value = false
    }
  }
}

function toggleTLS(checked: boolean) {
  draft.tls.enabled = checked
  if (!checked) {
    draft.tls.cert_id = ''
    matchingCertID.value = ''
  }
  regenerateTemplate()
  void refreshCertificateMatch()
}

function openSSLSettings() {
  void router.push({ path: '/settings', query: { tab: 'ssl', domain: draft.domain.trim() } })
}

async function inferDefaults() {
  formError.value = ''
  syncRecordNames()
  try {
    const result = await ingressStore.inferDefaults(props.project.id, {
      env_name: draft.source_hint.env_name,
      pipeline_id: draft.source_hint.pipeline_id,
      role: draft.source_hint.role,
      proxy_host_ids: [...draft.proxy.host_ids],
      domain: draft.domain,
      record_type: draft.dns.record_type,
    })
    draft.upstreams = result.upstreams.map(row => ({ ...row }))
    warnings.value = result.warnings ?? []
    regenerateTemplate()
  } catch (err) {
    formError.value = err instanceof Error ? err.message : String(err)
  }
}

async function submitIngress() {
  formError.value = ''
  syncRecordNames()
  const upstreams = cleanUpstreams()
  const records = cleanRecords()
  if (!draft.name.trim()) {
    formError.value = t('overview.ingress.validationName')
    return
  }
  if (!draft.domain.trim()) {
    formError.value = t('overview.ingress.validationDomain')
    return
  }
  if (draft.proxy.host_ids.length === 0) {
    formError.value = t('overview.ingress.validationProxyHost')
    return
  }
  if (upstreams.length === 0) {
    formError.value = t('overview.ingress.validationUpstream')
    return
  }
  if (!draft.proxy_options.raw_template.trim()) {
    formError.value = t('overview.ingress.validationRawTemplate')
    return
  }
  if (draft.tls.enabled && !draft.tls.cert_id) {
    formError.value = t('overview.ingress.validationCertificate')
    return
  }

  saving.value = true
  try {
    const payload: Ingress = {
      id: draft.id,
      project_id: props.project.id,
      name: draft.name.trim(),
      domain: draft.domain.trim(),
      source_hint: { ...draft.source_hint },
      proxy: {
        provider: draft.proxy.provider,
        host_ids: [...draft.proxy.host_ids],
      },
      upstreams,
      proxy_options: {
        websocket: draft.proxy_options.websocket,
        proxy_timeout: draft.proxy_options.proxy_timeout,
        raw_template: draft.proxy_options.raw_template,
      },
      tls: {
        enabled: draft.tls.enabled,
        cert_id: draft.tls.enabled ? draft.tls.cert_id : undefined,
      },
      dns: {
        provider: draft.dns.provider,
        records,
      },
    }
    await ingressStore.saveProjectIngress(props.project.id, payload)
    formOpen.value = false
  } catch (err) {
    formError.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

function cleanUpstreams(): Upstream[] {
  return draft.upstreams
    .filter(row => row.ip.trim() !== '' && row.port !== '')
    .map(row => ({
      host_id: row.host_id,
      ip: row.ip.trim(),
      port: Number(row.port),
    }))
}

function cleanRecords(): DNSRecord[] {
  return draft.dns.records
    .filter(record => record.value.trim() !== '')
    .map(record => ({
      type: record.type || draft.dns.record_type,
      name: record.name.trim() || draft.domain.trim(),
      value: record.value.trim(),
      ttl: Number(record.ttl || 300),
    }))
}

async function previewIngress(ingress: Ingress) {
  if (!ingress.id) return
  try {
    await ingressStore.previewProjectIngress(props.project.id, ingress.id)
    previewIngressId.value = ingress.id
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

async function applyIngress(ingress: Ingress) {
  if (!ingress.id) return
  try {
    await ingressStore.applyProjectIngress(props.project.id, ingress.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

async function detectOrphans(ingress: Ingress) {
  if (!ingress.id) return
  try {
    await ingressStore.detectProjectOrphans(props.project.id, ingress.id)
    orphanIngressId.value = ingress.id
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

async function deleteIngress(ingress: Ingress) {
  if (!ingress.id) return
  try {
    await ingressStore.deleteProjectIngress(props.project.id, ingress.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}
</script>

<template>
  <section class="project-ingress">
    <header class="toolbar">
      <div>
        <h2>{{ t('overview.ingress.title') }}</h2>
      </div>
      <button type="button" class="primary" data-test="project-ingress-add" @click="openCreate">
        + {{ t('overview.ingress.add') }}
      </button>
    </header>

    <div v-if="error" class="error">{{ error }}</div>
    <div v-if="loading" class="empty">{{ t('common.loading') }}</div>
    <div v-else-if="sortedIngresses.length === 0" class="empty">{{ t('overview.ingress.empty') }}</div>
    <table v-else class="ingress-table">
      <thead>
        <tr>
          <th>{{ t('overview.ingress.name') }}</th>
          <th>{{ t('overview.ingress.domain') }}</th>
          <th>{{ t('overview.ingress.upstreams') }}</th>
          <th>{{ t('overview.ingress.proxyHosts') }}</th>
          <th>{{ t('overview.ingress.dns') }}</th>
          <th>{{ t('overview.ingress.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in sortedIngresses" :key="item.id || item.domain" data-test="project-ingress-row">
          <td>{{ item.name }}</td>
          <td class="mono">{{ item.domain }}</td>
          <td>{{ item.upstreams?.length ?? 0 }}</td>
          <td>
            <span v-for="hostID in item.proxy?.host_ids ?? []" :key="hostID" class="chip">
              {{ hostLabel(hostID) }}
            </span>
          </td>
          <td>{{ item.dns.records?.length ?? 0 }} · {{ item.dns.provider }}</td>
          <td class="actions">
            <button type="button" @click="openEdit(item)">{{ t('common.edit') }}</button>
            <button type="button" @click="previewIngress(item)">{{ t('overview.ingress.preview') }}</button>
            <button type="button" @click="applyIngress(item)">{{ t('overview.ingress.apply') }}</button>
            <button type="button" @click="detectOrphans(item)">{{ t('overview.ingress.detectOrphans') }}</button>
            <button type="button" class="danger" @click="deleteIngress(item)">{{ t('common.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

    <section v-if="previewIngressId" class="result-panel">
      <h3>{{ t('overview.ingress.preview') }}</h3>
      <pre>{{ ingressStore.previews[previewIngressId]?.rendered_config_by_host }}</pre>
    </section>

    <section v-if="orphanIngressId" class="result-panel">
      <h3>{{ t('overview.ingress.detectOrphans') }}</h3>
      <pre>{{ ingressStore.orphanReports[orphanIngressId] }}</pre>
    </section>

    <div v-if="formOpen" class="modal-backdrop" @click.self="formOpen = false">
      <section class="modal">
        <header class="modal-header">
          <h2>{{ draft.id ? t('overview.ingress.edit') : t('overview.ingress.add') }}</h2>
          <button type="button" class="icon-btn" @click="formOpen = false">×</button>
        </header>

        <div v-if="formError" class="error">{{ formError }}</div>
        <div class="architecture-hint" data-test="ingress-architecture-hint">
          {{ t('overview.ingress.architectureHint') }}
        </div>

        <div class="form-grid">
          <label>
            <span>{{ t('overview.ingress.name') }}</span>
            <input v-model="draft.name" data-test="ingress-name" />
          </label>
          <label>
            <span>{{ t('overview.ingress.domain') }}</span>
            <input
              :value="draft.domain"
              data-test="ingress-domain"
              placeholder="api.example.com"
              @input="draft.domain = ($event.target as HTMLInputElement).value; syncRecordNames(); regenerateTemplate(); void refreshCertificateMatch()"
            />
          </label>
        </div>

        <section class="form-section">
          <h3>{{ t('overview.ingress.proxy') }} / {{ t('overview.ingress.dns') }}</h3>
          <div class="proxy-control-grid">
            <label>
              <span>{{ t('overview.ingress.proxyProvider') }}</span>
              <select v-model="draft.proxy.provider" data-test="proxy-provider">
                <option value="nginx">nginx</option>
              </select>
            </label>
            <div class="nginx-options inline-nginx-options">
              <div class="tls-option-group">
                <label class="inline-check">
                  <input
                    data-test="ingress-tls-enabled"
                    type="checkbox"
                    :checked="draft.tls.enabled"
                    @change="toggleTLS(($event.target as HTMLInputElement).checked)"
                  />
                  {{ t('overview.ingress.tls') }}
                </label>
                <label v-if="draft.tls.enabled">
                  <span>{{ t('overview.ingress.certificate') }}</span>
                  <select v-model="draft.tls.cert_id" data-test="ingress-cert-select">
                    <option value="">{{ t('overview.ingress.selectCertificate') }}</option>
                    <option v-for="cert in activeCertificates" :key="cert.id" :value="cert.id">
                      {{ certLabel(cert) }}{{ cert.id === matchingCertID ? ` (${t('overview.ingress.autoMatched')})` : '' }}
                    </option>
                  </select>
                </label>
              </div>
              <div class="runtime-option-group">
                <label class="inline-check">
                  <input v-model="draft.proxy_options.websocket" type="checkbox" @change="regenerateTemplate" />
                  {{ t('overview.ingress.websocket') }}
                </label>
                <label>
                  <span>{{ t('overview.ingress.timeout') }}</span>
                  <input
                    v-model="draft.proxy_options.proxy_timeout"
                    data-test="proxy-timeout"
                    @input="regenerateTemplate"
                  />
                </label>
              </div>
            </div>
          </div>
          <div
            v-if="draft.tls.enabled && !draft.tls.cert_id && !matchingCertID && !certMatchLoading"
            class="warning"
            data-test="ingress-cert-missing"
          >
            {{ t('overview.ingress.noMatchingCertificate') }}
            <button type="button" class="secondary" data-test="ingress-cert-request" @click="openSSLSettings">
              {{ t('overview.ingress.requestCertificate') }}
            </button>
          </div>

          <div class="form-grid three dns-settings">
            <label>
              <span>{{ t('overview.ingress.dnsProvider') }}</span>
              <select v-model="draft.dns.provider" data-test="ingress-dns-provider">
                <option v-for="provider in dnsProviderOptions" :key="provider.id" :value="provider.id">
                  {{ provider.name }} · {{ provider.type }}
                </option>
              </select>
            </label>
            <label>
              <span>{{ t('overview.ingress.recordType') }}</span>
              <select
                :value="draft.dns.record_type"
                data-test="dns-record-type"
                @change="setRecordType(($event.target as HTMLSelectElement).value as RecordType)"
              >
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
              </select>
            </label>
            <label>
              <span>{{ t('overview.ingress.ttl') }}</span>
              <input
                type="number"
                min="1"
                :value="draft.dns.ttl"
                @input="setDNSTTL(($event.target as HTMLInputElement).value)"
              />
            </label>
          </div>

          <div class="selector-field">
            <span class="field-title">{{ t('overview.ingress.proxyHosts') }}</span>
            <button
              type="button"
              class="selector-trigger"
              data-test="proxy-host-selector"
              @click="proxySelectorOpen = !proxySelectorOpen"
            >
              <span v-if="selectedProxyHosts.length === 0">{{ t('overview.ingress.selectProxyHosts') }}</span>
              <span v-else>
                {{ t('overview.ingress.selectedProxyHosts', { count: selectedProxyHosts.length, total: sortedHosts.length }) }}
              </span>
            </button>
            <div v-if="proxySelectorOpen" class="selector-menu" data-test="proxy-host-menu">
              <input
                v-model="proxyHostQuery"
                data-test="proxy-host-search"
                :placeholder="t('overview.ingress.searchHosts')"
              />
              <div class="selector-filters">
                <button type="button" :class="{ active: proxyHostFilter === 'public' }" @click="proxyHostFilter = 'public'">
                  {{ t('overview.ingress.publicHosts') }}
                </button>
                <button type="button" :class="{ active: proxyHostFilter === 'selected' }" @click="proxyHostFilter = 'selected'">
                  {{ t('overview.ingress.selectedOnly') }}
                </button>
                <button type="button" :class="{ active: proxyHostFilter === 'all' }" @click="proxyHostFilter = 'all'">
                  {{ t('overview.ingress.allHosts') }}
                </button>
              </div>
              <div class="selector-options">
                <label v-for="host in filteredProxyHosts" :key="host.id" class="selector-option">
                  <input
                    type="checkbox"
                    :data-test="`proxy-host-${host.id}`"
                    :checked="isProxyHostSelected(host.id)"
                    @change="toggleProxyHost(host.id, ($event.target as HTMLInputElement).checked)"
                  />
                  <span>{{ host.name }}</span>
                  <small>{{ hostAddress(host) }}</small>
                </label>
              </div>
              <div class="selector-footer" data-test="proxy-selected-count">
                {{ t('overview.ingress.selectedProxyHosts', { count: selectedProxyHosts.length, total: sortedHosts.length }) }}
              </div>
            </div>
          </div>

          <div v-if="draft.dns.records.length === 0" class="empty-inline" data-test="dns-record-empty">
            {{ t('overview.ingress.dnsRecordsEmpty') }}
          </div>
          <div v-else class="rows dns-records">
            <div v-for="(record, index) in draft.dns.records" :key="index" class="record-row">
              <input
                :data-test="`dns-record-value-${index}`"
                v-model="record.value"
                :placeholder="t('overview.ingress.recordValue')"
              />
              <button type="button" class="danger" @click="removeDNSRecord(index)">{{ t('common.delete') }}</button>
            </div>
          </div>
          <button type="button" class="secondary" data-test="dns-record-add" @click="addDNSRecord">
            + {{ t('overview.ingress.addRecord') }}
          </button>
        </section>

        <section class="form-section">
          <h3>{{ t('overview.ingress.upstreams') }}</h3>
          <div class="upstream-sources">
            <div class="source-panel">
              <h4>{{ t('overview.ingress.selectFromHosts') }}</h4>
              <input v-model="upstreamHostQuery" :placeholder="t('overview.ingress.searchHosts')" />
              <div class="compact-host-grid">
                <label v-for="host in filteredUpstreamHosts" :key="host.id" class="host-card">
                  <input
                    type="checkbox"
                    :checked="isUpstreamHostSelected(host.id)"
                    @change="toggleUpstreamHost(host.id, ($event.target as HTMLInputElement).checked)"
                  />
                  <span>{{ host.name }}</span>
                  <small>{{ privateAddressForHost(host) }}</small>
                </label>
              </div>
            </div>
            <div class="source-panel import-panel" data-test="upstream-import">
              <h4>{{ t('overview.ingress.importFromPipeline') }}</h4>
              <div class="import-controls">
                <label>
                  <span>{{ t('overview.ingress.env') }}</span>
                  <select v-model="draft.source_hint.env_name" data-test="source-env">
                    <option v-for="env in project.environments ?? []" :key="env.name" :value="env.name">{{ env.name }}</option>
                  </select>
                </label>
                <label>
                  <span>{{ t('overview.ingress.pipeline') }}</span>
                  <select v-model="draft.source_hint.pipeline_id" data-test="source-pipeline">
                    <option v-for="pipeline in project.pipelines ?? []" :key="pipeline.id" :value="pipeline.id">
                      {{ pipeline.name || pipeline.id }}
                    </option>
                  </select>
                </label>
                <label>
                  <span>{{ t('overview.ingress.role') }}</span>
                  <select v-model="draft.source_hint.role" data-test="source-role">
                    <option v-for="role in roleOptions" :key="role" :value="role">{{ role }}</option>
                  </select>
                </label>
                <button type="button" class="primary import-action" data-test="ingress-infer" @click="inferDefaults">
                  {{ t('overview.ingress.infer') }}
                </button>
              </div>
            </div>
          </div>
          <div v-if="warnings.length > 0" class="warning upstream-warning" data-test="upstream-import-warnings">
            <strong>{{ t('overview.ingress.warnings') }}</strong>
            <div v-for="warning in warnings" :key="warning">{{ warning }}</div>
          </div>
          <div v-if="draft.upstreams.length === 0" class="empty-inline">
            {{ t('overview.ingress.upstreamsEmpty') }}
          </div>
          <div v-else class="upstream-card-grid">
            <div v-for="(row, index) in draft.upstreams" :key="index" class="upstream-card-row">
              <div class="upstream-node-label">
                <span>{{ t('overview.ingress.host') }}</span>
                <strong>{{ row.host_id ? hostLabel(row.host_id) : t('overview.ingress.manualUpstream') }}</strong>
              </div>
              <label>
                <span>{{ t('overview.ingress.ip') }}</span>
                <input
                  :data-test="`upstream-ip-${index}`"
                  :value="row.ip"
                  :placeholder="t('overview.ingress.ip')"
                  @input="setUpstreamIP(index, ($event.target as HTMLInputElement).value)"
                />
              </label>
              <label>
                <span>{{ t('overview.ingress.port') }}</span>
                <input
                  :data-test="`upstream-port-${index}`"
                  class="port-input"
                  :value="row.port"
                  type="number"
                  min="1"
                  :placeholder="t('overview.ingress.port')"
                  @input="setUpstreamPort(index, ($event.target as HTMLInputElement).value)"
                />
              </label>
              <button type="button" class="danger" @click="removeUpstream(index)">{{ t('common.delete') }}</button>
            </div>
          </div>
          <button type="button" class="secondary" data-test="upstream-add" @click="addUpstream">
            + {{ t('overview.ingress.addUpstream') }}
          </button>
        </section>

        <section class="form-section">
          <h3>{{ t('overview.ingress.rawTemplate') }}</h3>
          <label class="raw-template">
            <span>{{ t('overview.ingress.rawTemplate') }}</span>
            <textarea v-model="draft.proxy_options.raw_template" data-test="nginx-raw-template" rows="12" />
          </label>
        </section>

        <footer class="modal-actions">
          <button type="button" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary" data-test="ingress-save" :disabled="saving" @click="submitIngress">
            {{ t('common.save') }}
          </button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.project-ingress {
  height: calc(100vh - 65px);
  overflow: auto;
  padding: 18px 20px 28px;
}
.toolbar,
.modal-header,
.modal-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.toolbar {
  margin-bottom: 14px;
}
h2,
h3,
h4 {
  margin: 0;
}
h2 {
  font-size: 18px;
}
h3 {
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 700;
}
h4 {
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 700;
}
.empty,
.empty-inline,
.error,
.warning,
.result-panel {
  padding: 10px 12px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 12px;
}
.error {
  color: var(--status-failed);
  border-color: rgba(248, 81, 73, 0.35);
  background: rgba(248, 81, 73, 0.1);
}
.warning {
  color: var(--text-primary);
  border-color: rgba(230, 162, 60, 0.4);
  background: rgba(230, 162, 60, 0.12);
}
.empty-inline {
  margin-top: 10px;
  border-radius: 6px;
}
.architecture-hint {
  margin-top: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}
.ingress-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.ingress-table th,
.ingress-table td {
  padding: 8px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
  vertical-align: top;
}
.ingress-table th {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 600;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.chip {
  display: inline-flex;
  margin: 0 4px 4px 0;
  padding: 2px 6px;
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  color: var(--text-secondary);
}
.mono,
pre,
textarea {
  font-family: var(--font-mono, monospace);
}
button {
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.primary {
  border-color: var(--accent);
  background: var(--accent);
  color: #fff;
}
.secondary {
  margin-top: 8px;
}
.danger {
  color: var(--status-failed);
}
.icon-btn {
  width: 28px;
  padding: 0;
  font-size: 18px;
}
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.62);
}
.modal {
  width: min(1160px, 100%);
  max-height: calc(100vh - 48px);
  overflow: auto;
  padding: 18px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
  box-shadow: 0 18px 60px rgba(0, 0, 0, 0.4);
}
.form-section {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--border-secondary);
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 10px;
}
.form-grid.three {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.proxy-control-grid {
  display: grid;
  grid-template-columns: minmax(200px, 280px) minmax(0, 1fr);
  gap: 12px;
  margin-top: 10px;
}
.dns-settings {
  margin-top: 12px;
}
.import-controls {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
  align-items: end;
  gap: 10px;
}
.import-action {
  margin-top: 0;
}
.upstream-warning {
  margin-top: 10px;
}
label {
  display: grid;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 12px;
}
.field-title {
  display: block;
  margin-bottom: 5px;
  color: var(--text-secondary);
  font-size: 12px;
}
.selector-field {
  position: relative;
  margin-top: 12px;
}
.selector-trigger {
  display: flex;
  width: min(100%, 420px);
  align-items: center;
  justify-content: space-between;
  text-align: left;
}
.selector-menu {
  position: absolute;
  z-index: 5;
  display: grid;
  width: min(520px, 100%);
  gap: 8px;
  margin-top: 6px;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  box-shadow: 0 14px 34px rgba(0, 0, 0, 0.36);
}
.selector-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.selector-filters button.active {
  border-color: var(--accent);
  color: var(--accent);
}
.selector-options {
  display: grid;
  max-height: 220px;
  overflow: auto;
  gap: 4px;
}
.selector-option {
  grid-template-columns: auto minmax(120px, max-content) minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  min-height: 30px;
  padding: 4px 6px;
  border-radius: 5px;
}
.selector-option:hover {
  background: var(--bg-secondary);
}
.selector-option input,
.host-card input {
  width: auto;
  height: auto;
}
.selector-option small,
.host-card small {
  overflow: hidden;
  color: var(--text-tertiary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.selector-footer {
  color: var(--text-tertiary);
  font-size: 11px;
}
input,
select,
textarea {
  width: 100%;
  min-width: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 12px;
}
input,
select {
  height: 30px;
  padding: 0 8px;
}
textarea {
  resize: vertical;
  padding: 8px;
  line-height: 1.45;
}
.rows,
.dns-records {
  display: grid;
  gap: 8px;
}
.inline-check input {
  width: auto;
  height: auto;
}
.record-row {
  display: grid;
  align-items: center;
  gap: 8px;
}
.record-row {
  grid-template-columns: minmax(0, 1fr) auto;
}
.upstream-sources {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 0.9fr);
  gap: 12px;
  margin-top: 10px;
}
.source-panel {
  display: grid;
  align-content: start;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
}
.compact-host-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  max-height: 180px;
  overflow: auto;
}
.host-card {
  grid-template-columns: auto minmax(92px, 0.8fr) minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 6px 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
}
.upstream-card-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-top: 10px;
}
.upstream-card-row {
  display: grid;
  grid-template-columns: minmax(90px, 0.8fr) minmax(120px, 1fr) 92px auto;
  align-items: end;
  gap: 8px;
  padding: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
}
.upstream-node-label {
  display: grid;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 12px;
}
.upstream-node-label strong {
  display: flex;
  align-items: center;
  min-height: 30px;
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.port-input {
  text-align: right;
}
.raw-template {
  margin-top: 10px;
}
.inline-check {
  display: flex;
  align-items: center;
  gap: 8px;
}
.nginx-options {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 0.9fr);
  gap: 12px;
  margin-top: 10px;
}
.inline-nginx-options {
  margin-top: 0;
}
.tls-option-group,
.runtime-option-group {
  display: grid;
  grid-template-columns: minmax(150px, max-content) minmax(220px, 1fr);
  align-items: center;
  gap: 10px;
}
.runtime-option-group {
  grid-template-columns: minmax(150px, max-content) minmax(220px, 360px);
}
.result-panel {
  margin-top: 12px;
}
.result-panel pre {
  margin: 8px 0 0;
  overflow: auto;
  white-space: pre-wrap;
}
@media (max-width: 760px) {
  .project-ingress {
    padding: 14px;
  }
	  .form-grid,
	  .form-grid.three,
  .proxy-control-grid,
	  .import-controls,
  .nginx-options,
  .tls-option-group,
  .runtime-option-group,
  .record-row,
  .upstream-sources,
  .compact-host-grid,
  .upstream-card-grid,
  .upstream-card-row {
	    grid-template-columns: 1fr;
	  }
  .selector-menu,
  .selector-trigger {
    width: 100%;
  }
	}
</style>
