<!--
TemplateContentModal：模板内容只读查看弹窗。

职责：
  - 展示模板名称与 YAML 内容
  - 展示加载和错误状态
  - 通过 close 事件交还关闭控制权

边界：
  - 不加载模板详情数据
  - 不编辑或保存模板内容
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PipelineTemplateDetail, TemplateInput } from '@/api/agent'

const props = defineProps<{
  open: boolean
  title: string
  yaml: string
  detail?: PipelineTemplateDetail | null
  loading?: boolean
  error?: string
  canApply?: boolean
  applying?: boolean
}>()
defineEmits<{ close: []; apply: [] }>()

const { t } = useI18n()

function inputEntries(): [string, TemplateInput][] {
  return Object.entries(props.detail?.template.inputs ?? {})
}

function inputMeta(input: TemplateInput): string[] {
  const parts = [input.type, input.required ? t('settings.templates.required') : t('settings.templates.optional')]
  if (input.default) parts.push(t('settings.templates.defaultValue', { value: input.default }))
  if (input.options?.length) parts.push(t('settings.templates.options', { value: input.options.join(', ') }))
  return parts
}
</script>

<template>
  <div v-if="open" class="settings-modal-backdrop">
    <section class="settings-modal settings-modal-wide" role="dialog" aria-modal="true">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ title }}</h2>
        <button
          type="button"
          class="settings-btn settings-btn-icon settings-btn-ghost"
          data-test="template-modal-close"
          @click="$emit('close')"
        >
          ×
        </button>
      </header>

      <div class="settings-modal-body template-modal-body">
        <div v-if="loading" class="settings-alert">{{ t('common.loading') }}</div>
        <div v-else-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>
        <template v-else>
          <div v-if="detail" class="template-summary">
            <div class="summary-line">
              <span>{{ t('settings.templates.digest') }}</span>
              <code>{{ detail.digest }}</code>
            </div>
            <section class="input-summary">
              <h3>{{ t('settings.templates.inputSummary') }}</h3>
              <div v-if="inputEntries().length === 0" class="settings-empty">{{ t('settings.templates.noInputs') }}</div>
              <article v-for="[name, input] in inputEntries()" :key="name" class="input-row">
                <div class="input-main">
                  <strong>{{ input.label || name }}</strong>
                  <span>{{ name }}</span>
                </div>
                <div class="input-meta">{{ inputMeta(input).join(' · ') }}</div>
                <p v-if="input.description">{{ input.description }}</p>
              </article>
            </section>
          </div>
          <slot />
          <pre class="yaml-view"><code>{{ yaml }}</code></pre>
        </template>
      </div>

      <footer class="settings-modal-footer">
        <button type="button" class="settings-btn" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button
          v-if="canApply"
          type="button"
          class="settings-btn settings-btn-primary"
          data-test="template-apply"
          :disabled="applying"
          @click="$emit('apply')"
        >
          {{ t('settings.templates.applyToProject') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.template-modal-body {
  display: grid;
  gap: 12px;
}
.template-summary {
  display: grid;
  gap: 10px;
}
.summary-line {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
.summary-line code {
  color: var(--text-secondary);
}
.input-summary h3 {
  margin: 0 0 8px;
  font-size: 12px;
}
.input-row {
  padding: 8px 0;
  border-top: 1px solid var(--border-secondary);
}
.input-main {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.input-main strong {
  font-size: 12px;
}
.input-main span,
.input-meta,
.input-row p {
  color: var(--text-tertiary);
  font-size: 11px;
}
.input-row p {
  margin: 4px 0 0;
}
.yaml-view {
  margin: 0;
  padding: 12px;
  overflow: auto;
  max-height: 520px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  font-size: 12px;
  line-height: 1.5;
}
</style>
