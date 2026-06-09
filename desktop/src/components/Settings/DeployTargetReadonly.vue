<!--
DeployTargetReadonly：部署目标只读展示。

职责：
  - 按环境展示部署目标机器

边界：
  - 只读，不在流水线表单中编辑部署目标
  - 不发请求，目标由父组件解析后传入
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{ targetsByEnv: Record<string, string[]> }>()

const { t } = useAppI18n()
</script>

<template>
  <div class="deploy-target-readonly" data-test="deploy-target-readonly">
    <div class="dtr-label">{{ t('settings.pipeline.deployTarget') }}</div>
    <div class="dtr-rows">
      <div v-for="(hosts, env) in targetsByEnv" :key="env" class="dtr-row" :data-test="`deploy-target-${env}`">
        <span class="dtr-env">{{ env }}</span>
        <span class="dtr-hosts">
          <span v-for="host in hosts" :key="host" class="dtr-chip">{{ host }}</span>
          <span v-if="hosts.length === 0" class="dtr-empty">{{ t('common.none') }}</span>
        </span>
      </div>
    </div>
    <div class="dtr-hint">{{ t('settings.pipeline.deployTargetHint') }}</div>
  </div>
</template>

<style scoped>
.deploy-target-readonly {
  padding: 10px 18px;
  background: #0e141c;
  border-bottom: 1px solid #263240;
}

.dtr-label {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
}

.dtr-rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
}

.dtr-row {
  display: flex;
  gap: 10px;
  align-items: center;
  font-size: 12px;
}

.dtr-env {
  min-width: 60px;
  color: var(--text-secondary);
}

.dtr-hosts {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.dtr-chip {
  border-radius: 4px;
  background: #1a2330;
  color: #c6d5dd;
  padding: 2px 8px;
}

.dtr-empty,
.dtr-hint {
  color: var(--text-tertiary, #667);
}

.dtr-hint {
  margin-top: 6px;
  font-size: 11px;
}
</style>
