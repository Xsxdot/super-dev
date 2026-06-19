<!--
AIGuidanceFields：AI 可见运行说明编辑器。

职责：
  - 编辑 ai_note 与 auth_hint 两个非敏感提示字段
  - 在项目、环境、服务配置表单中复用同一组输入

边界：
  - 不编辑 debug_credentials 明文值
  - 不发起保存请求，由父组件统一保存项目配置
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{
  title: string
  hint?: string
  aiNote?: string
  authHint?: string
  testPrefix: string
}>()

const emit = defineEmits<{
  'update:aiNote': [string]
  'update:authHint': [string]
}>()

const { t } = useAppI18n()
</script>

<template>
  <section class="ai-guidance-fields" :data-test="`${testPrefix}-ai-guidance`">
    <div class="ai-guidance-head">
      <h3 class="ai-guidance-title">{{ title }}</h3>
      <p v-if="hint" class="ai-guidance-hint">{{ hint }}</p>
    </div>
    <label class="ai-guidance-field">
      <span>{{ t('settings.aiGuidance.aiNote') }}</span>
      <textarea
        class="settings-input ai-guidance-textarea"
        :data-test="`${testPrefix}-ai-note`"
        :placeholder="t('settings.aiGuidance.aiNotePlaceholder')"
        :value="aiNote ?? ''"
        @input="emit('update:aiNote', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
    <label class="ai-guidance-field">
      <span>{{ t('settings.aiGuidance.authHint') }}</span>
      <textarea
        class="settings-input ai-guidance-textarea"
        :data-test="`${testPrefix}-auth-hint`"
        :placeholder="t('settings.aiGuidance.authHintPlaceholder')"
        :value="authHint ?? ''"
        @input="emit('update:authHint', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
  </section>
</template>

<style scoped>
.ai-guidance-fields {
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: rgba(8, 13, 20, 0.42);
}
.ai-guidance-head {
  display: grid;
  gap: 4px;
}
.ai-guidance-title {
  margin: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}
.ai-guidance-hint {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.5;
}
.ai-guidance-field {
  display: grid;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
.ai-guidance-textarea {
  min-height: 62px;
  resize: vertical;
  line-height: 1.45;
}
</style>
