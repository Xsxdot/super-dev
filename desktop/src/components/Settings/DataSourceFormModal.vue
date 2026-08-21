<!--
数据源表单弹窗。

职责：
  - 收集 PG/Redis 管理连接并展示登记即探测的结果
  - 让密码留空时沿用后端 Update 的不改密码语义

边界：
  - 不展示或保存服务端返回的密码
  - 不决定租约、审批和回收策略
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDataSourceStore } from '@/stores/datasources'
import type { DataSource, DataSourceInput, DataSourceProbeResult } from '@/api/datasources'

const props = defineProps<{
  open: boolean
  source?: DataSource | null
}>()
const emit = defineEmits<{ close: []; saved: [DataSource] }>()
const { t } = useI18n()
const store = useDataSourceStore()

const draft = reactive<DataSourceInput>({ kind: 'postgres', name: '', host: '', port: 5432, user: '', password: '', extra: {} })
const saving = ref(false)
const probing = ref(false)
const formError = ref('')
const probeResult = ref<DataSourceProbeResult | null>(null)

const isEditing = computed(() => !!props.source?.id)

function resetDraft(source?: DataSource | null) {
  Object.assign(draft, {
    kind: source?.kind ?? 'postgres',
    name: source?.name ?? '',
    host: source?.host ?? '',
    port: source?.port ?? (source?.kind === 'redis' ? 6379 : 5432),
    user: source?.user ?? '',
    password: '',
    extra: { ...(source?.extra ?? {}) },
  })
  probeResult.value = source?.probe ?? null
  formError.value = ''
}

watch(() => [props.open, props.source], () => {
  if (props.open) resetDraft(props.source)
}, { immediate: true })

function onKindChange() {
  if (!draft.port || draft.port === 5432 || draft.port === 6379) {
    draft.port = draft.kind === 'redis' ? 6379 : 5432
  }
}

function inputPayload(): DataSourceInput {
  return {
    kind: draft.kind,
    name: draft.name.trim(),
    host: draft.host.trim(),
    port: Number(draft.port),
    user: draft.user?.trim() || undefined,
    password: draft.password?.trim() || undefined,
    extra: Object.fromEntries(Object.entries(draft.extra ?? {}).filter(([, value]) => value?.trim())),
  }
}

function showError(error: unknown) {
  formError.value = error instanceof Error ? error.message : String(error)
}

async function save() {
  formError.value = ''
  const payload = inputPayload()
  if (!payload.name || !payload.host || !payload.port) {
    formError.value = t('settings.dataSources.formRequired')
    return
  }
  saving.value = true
  try {
    const saved = isEditing.value && props.source
      ? await store.update(props.source.id, payload)
      : await store.create(payload)
    emit('saved', saved)
  } catch (error) {
    showError(error)
  } finally {
    saving.value = false
  }
}

async function probe() {
  formError.value = ''
  probing.value = true
  try {
    if (isEditing.value && props.source) {
      probeResult.value = await store.probe(props.source.id)
    } else {
      // 新登记必须先写入才能获得可重复探测的 ID；后端 Add 会在落盘前完成 Probe。
      const saved = await store.create(inputPayload())
      probeResult.value = saved.probe
      emit('saved', saved)
    }
  } catch (error) {
    showError(error)
  } finally {
    probing.value = false
  }
}
</script>

<template>
  <div v-if="open" class="settings-modal-backdrop" data-test="datasource-form-backdrop">
    <section class="settings-modal datasource-modal" role="dialog" aria-modal="true" data-test="datasource-form-modal">
      <header class="settings-modal-header">
        <h2>{{ isEditing ? t('settings.dataSources.editTitle') : t('settings.dataSources.addTitle') }}</h2>
        <button class="settings-modal-close" type="button" @click="emit('close')">×</button>
      </header>
      <div class="settings-modal-body">
        <p v-if="formError" class="settings-alert settings-alert-danger" role="alert">{{ formError }}</p>
        <label class="settings-field">
          <span>{{ t('settings.dataSources.type') }}</span>
          <select v-model="draft.kind" data-test="datasource-kind" class="settings-select" @change="onKindChange">
            <option value="postgres">PostgreSQL</option>
            <option value="redis">Redis</option>
          </select>
        </label>
        <label class="settings-field">
          <span>{{ t('settings.dataSources.name') }}</span>
          <input v-model="draft.name" data-test="datasource-name" class="settings-input" type="text">
        </label>
        <div class="datasource-form-grid">
          <label class="settings-field">
            <span>{{ t('settings.dataSources.host') }}</span>
            <input v-model="draft.host" data-test="datasource-host" class="settings-input" type="text">
          </label>
          <label class="settings-field">
            <span>{{ t('settings.dataSources.port') }}</span>
            <input v-model.number="draft.port" data-test="datasource-port" class="settings-input" type="number" min="1" max="65535">
          </label>
        </div>
        <label class="settings-field">
          <span>{{ t('settings.dataSources.user') }}</span>
          <input v-model="draft.user" data-test="datasource-user" class="settings-input" type="text">
        </label>
        <label class="settings-field">
          <span>{{ t('settings.dataSources.password') }}</span>
          <input v-model="draft.password" data-test="datasource-password" class="settings-input" type="password" :placeholder="t('settings.dataSources.passwordPlaceholder')">
        </label>
        <template v-if="draft.kind === 'postgres'">
          <label class="settings-field">
            <span>{{ t('settings.dataSources.maintenanceDb') }}</span>
            <input v-model="draft.extra!.maintenance_db" data-test="datasource-maintenance-db" class="settings-input" type="text" placeholder="postgres">
          </label>
          <label class="settings-field">
            <span>{{ t('settings.dataSources.sslmode') }}</span>
            <select v-model="draft.extra!.sslmode" data-test="datasource-sslmode" class="settings-select">
              <option value="disable">disable</option>
              <option value="require">require</option>
              <option value="verify-full">verify-full</option>
            </select>
          </label>
        </template>
        <section v-if="probeResult" class="datasource-probe-result" data-test="datasource-probe-result">
          <strong>{{ probeResult.ok ? t('settings.dataSources.probeSuccess') : t('settings.dataSources.probeFailed') }}</strong>
          <div v-if="probeResult.capabilities" class="probe-capabilities">
            <span v-for="(ok, capability) in probeResult.capabilities" :key="capability" :class="ok ? 'probe-ok' : 'probe-failed'">
              {{ capability }}: {{ ok ? 'OK' : '✕' }}
            </span>
          </div>
          <p v-if="probeResult.error" class="settings-alert settings-alert-danger">{{ probeResult.error }}</p>
          <pre v-if="probeResult.fix_hint" class="probe-fix-hint">{{ probeResult.fix_hint }}</pre>
        </section>
      </div>
      <footer class="settings-modal-footer">
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="saving || probing" data-test="datasource-probe" @click="probe">
          {{ t('settings.dataSources.probe') }}
        </button>
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="saving || probing" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button class="settings-btn settings-btn-primary" type="button" :disabled="saving || probing" data-test="datasource-save" @click="save">{{ t('common.save') }}</button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.datasource-modal { width: min(560px, calc(100vw - 32px)); }
.settings-modal-body { display: grid; gap: 12px; }
.settings-field { display: grid; gap: 5px; color: var(--text-secondary); font-size: 12px; }
.datasource-form-grid { display: grid; grid-template-columns: 1fr 140px; gap: 10px; }
.datasource-probe-result { display: grid; gap: 8px; padding: 10px; border: 1px solid var(--border-secondary); border-radius: 6px; }
.probe-capabilities { display: flex; flex-wrap: wrap; gap: 6px; font-size: 11px; }
.probe-ok { color: var(--success, #2d9d61); }
.probe-failed { color: var(--danger, #c94a4a); }
.probe-fix-hint { margin: 0; padding: 8px; overflow: auto; background: var(--bg-surface); font-size: 11px; white-space: pre-wrap; }
</style>
