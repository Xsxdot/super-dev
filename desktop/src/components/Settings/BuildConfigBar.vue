<!--
BuildConfigBar：流水线级构建配置带。

职责：
  - 选择构建机器，写入流水线级 builder 角色
  - 选择代码同步方式，写入 ProjectPipeline.sync_mode

边界：
  - 不保存配置，仅通过 update 事件回传父组件
  - 不处理环境变量或部署目标展示
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { SyncMode } from '@/api/agent'

type HostOption = { id: string; name: string; is_self?: boolean }

const props = defineProps<{
  builderHostId: string
  syncMode: SyncMode
  syncCommand?: string
  hosts: HostOption[]
}>()

const emit = defineEmits<{
  'update:builderHostId': [string]
  'update:syncMode': [SyncMode]
  'update:syncCommand': [string]
}>()

const { t } = useAppI18n()
const modes: SyncMode[] = ['transfer', 'remote_cmd']
const selfHostId = computed(() => props.hosts.find(host => host.is_self)?.id ?? '')
const selectedIsLocal = computed(() => props.builderHostId === '' || (selfHostId.value !== '' && props.builderHostId === selfHostId.value))
const showLocalOption = computed(() => selfHostId.value === '')

function selectBuilder(event: Event) {
  emit('update:builderHostId', (event.target as HTMLSelectElement).value)
}

function selectSyncMode(mode: SyncMode) {
  emit('update:syncMode', mode)
}

function updateSyncCommand(event: Event) {
  emit('update:syncCommand', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="build-config-bar" data-test="build-config-bar">
    <label class="bcb-field">
      <span>{{ t('settings.pipeline.builderHost') }}</span>
      <select
        class="settings-input bcb-select"
        data-test="build-config-builder"
        :value="builderHostId"
        @change="selectBuilder"
      >
        <option v-if="showLocalOption" value="">{{ t('common.local') }}</option>
        <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
      </select>
    </label>

    <div v-if="!selectedIsLocal" class="bcb-field" data-test="build-config-sync">
      <span>{{ t('settings.pipeline.syncMode') }}</span>
      <div class="bcb-segment" role="group" :aria-label="t('settings.pipeline.syncMode')">
        <button
          v-for="mode in modes"
          :key="mode"
          type="button"
          :class="{ active: syncMode === mode }"
          :data-test="`build-config-sync-${mode}`"
          @click="selectSyncMode(mode)"
        >
          {{ t(`settings.pipeline.syncMode_${mode}`) }}
        </button>
      </div>
    </div>

    <label v-if="!selectedIsLocal && syncMode === 'remote_cmd'" class="bcb-field bcb-command-field">
      <span>{{ t('settings.pipeline.syncCommand') }}</span>
      <input
        class="settings-input bcb-command"
        data-test="build-config-sync-command"
        :placeholder="t('settings.pipeline.syncCommandPlaceholder')"
        :value="syncCommand ?? ''"
        @input="updateSyncCommand"
      />
    </label>
  </div>
</template>

<style scoped>
.build-config-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 20px;
  align-items: end;
  padding: 12px 18px;
  background: #121922;
  border-bottom: 1px solid #263240;
}

.bcb-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
}

.bcb-select {
  min-width: 180px;
  height: 34px;
}

.bcb-command-field {
  flex: 1 1 320px;
}

.bcb-command {
  width: min(520px, 100%);
  height: 34px;
}

.bcb-segment {
  display: flex;
  overflow: hidden;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
}

.bcb-segment button {
  height: 34px;
  padding: 0 14px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}

.bcb-segment button + button {
  border-left: 1px solid var(--border-secondary);
}

.bcb-segment button.active {
  background: #176de9;
  color: #fff;
}
</style>
