<!--
DNSProviderTab：设置页全局 DNS Provider 管理。

职责：
  - 展示内置 Manual DNS 和已保存 DNS Provider
  - 新建/删除 Cloudflare、Aliyun Provider 配置
  - 将 Provider 配置交由 ingress store 保存

边界：
  - 不管理项目级 Ingress 声明
  - 不验证 DNS 凭据是否可用
-->
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIngressStore } from '@/stores/ingress'
import type { DNSProviderConfig } from '@/api/ingress'

const store = useIngressStore()
const { t } = useI18n()

const formOpen = ref(false)
const saving = ref(false)
const error = ref('')
const draft = reactive({
  id: '',
  name: '',
  type: 'cloudflare' as DNSProviderConfig['type'],
  zone_id: '',
  api_token: '',
  access_key_id: '',
  access_key_secret: '',
})

const providers = computed(() => store.dnsProviders)

onMounted(async () => {
  try {
    await store.loadDNSProviders()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
})

function openCreate() {
  Object.assign(draft, {
    id: '',
    name: '',
    type: 'cloudflare',
    zone_id: '',
    api_token: '',
    access_key_id: '',
    access_key_secret: '',
  })
  error.value = ''
  formOpen.value = true
}

async function submitProvider() {
  if (!draft.name.trim()) {
    error.value = t('settings.dnsProviders.nameRequired')
    return
  }
  saving.value = true
  error.value = ''
  try {
    const secrets: Record<string, string> = draft.type === 'cloudflare'
      ? { api_token: draft.api_token }
      : { access_key_id: draft.access_key_id, access_key_secret: draft.access_key_secret }
    await store.saveDNSProvider({
      id: draft.id.trim() || undefined,
      name: draft.name.trim(),
      type: draft.type,
      zone_id: draft.zone_id.trim() || undefined,
      secrets,
    })
    formOpen.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function deleteProvider(provider: DNSProviderConfig) {
  if (!provider.id) return
  try {
    await store.deleteDNSProvider(provider.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}
</script>

<template>
  <section class="dns-provider-tab">
    <header class="pane-header">
      <h1>{{ t('settings.dnsProviders.title') }}</h1>
      <button type="button" class="primary-btn" data-test="dns-provider-add" @click="openCreate">
        + {{ t('settings.dnsProviders.add') }}
      </button>
    </header>

    <div v-if="error" class="error">{{ error }}</div>

    <table class="provider-table">
      <thead>
        <tr>
          <th>{{ t('settings.dnsProviders.name') }}</th>
          <th>{{ t('settings.dnsProviders.type') }}</th>
          <th>{{ t('settings.dnsProviders.zoneID') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr data-test="dns-provider-manual">
          <td>{{ t('settings.dnsProviders.manual') }}</td>
          <td>manual</td>
          <td>-</td>
          <td class="actions"></td>
        </tr>
        <tr v-for="provider in providers" :key="provider.id || provider.name" data-test="dns-provider-row">
          <td>{{ provider.name }}</td>
          <td>{{ provider.type }}</td>
          <td class="mono">{{ provider.zone_id || '-' }}</td>
          <td class="actions">
            <button type="button" class="danger" @click="deleteProvider(provider)">
              {{ t('common.delete') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="formOpen" class="modal-backdrop" @click.self="formOpen = false">
      <section class="modal">
        <header class="modal-header">
          <h2>{{ t('settings.dnsProviders.add') }}</h2>
          <button type="button" class="icon-btn" @click="formOpen = false">×</button>
        </header>

        <label>
          <span>{{ t('settings.dnsProviders.id') }}</span>
          <input v-model="draft.id" data-test="dns-provider-id" placeholder="cloudflare-prod" />
        </label>
        <label>
          <span>{{ t('settings.dnsProviders.name') }}</span>
          <input v-model="draft.name" data-test="dns-provider-name" />
        </label>
        <label>
          <span>{{ t('settings.dnsProviders.type') }}</span>
          <select v-model="draft.type" data-test="dns-provider-type">
            <option value="cloudflare">Cloudflare</option>
            <option value="aliyun">Aliyun</option>
          </select>
        </label>
        <label>
          <span>{{ t('settings.dnsProviders.zoneID') }}</span>
          <input v-model="draft.zone_id" data-test="dns-provider-zone" />
        </label>
        <label v-if="draft.type === 'cloudflare'">
          <span>{{ t('settings.dnsProviders.apiToken') }}</span>
          <input v-model="draft.api_token" type="password" data-test="dns-provider-token" />
        </label>
        <template v-else>
          <label>
            <span>{{ t('settings.dnsProviders.accessKeyID') }}</span>
            <input v-model="draft.access_key_id" data-test="dns-provider-access-key-id" />
          </label>
          <label>
            <span>{{ t('settings.dnsProviders.accessKeySecret') }}</span>
            <input v-model="draft.access_key_secret" type="password" data-test="dns-provider-access-key-secret" />
          </label>
        </template>

        <footer>
          <button type="button" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="primary-btn" :disabled="saving" data-test="dns-provider-save" @click="submitProvider">
            {{ t('common.save') }}
          </button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.dns-provider-tab {
  width: 100%;
}
.pane-header,
.modal-header,
footer {
  display: flex;
  align-items: center;
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
.provider-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.provider-table th,
.provider-table td {
  padding: 7px 8px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
}
.provider-table th {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 400;
}
.mono {
  font-family: var(--font-mono, monospace);
}
.actions {
  text-align: right;
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
.icon-btn {
  width: 28px;
  height: 28px;
  padding: 0;
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
  width: min(440px, calc(100vw - 32px));
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
select {
  padding: 6px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
footer {
  justify-content: flex-end;
  margin-top: 16px;
}
button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
