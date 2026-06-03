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
    upstreams: [{ ip: '', port: '' }],
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
      records: [{ type: 'A', name: '', value: '', ttl: 300 }],
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
      records: next.dns.records.map(record => ({ ...record })),
    },
  })
}

function openCreate() {
  warnings.value = []
  formError.value = ''
  matchingCertID.value = ''
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
    upstreams: (ingress.upstreams?.length ? ingress.upstreams : [{ ip: '', port: '' }]).map(row => ({ ...row })),
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
      records: (ingress.dns.records?.length ? ingress.dns.records : firstRecord ? [firstRecord] : [])
        .map(record => ({ ...record })),
    },
  })
  if (draft.dns.records.length === 0) addDNSRecord()
  formOpen.value = true
  void refreshCertificateMatch()
}

function hostLabel(hostID: string): string {
  const host = remoteStore.hostById(hostID)
  return host?.name ?? hostID
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
}

function addUpstream() {
  draft.upstreams.push({ ip: '', port: '' })
  regenerateTemplate()
}

function removeUpstream(index: number) {
  draft.upstreams.splice(index, 1)
  if (draft.upstreams.length === 0) addUpstream()
  regenerateTemplate()
}

function setUpstreamIP(index: number, value: string) {
  draft.upstreams[index].ip = value
  regenerateTemplate()
}

function setUpstreamPort(index: number, value: string) {
  draft.upstreams[index].port = value.trim() === '' ? '' : Number(value)
  regenerateTemplate()
}

function addDNSRecord() {
  draft.dns.records.push({
    type: draft.dns.record_type,
    name: draft.domain,
    value: '',
    ttl: 300,
  })
}

function removeDNSRecord(index: number) {
  draft.dns.records.splice(index, 1)
  if (draft.dns.records.length === 0) addDNSRecord()
}

function setRecordType(value: RecordType) {
  draft.dns.record_type = value
  for (const record of draft.dns.records) {
    record.type = value
  }
}

function syncRecordNames() {
  for (const record of draft.dns.records) {
    record.name = draft.domain
  }
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
    draft.dns.records = result.dns_records.map(record => ({ ...record }))
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
        <div v-if="warnings.length > 0" class="warning">
          <strong>{{ t('overview.ingress.warnings') }}</strong>
          <div v-for="warning in warnings" :key="warning">{{ warning }}</div>
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
          <h3>{{ t('overview.ingress.source') }}</h3>
          <div class="form-grid three">
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
          </div>
        </section>

        <section class="form-section">
          <h3>{{ t('overview.ingress.proxy') }}</h3>
          <div class="form-grid">
            <label>
              <span>{{ t('overview.ingress.proxyProvider') }}</span>
              <select v-model="draft.proxy.provider" data-test="proxy-provider">
                <option value="nginx">nginx</option>
              </select>
            </label>
            <div>
              <span class="field-title">{{ t('overview.ingress.proxyHosts') }}</span>
              <div class="host-list">
                <label v-for="host in sortedHosts" :key="host.id" class="check-row">
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
            </div>
          </div>
          <button type="button" class="secondary" data-test="ingress-infer" @click="inferDefaults">
            {{ t('overview.ingress.infer') }}
          </button>
        </section>

        <section class="form-section">
          <h3>{{ t('overview.ingress.upstreams') }}</h3>
          <div class="rows">
            <div v-for="(row, index) in draft.upstreams" :key="index" class="upstream-row">
              <input
                :data-test="`upstream-ip-${index}`"
                :value="row.ip"
                :placeholder="t('overview.ingress.ip')"
                @input="setUpstreamIP(index, ($event.target as HTMLInputElement).value)"
              />
              <span class="colon">:</span>
              <input
                :data-test="`upstream-port-${index}`"
                class="port-input"
                :value="row.port"
                type="number"
                min="1"
                :placeholder="t('overview.ingress.port')"
                @input="setUpstreamPort(index, ($event.target as HTMLInputElement).value)"
              />
              <button type="button" class="danger" @click="removeUpstream(index)">{{ t('common.delete') }}</button>
            </div>
          </div>
          <button type="button" class="secondary" data-test="upstream-add" @click="addUpstream">
            + {{ t('overview.ingress.addUpstream') }}
          </button>
        </section>

        <section class="form-section">
          <h3>{{ t('overview.ingress.dns') }}</h3>
          <div class="form-grid three">
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
                :value="draft.dns.records[0]?.ttl ?? 300"
                @input="draft.dns.records.forEach(record => { record.ttl = Number(($event.target as HTMLInputElement).value || 300) })"
              />
            </label>
          </div>
          <div class="rows">
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
          <h3>{{ t('overview.ingress.nginx') }}</h3>
          <div class="form-grid three">
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
h3 {
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
.empty,
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
.host-list,
.rows {
  display: grid;
  gap: 8px;
}
.check-row {
  grid-template-columns: auto minmax(90px, max-content) 1fr;
  align-items: center;
  gap: 8px;
}
.check-row input,
.inline-check input {
  width: auto;
  height: auto;
}
.check-row small {
  overflow: hidden;
  color: var(--text-tertiary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.upstream-row,
.record-row {
  display: grid;
  align-items: center;
  gap: 8px;
}
.upstream-row {
  grid-template-columns: minmax(0, 1fr) auto 120px auto;
}
.record-row {
  grid-template-columns: minmax(0, 1fr) auto;
}
.colon {
  color: var(--text-tertiary);
  font-weight: 700;
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
  .upstream-row {
    grid-template-columns: 1fr;
  }
}
</style>
