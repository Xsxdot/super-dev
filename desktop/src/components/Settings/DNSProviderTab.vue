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
const editingProviderID = ref<string | null>(null)
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
const isEditing = computed(() => editingProviderID.value !== null)

function providerDetail(provider: DNSProviderConfig) {
  if (provider.type === 'cloudflare') return provider.zone_id || t('settings.dnsProviders.zoneAuto')
  if (provider.type === 'aliyun') return t('settings.dnsProviders.noZoneRequired')
  return '-'
}

onMounted(async () => {
  try {
    await store.loadDNSProviders()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
})

function openCreate() {
  editingProviderID.value = null
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

function openEdit(provider: DNSProviderConfig) {
  editingProviderID.value = provider.id ?? provider.name
  Object.assign(draft, {
    id: provider.id ?? '',
    name: provider.name,
    type: provider.type,
    zone_id: provider.zone_id ?? '',
    api_token: provider.secrets?.api_token ?? '',
    access_key_id: provider.secrets?.access_key_id ?? '',
    access_key_secret: provider.secrets?.access_key_secret ?? '',
  })
  error.value = ''
  formOpen.value = true
}

function draftSecrets(): Record<string, string> | undefined {
  const entries = draft.type === 'cloudflare'
    ? [['api_token', draft.api_token]]
    : [
        ['access_key_id', draft.access_key_id],
        ['access_key_secret', draft.access_key_secret],
      ]
  const secrets = Object.fromEntries(
    entries
      .map(([key, value]) => [key, value.trim()] as const)
      .filter(([, value]) => value),
  )
  return Object.keys(secrets).length > 0 ? secrets : undefined
}

async function submitProvider() {
  if (!draft.name.trim()) {
    error.value = t('settings.dnsProviders.nameRequired')
    return
  }
  saving.value = true
  error.value = ''
  try {
    const payload: DNSProviderConfig = {
      id: draft.id.trim() || undefined,
      name: draft.name.trim(),
      type: draft.type,
    }
    if (draft.type === 'cloudflare' && draft.zone_id.trim()) payload.zone_id = draft.zone_id.trim()
    const secrets = draftSecrets()
    if (secrets) payload.secrets = secrets
    await store.saveDNSProvider(payload)
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
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.dnsProviders.title') }}</h1>
      </div>
      <button type="button" class="settings-btn settings-btn-primary" data-test="dns-provider-add" @click="openCreate">
        + {{ t('settings.dnsProviders.add') }}
      </button>
    </header>

    <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>

    <div class="settings-surface settings-surface-scroll">
      <table class="settings-table provider-table">
        <thead>
          <tr>
            <th>{{ t('settings.dnsProviders.name') }}</th>
            <th>{{ t('settings.dnsProviders.type') }}</th>
            <th>{{ t('settings.dnsProviders.details') }}</th>
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
            <td class="mono">{{ providerDetail(provider) }}</td>
            <td class="actions">
              <button
                type="button"
                class="settings-btn settings-btn-text"
                :data-test="`dns-provider-edit-${provider.id || provider.name}`"
                @click="openEdit(provider)"
              >
                {{ t('common.edit') }}
              </button>
              <button type="button" class="settings-btn settings-btn-text settings-btn-danger" @click="deleteProvider(provider)">
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
          <h2 class="settings-modal-title">{{ isEditing ? t('common.edit') : t('settings.dnsProviders.add') }}</h2>
          <button type="button" class="settings-btn settings-btn-icon settings-btn-ghost" @click="formOpen = false">×</button>
        </header>

        <div class="settings-modal-body">
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.dnsProviders.id') }}</span>
            <input v-model="draft.id" class="settings-input" data-test="dns-provider-id" placeholder="cloudflare-prod" :disabled="isEditing" />
          </label>
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.dnsProviders.name') }}</span>
            <input v-model="draft.name" class="settings-input" data-test="dns-provider-name" />
          </label>
          <label class="settings-field">
            <span class="settings-field-label">{{ t('settings.dnsProviders.type') }}</span>
            <select v-model="draft.type" class="settings-select" data-test="dns-provider-type">
              <option value="cloudflare">Cloudflare</option>
              <option value="aliyun">Aliyun</option>
            </select>
          </label>
          <label v-if="draft.type === 'cloudflare'" class="settings-field">
            <span class="settings-field-label">{{ t('settings.dnsProviders.zoneIDOptional') }}</span>
            <input v-model="draft.zone_id" class="settings-input" data-test="dns-provider-zone" />
          </label>
          <label v-if="draft.type === 'cloudflare'" class="settings-field">
            <span class="settings-field-label">{{ t('settings.dnsProviders.apiToken') }}</span>
            <input v-model="draft.api_token" class="settings-input" type="password" data-test="dns-provider-token" />
          </label>
          <template v-else>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.dnsProviders.accessKeyID') }}</span>
              <input v-model="draft.access_key_id" class="settings-input" data-test="dns-provider-access-key-id" />
            </label>
            <label class="settings-field">
              <span class="settings-field-label">{{ t('settings.dnsProviders.accessKeySecret') }}</span>
              <input v-model="draft.access_key_secret" class="settings-input" type="password" data-test="dns-provider-access-key-secret" />
            </label>
          </template>
        </div>

        <footer class="settings-modal-footer">
          <button type="button" class="settings-btn" @click="formOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="settings-btn settings-btn-primary" :disabled="saving" data-test="dns-provider-save" @click="submitProvider">
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
.mono {
  font-family: var(--font-mono, monospace);
}
.settings-modal-body {
  display: grid;
  gap: 10px;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  text-align: right;
}
</style>
