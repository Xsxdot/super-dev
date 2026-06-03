<!--
TemplateManagerTab：流水线模板管理页签。

职责：
  - 展示 builtin/user/project 模板列表
  - 提供导入 YAML 模板入口
  - 提供模板内容查看入口

边界：
  - 不编辑模板内容
  - 不解析模板 YAML
  - 不直接访问文件系统或 agent API
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PipelineTemplateSummary } from '@/api/agent'

const props = defineProps<{
  templates: PipelineTemplateSummary[]
  onImport?: () => void
  onView?: (template: PipelineTemplateSummary) => void
}>()

const { t } = useI18n()
</script>

<template>
  <section class="template-manager">
    <header class="template-toolbar">
      <div>
        <h1>{{ t('settings.templates.title') }}</h1>
        <p>{{ t('settings.templates.description') }}</p>
      </div>
      <button type="button" class="primary-btn" data-test="template-import" @click="props.onImport?.()">
        {{ t('settings.templates.import') }}
      </button>
    </header>

    <div v-if="templates.length === 0" class="empty">
      {{ t('settings.templates.empty') }}
    </div>

    <div v-else class="template-table">
      <div class="template-row table-head">
        <span>{{ t('settings.templates.source') }}</span>
        <span>{{ t('settings.templates.name') }}</span>
        <span>{{ t('settings.templates.version') }}</span>
        <span>{{ t('settings.templates.descriptionColumn') }}</span>
        <span>{{ t('settings.templates.actions') }}</span>
      </div>
      <div v-for="template in templates" :key="`${template.source}:${template.id}:${template.version}`" class="template-row">
        <span class="source">{{ template.source }}</span>
        <span class="name">{{ template.name }}</span>
        <span>{{ template.version }}</span>
        <span class="description">{{ template.description || t('settings.templates.noDescription') }}</span>
        <button type="button" class="text-btn" :data-test="`template-view-${template.id}`" @click="props.onView?.(template)">
          {{ t('common.view') }}
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.template-manager {
  width: 100%;
}
.template-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
}
.template-toolbar h1 {
  margin: 0;
  font-size: 18px;
  color: var(--text-primary);
}
.template-toolbar p {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--text-tertiary);
}
.primary-btn {
  padding: 5px 12px;
  border: none;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
  font-size: 12px;
}
.empty {
  padding: 12px 0;
  font-size: 12px;
  color: var(--text-tertiary);
}
.template-table {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
}
.template-row {
  display: grid;
  grid-template-columns: 90px minmax(160px, 1fr) 80px minmax(220px, 1.4fr) 56px;
  gap: 10px;
  align-items: center;
  padding: 7px 10px;
  border-top: 1px solid var(--border-secondary);
  font-size: 12px;
  color: var(--text-secondary);
}
.template-row:first-child {
  border-top: none;
}
.table-head {
  background: var(--bg-secondary);
  color: var(--text-tertiary);
  font-size: 11px;
}
.name {
  color: var(--text-primary);
  font-weight: 600;
}
.source,
.description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.text-btn {
  border: none;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  font-size: 12px;
  padding: 0;
  text-align: left;
}
</style>
