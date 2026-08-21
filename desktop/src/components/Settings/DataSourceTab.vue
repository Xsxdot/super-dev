<!--
数据源设置页。

职责：
  - 展示 PG 权限徽标、Redis db 占用图和活跃临时资源
  - 提供数据源 CRUD、租约手动回收与实例对账入口

边界：
  - 不展示密码或明文 DSN；连接凭据只在数据源表单写入请求中短暂存在
  - 不执行供给、审批或回收业务，全部委托 datasources store/agent
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DataSourceFormModal from '@/components/Settings/DataSourceFormModal.vue'
import { useDataSourceStore } from '@/stores/datasources'
import type { DataSource, ReconcileReport } from '@/api/datasources'

const { t } = useI18n()
const store = useDataSourceStore()
const formOpen = ref(false)
const editingSource = ref<DataSource | null>(null)
const reconcileResult = ref<ReconcileReport | null>(null)
const actionError = ref('')

const postgresSources = computed(() => store.sources.filter(source => source.kind === 'postgres'))
const redisSources = computed(() => store.sources.filter(source => source.kind === 'redis'))

onMounted(() => {
  void store.load()
})

function openCreate() {
  editingSource.value = null
  formOpen.value = true
}

function openEdit(source: DataSource) {
  editingSource.value = source
  formOpen.value = true
}

function sourceCapabilities(source: DataSource): Array<{ name: string; ok: boolean }> {
  return ['createdb', 'createrole', 'pg_signal_backend'].map(name => ({
    name: name === 'createdb' ? 'CREATEDB' : name === 'createrole' ? 'CREATEROLE' : name,
    ok: source.probe.capabilities?.[name] === true,
  }))
}

function redisTotal(source: DataSource): number {
  const total = Number(source.probe.facts?.databases ?? 16)
  return Number.isFinite(total) && total > 1 ? Math.min(64, Math.floor(total)) : 16
}

function dbSet(source: DataSource): Set<number> {
  return new Set((source.probe.facts?.occupied_dbs ?? '').split(',').map(value => Number(value.trim())).filter(value => Number.isInteger(value)))
}

function leaseDBSet(): Set<number> {
  const values = store.activeLeases.flatMap(lease => lease.resources
    .filter(resource => resource.kind === 'redis')
    .map(resource => Number(resource.meta?.db_index ?? resource.name.replace(/^db/, ''))))
  return new Set(values.filter(value => Number.isInteger(value)))
}

function dbClass(source: DataSource, index: number): string[] {
  if (index === 0) return ['reserved']
  if (leaseDBSet().has(index)) return ['lease']
  if (dbSet(source).has(index)) return ['occupied']
  return ['free']
}

function formatExpiry(expiresAt: string): string {
  const date = new Date(expiresAt)
  if (Number.isNaN(date.getTime())) return expiresAt
  return date.toLocaleString()
}

function resourceSummary(lease: { resources: Array<{ kind: string; name: string }> }): string {
  return lease.resources.map(resource => `${resource.kind}:${resource.name}`).join(', ')
}

async function reclaim(leaseId: string) {
  actionError.value = ''
  try {
    await store.reclaim(leaseId)
    await store.leases()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

async function reconcile() {
  actionError.value = ''
  try {
    reconcileResult.value = await store.reconcile()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  }
}

function onSaved() {
  formOpen.value = false
  editingSource.value = null
  void store.load()
}
</script>

<template>
  <section class="datasource-tab" data-test="datasource-tab">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.dataSources.title') }}</h1>
        <p class="settings-pane-description">{{ t('settings.dataSources.description') }}</p>
      </div>
      <button type="button" class="settings-btn settings-btn-primary" data-test="datasource-add" @click="openCreate">
        + {{ t('settings.dataSources.add') }}
      </button>
    </header>

    <p v-if="store.error || actionError" class="settings-alert settings-alert-danger" role="alert">{{ actionError || store.error }}</p>

    <section class="settings-card-list datasource-card-list">
      <article v-for="source in postgresSources" :key="source.id" class="settings-card datasource-card" :data-test="`datasource-card-${source.id}`">
        <header class="settings-card-header">
          <div>
            <h2>{{ source.name }}</h2>
            <p>{{ source.host }}:{{ source.port }} · PostgreSQL</p>
          </div>
          <button class="settings-btn settings-btn-secondary" type="button" :data-test="`datasource-edit-${source.id}`" @click="openEdit(source)">{{ t('common.edit') }}</button>
        </header>
        <div class="datasource-capabilities" data-test="postgres-capabilities">
          <span v-for="capability in sourceCapabilities(source)" :key="capability.name" class="settings-badge" :class="capability.ok ? 'badge-ok' : 'badge-failed'">
            {{ capability.name }}
          </span>
        </div>
        <p class="datasource-hint">{{ source.probe.ok ? t('settings.dataSources.probeReady') : (source.probe.fix_hint || source.probe.error) }}</p>
      </article>

      <article v-for="source in redisSources" :key="source.id" class="settings-card datasource-card" :data-test="`datasource-card-${source.id}`">
        <header class="settings-card-header">
          <div>
            <h2>{{ source.name }}</h2>
            <p>{{ source.host }}:{{ source.port }} · Redis</p>
          </div>
          <button class="settings-btn settings-btn-secondary" type="button" :data-test="`datasource-edit-${source.id}`" @click="openEdit(source)">{{ t('common.edit') }}</button>
        </header>
        <div class="redis-grid" :data-test="`redis-grid-${source.id}`">
          <span
            v-for="index in redisTotal(source)"
            :key="index - 1"
            class="redis-db-cell"
            :class="dbClass(source, index - 1)"
            :data-test="`redis-db-cell-${index - 1}`"
            :title="index - 1 === 0 ? t('settings.dataSources.dbReserved') : undefined"
          >
            {{ index - 1 }}
          </span>
        </div>
        <div class="redis-legend">
          <span><i class="free" />{{ t('settings.dataSources.dbFree') }}</span>
          <span><i class="occupied" />{{ t('settings.dataSources.dbOccupied') }}</span>
          <span><i class="lease" />{{ t('settings.dataSources.dbLease') }}</span>
          <span><i class="reserved" />{{ t('settings.dataSources.dbReserved') }}</span>
        </div>
      </article>
    </section>

    <section class="settings-card datasource-leases" data-test="active-leases">
      <header class="settings-card-header">
        <div>
          <h2>{{ t('settings.dataSources.activeLeases') }}</h2>
          <p>{{ t('settings.dataSources.activeLeasesDescription') }}</p>
        </div>
        <button class="settings-btn settings-btn-secondary" type="button" data-test="datasource-reconcile" :disabled="store.loading" @click="reconcile">
          {{ t('settings.dataSources.reconcile') }}
        </button>
      </header>
      <div v-if="store.activeLeases.length === 0" class="settings-empty">{{ t('settings.dataSources.noLeases') }}</div>
      <div v-else class="settings-table datasource-lease-table">
        <div v-for="item in store.activeLeases" :key="item.id" class="datasource-lease-row" :data-test="`lease-row-${item.id}`">
          <div>
            <strong>{{ item.purpose }}</strong>
            <span>{{ resourceSummary(item) }}</span>
          </div>
          <time :datetime="item.expires_at">{{ t('settings.dataSources.expiresAt') }} {{ formatExpiry(item.expires_at) }}</time>
          <button class="settings-btn settings-btn-danger" type="button" :data-test="`reclaim-lease-${item.id}`" @click="reclaim(item.id)">{{ t('settings.dataSources.reclaim') }}</button>
        </div>
      </div>
      <p v-if="reconcileResult" class="settings-alert settings-alert-success" data-test="reconcile-result">
        {{ t('settings.dataSources.reconcileResult', { count: reconcileResult.expired_reclaimed }) }}
        <span v-for="orphan in reconcileResult.orphans_reclaimed" :key="orphan.name"> {{ orphan.name }}</span>
      </p>
    </section>

    <DataSourceFormModal :open="formOpen" :source="editingSource" @close="formOpen = false" @saved="onSaved" />
  </section>
</template>

<style scoped>
.datasource-card-list { gap: 12px; }
.datasource-card { display: grid; gap: 12px; }
.datasource-card h2 { margin: 0; font-size: 14px; }
.datasource-card p { margin: 4px 0 0; color: var(--text-tertiary); font-size: 11px; }
.datasource-capabilities { display: flex; flex-wrap: wrap; gap: 6px; }
.badge-ok { color: var(--success, #2d9d61); border-color: var(--success, #2d9d61); }
.badge-failed { color: var(--danger, #c94a4a); border-color: var(--danger, #c94a4a); }
.datasource-hint { min-height: 16px; }
.redis-grid { display: grid; grid-template-columns: repeat(8, minmax(28px, 1fr)); gap: 6px; }
.redis-db-cell { display: grid; place-items: center; min-height: 28px; border: 1px solid var(--border-secondary); border-radius: 4px; color: var(--text-tertiary); font-size: 10px; }
.redis-db-cell.free, .redis-legend .free { background: color-mix(in srgb, var(--success, #2d9d61) 12%, transparent); }
.redis-db-cell.occupied, .redis-legend .occupied { background: color-mix(in srgb, var(--warning, #d28b32) 18%, transparent); color: var(--warning, #d28b32); }
.redis-db-cell.lease, .redis-legend .lease { background: color-mix(in srgb, var(--accent, #6e65d8) 18%, transparent); color: var(--accent, #6e65d8); }
.redis-db-cell.reserved, .redis-legend .reserved { background: var(--bg-overlay); color: var(--text-secondary); }
.redis-legend { display: flex; flex-wrap: wrap; gap: 10px; color: var(--text-tertiary); font-size: 10px; }
.redis-legend span { display: inline-flex; align-items: center; gap: 4px; }
.redis-legend i { width: 9px; height: 9px; border: 1px solid var(--border-secondary); border-radius: 2px; }
.datasource-leases { margin-top: 14px; }
.datasource-lease-table { padding: 0; }
.datasource-lease-row { display: grid; grid-template-columns: minmax(0, 1fr) max-content max-content; align-items: center; gap: 14px; min-height: 52px; padding: 8px 12px; border-bottom: 1px solid var(--border-secondary); }
.datasource-lease-row:last-child { border-bottom: 0; }
.datasource-lease-row > div { display: grid; gap: 4px; min-width: 0; }
.datasource-lease-row span, .datasource-lease-row time { color: var(--text-tertiary); font-size: 11px; }
@media (max-width: 720px) {
  .datasource-lease-row { grid-template-columns: 1fr; gap: 6px; }
}
</style>
