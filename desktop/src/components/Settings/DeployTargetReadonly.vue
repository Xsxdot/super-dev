<!--
DeployTargetReadonly：流水线级部署目标只读矩阵。

职责：
  - 按环境展示当前流水线的部署目标机器
  - 对多节点环境做摘要展示，并保留完整目标列表

边界：
  - 只读，不在流水线表单中编辑部署目标
  - 不按 service 拆分；目标属于当前流水线整体
  - 不发请求，目标由父组件解析后传入
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ targetsByEnv: Record<string, string[]> }>()

const { t } = useAppI18n()
const visibleHostLimit = 2

const envEntries = computed(() => Object.entries(props.targetsByEnv ?? {}).filter(([env]) => env))
const totalHostCount = computed(() => envEntries.value.reduce((sum, [, hosts]) => sum + hosts.length, 0))

function visibleHosts(hosts: string[]) {
  return hosts.slice(0, visibleHostLimit)
}

function overflowHosts(hosts: string[]) {
  return hosts.slice(visibleHostLimit)
}
</script>

<template>
  <div class="deploy-target-readonly" data-test="deploy-target-readonly">
    <div class="dtr-head">
      <div>
        <div class="dtr-title">
          {{ t('settings.pipeline.deployTarget') }}
          <span class="dtr-badge">{{ t('settings.pipeline.readonly') }}</span>
          <span class="dtr-badge">{{ t('settings.pipeline.envCount', { count: envEntries.length }) }}</span>
          <span class="dtr-badge">{{ t('settings.pipeline.hostCount', { count: totalHostCount }) }}</span>
        </div>
        <div class="dtr-hint">{{ t('settings.pipeline.deployTargetHint') }}</div>
      </div>
      <button type="button" class="settings-btn settings-btn-text dtr-link">
        {{ t('settings.pipeline.viewProjectEnv') }}
      </button>
    </div>

    <div class="dtr-matrix">
      <div class="dtr-label-cell" data-test="deploy-target-label">
        <span>{{ t('settings.pipeline.currentPipeline') }}</span>
        <strong>{{ t('settings.pipeline.targetHosts') }}</strong>
      </div>
      <div
        v-for="[env, hosts] in envEntries"
        :key="env"
        class="dtr-env-cell"
        :data-test="`deploy-target-env-${env}`"
      >
        <div class="dtr-env">{{ env }}</div>
        <div class="dtr-hosts">
          <span v-for="host in visibleHosts(hosts)" :key="host" class="dtr-chip">{{ host }}</span>
          <span v-if="overflowHosts(hosts).length" class="dtr-overflow">
            +{{ overflowHosts(hosts).length }}
            <span class="dtr-popover" :data-test="`deploy-target-overflow-${env}`">
              <span class="dtr-popover-title">{{ t('settings.pipeline.allHosts', { count: hosts.length }) }}</span>
              <span v-for="host in hosts" :key="host" class="dtr-popover-host">{{ host }}</span>
            </span>
          </span>
          <span v-if="hosts.length === 0" class="dtr-empty">{{ t('common.none') }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.deploy-target-readonly {
  display: grid;
  gap: 10px;
  padding: 12px 18px 14px;
  background: #0e141c;
  border-bottom: 1px solid #263240;
}

.dtr-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.dtr-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}

.dtr-badge {
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: #15202b;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 500;
  padding: 2px 7px;
}

.dtr-hint {
  margin-top: 5px;
  color: var(--text-tertiary, #667);
  font-size: 11px;
}

.dtr-link {
  align-self: center;
}

.dtr-matrix {
  display: grid;
  grid-template-columns: 220px repeat(auto-fit, minmax(180px, 1fr));
  border: 1px solid #1d2936;
  border-radius: 6px;
  overflow: visible;
}

.dtr-label-cell,
.dtr-env-cell {
  min-height: 74px;
  border-right: 1px solid #1a2330;
  background: #0d151d;
  padding: 12px;
}

.dtr-env-cell:last-child {
  border-right: 0;
}

.dtr-label-cell {
  display: grid;
  align-content: center;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}

.dtr-label-cell strong {
  color: var(--text-primary);
  font-weight: 650;
}

.dtr-env {
  margin-bottom: 10px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}

.dtr-hosts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.dtr-chip,
.dtr-overflow {
  display: inline-flex;
  align-items: center;
  min-height: 28px;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: #182331;
  color: #d7e1e8;
  font-size: 12px;
  padding: 3px 9px;
}

.dtr-chip::before,
.dtr-popover-host::before {
  content: "";
  width: 6px;
  height: 6px;
  border-radius: 50%;
  margin-right: 7px;
  background: #3fb950;
}

.dtr-overflow {
  position: relative;
  color: #8ec5ff;
  background: rgba(31, 111, 235, 0.14);
  border-color: rgba(31, 111, 235, 0.35);
}

.dtr-overflow:hover .dtr-popover {
  opacity: 1;
  pointer-events: auto;
  transform: translateY(0);
}

.dtr-popover {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  z-index: 5;
  min-width: 150px;
  display: grid;
  gap: 7px;
  border: 1px solid #263240;
  border-radius: 6px;
  background: #111a24;
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.34);
  color: var(--text-primary);
  opacity: 0;
  padding: 10px;
  pointer-events: none;
  transform: translateY(4px);
  transition: opacity 0.14s ease, transform 0.14s ease;
}

.dtr-popover-title {
  color: var(--text-secondary);
  font-size: 11px;
}

.dtr-popover-host {
  display: flex;
  align-items: center;
  color: var(--text-primary);
  font-size: 12px;
}

.dtr-empty {
  color: var(--text-tertiary, #667);
  font-size: 12px;
}
</style>
