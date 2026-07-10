<!--
手动 Agent 接入对话框

职责：
  - 区分本机与云端 Agent 的可达性边界
  - 展示并复制通用 stdio MCP 连接材料
  - 仅在用户于 Agent 内完成真实验证后回报 verified

边界：
  - 不写未知 Agent 的配置文件，不猜配置路径或 schema 方言
  - 不为云端或隔离环境生成无法执行的本机配置
-->
<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import { getGenericMcpConnectionMaterial, type GenericMcpConnectionMaterial } from '@/api/mcpInstall'
import { isOnboardingPreviewMode, previewMcpConnectionMaterial } from '@/dev/onboardingPreview'
import { useAppI18n } from '@/i18n/useAppI18n'
import { emitOnboardingDiagnostic } from '@/lib/onboardingDiagnostics'

type ExecutionEnvironment = 'local' | 'cloud'
type CopyState = 'idle' | 'success' | 'error'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  close: []
  verified: []
}>()

const { t } = useAppI18n()
const dialogRef = ref<HTMLElement | null>(null)
const environment = ref<ExecutionEnvironment | null>(null)
const material = ref<GenericMcpConnectionMaterial | null>(null)
const loading = ref(false)
const loadError = ref('')
const copyState = ref<CopyState>('idle')
const copyError = ref('')
const verifiedChecked = ref(false)
let materialRequestVersion = 0
let previouslyFocusedElement: HTMLElement | null = null

const canConfirmVerified = computed(() =>
  environment.value === 'local' && material.value !== null && verifiedChecked.value,
)

watch(
  () => props.open,
  async (open) => {
    if (open) {
      previouslyFocusedElement = document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null
    }
    resetState()
    if (!open) {
      restorePreviousFocus()
      return
    }
    await nextTick()
    dialogRef.value?.querySelector<HTMLElement>('[data-test="manual-env-local"]')?.focus()
  },
  { immediate: true },
)

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function resetState() {
  materialRequestVersion += 1
  environment.value = null
  material.value = null
  loading.value = false
  loadError.value = ''
  copyState.value = 'idle'
  copyError.value = ''
  verifiedChecked.value = false
}

function restorePreviousFocus() {
  const target = previouslyFocusedElement
  previouslyFocusedElement = null
  if (!target?.isConnected) return
  void nextTick(() => target.focus())
}

function focusableDialogElements(): HTMLElement[] {
  if (!dialogRef.value) return []
  return Array.from(dialogRef.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
  )).filter(element => !element.hasAttribute('hidden'))
}

function handleDialogKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeDialog()
    return
  }
  if (event.key !== 'Tab') return

  const focusable = focusableDialogElements()
  if (focusable.length === 0) {
    event.preventDefault()
    dialogRef.value?.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === dialogRef.value)) {
    event.preventDefault()
    last.focus()
    return
  }
  if (!event.shiftKey && (active === last || active === dialogRef.value)) {
    event.preventDefault()
    first.focus()
  }
}

async function selectEnvironment(nextEnvironment: ExecutionEnvironment) {
  const requestVersion = ++materialRequestVersion
  environment.value = nextEnvironment
  material.value = null
  loading.value = false
  loadError.value = ''
  copyState.value = 'idle'
  copyError.value = ''
  verifiedChecked.value = false
  emitOnboardingDiagnostic('manual.environment.selected', 'info', { environment: nextEnvironment })

  if (nextEnvironment === 'cloud') return

  loading.value = true
  emitOnboardingDiagnostic('manual.material.load.started', 'info')
  try {
    // 仅此 onboarding 对话框可消费浏览器夹具；共享 MCP API 始终保持真实 Tauri 语义。
    const nextMaterial = isOnboardingPreviewMode()
      ? previewMcpConnectionMaterial()
      : await getGenericMcpConnectionMaterial()
    // 用户可能在 command 返回前切到云端；丢弃旧响应，避免再次暴露本机材料。
    if (requestVersion !== materialRequestVersion || environment.value !== 'local') return
    material.value = nextMaterial
    emitOnboardingDiagnostic('manual.material.load.succeeded', 'info', {
      transport: nextMaterial.transport,
    })
  } catch (error) {
    if (requestVersion !== materialRequestVersion || environment.value !== 'local') return
    loadError.value = errorMessage(error)
    emitOnboardingDiagnostic('manual.material.failed', 'error', {
      errorCode: 'manual_material_load_failed',
      errorType: error instanceof Error ? error.name : typeof error,
    })
  } finally {
    if (requestVersion === materialRequestVersion) loading.value = false
  }
}

async function copyConfig() {
  if (!material.value) return
  copyState.value = 'idle'
  copyError.value = ''
  emitOnboardingDiagnostic('manual.material.copy.started', 'info')
  try {
    await navigator.clipboard.writeText(material.value.manual_config)
    copyState.value = 'success'
    emitOnboardingDiagnostic('manual.material.copied', 'info')
  } catch (error) {
    copyState.value = 'error'
    copyError.value = errorMessage(error)
    emitOnboardingDiagnostic('manual.material.copy.failed', 'error', {
      errorCode: 'manual_clipboard_write_failed',
      errorType: error instanceof Error ? error.name : typeof error,
    })
  }
}

function confirmVerified() {
  if (!canConfirmVerified.value) return
  emitOnboardingDiagnostic('manual.connection.verified', 'info')
  emit('verified')
}

function closeDialog() {
  resetState()
  emit('close')
  restorePreviousFocus()
}
</script>

<template>
  <div v-if="open" class="manual-dialog-backdrop" @mousedown.self="closeDialog">
    <section
      ref="dialogRef"
      class="manual-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="manual-dialog-title"
      aria-describedby="manual-dialog-description"
      tabindex="-1"
      @keydown="handleDialogKeydown"
    >
      <header class="manual-dialog-header">
        <div>
          <h2 id="manual-dialog-title">{{ t('onboarding.manual.dialogTitle') }}</h2>
          <p id="manual-dialog-description">{{ t('onboarding.manual.dialogDescription') }}</p>
        </div>
        <button class="icon-button" type="button" :aria-label="t('onboarding.manual.close')" @click="closeDialog">
          <Icon icon="lucide:x" aria-hidden="true" />
        </button>
      </header>

      <div class="manual-dialog-body">
        <fieldset class="environment-fieldset">
          <legend>{{ t('onboarding.manual.environmentTitle') }}</legend>
          <div class="environment-options">
            <button
              type="button"
              class="environment-option"
              :class="{ active: environment === 'local' }"
              :aria-pressed="environment === 'local'"
              data-test="manual-env-local"
              @click="selectEnvironment('local')"
            >
              <Icon icon="lucide:laptop" aria-hidden="true" />
              <span>
                <strong>{{ t('onboarding.manual.localTitle') }}</strong>
                <small>{{ t('onboarding.manual.localDescription') }}</small>
              </span>
            </button>
            <button
              type="button"
              class="environment-option"
              :class="{ active: environment === 'cloud' }"
              :aria-pressed="environment === 'cloud'"
              data-test="manual-env-cloud"
              @click="selectEnvironment('cloud')"
            >
              <Icon icon="lucide:cloud" aria-hidden="true" />
              <span>
                <strong>{{ t('onboarding.manual.cloudTitle') }}</strong>
                <small>{{ t('onboarding.manual.cloudDescription') }}</small>
              </span>
            </button>
          </div>
        </fieldset>

        <div v-if="environment === 'local'" class="manual-local-panel">
          <p v-if="loading" class="manual-state muted">{{ t('onboarding.manual.loading') }}</p>
          <p v-if="loadError" class="manual-state error" data-test="manual-load-error">
            {{ t('onboarding.manual.loadFailed', { message: loadError }) }}
          </p>

          <template v-if="material">
            <p class="manual-hint">{{ t('onboarding.manual.localHint') }}</p>
            <dl class="material-metadata">
              <div data-test="manual-transport">
                <dt>{{ t('onboarding.manual.transport') }}</dt>
                <dd><code>{{ material.transport }}</code></dd>
              </div>
              <div data-test="manual-command">
                <dt>{{ t('onboarding.manual.command') }}</dt>
                <dd><code>{{ material.command }}</code></dd>
              </div>
              <div data-test="manual-agent-url">
                <dt>{{ t('onboarding.manual.agentUrl') }}</dt>
                <dd><code>{{ material.agent_url }}</code></dd>
              </div>
            </dl>

            <div class="config-heading">
              <span>{{ t('onboarding.manual.configExample') }}</span>
              <button class="secondary-button compact" type="button" data-test="manual-copy-config" @click="copyConfig">
                <Icon icon="lucide:copy" aria-hidden="true" />
                {{ t('onboarding.manual.copyConfig') }}
              </button>
            </div>
            <pre data-test="manual-config"><code>{{ material.manual_config }}</code></pre>
            <p
              v-if="copyState !== 'idle'"
              class="manual-copy-feedback"
              :class="copyState"
              data-test="manual-copy-feedback"
            >
              {{
                copyState === 'success'
                  ? t('onboarding.manual.copySucceeded')
                  : t('onboarding.manual.copyFailed', { message: copyError })
              }}
            </p>

            <label class="verification-check">
              <input v-model="verifiedChecked" type="checkbox" data-test="manual-verified-checkbox">
              <span>{{ t('onboarding.manual.verificationLabel') }}</span>
            </label>
          </template>

          <div class="manual-dialog-actions">
            <button class="secondary-button" type="button" @click="closeDialog">
              {{ t('onboarding.manual.close') }}
            </button>
            <button
              class="primary-button"
              type="button"
              data-test="manual-confirm-verified"
              :disabled="!canConfirmVerified"
              @click="confirmVerified"
            >
              {{ t('onboarding.manual.confirmVerified') }}
            </button>
          </div>
        </div>

        <div v-else-if="environment === 'cloud'" class="manual-cloud-panel" data-test="manual-cloud-limit">
          <Icon icon="lucide:cloud-off" aria-hidden="true" />
          <div>
            <h3>{{ t('onboarding.manual.cloudLimitTitle') }}</h3>
            <p>{{ t('onboarding.manual.cloudLimitDescription') }}</p>
          </div>
          <button class="secondary-button" type="button" @click="closeDialog">
            {{ t('onboarding.manual.close') }}
          </button>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.manual-dialog-backdrop {
  position: fixed;
  z-index: 120;
  inset: 0;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(1, 4, 9, 0.72);
}

.manual-dialog {
  width: min(760px, 100%);
  max-height: calc(100vh - 48px);
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--bg-elevated);
  box-shadow: var(--shadow-modal);
  color: var(--text-primary);
  outline: none;
}

.manual-dialog:focus-visible {
  box-shadow: var(--shadow-modal), 0 0 0 2px var(--control-focus);
}

.manual-dialog-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 22px 24px 18px;
  border-bottom: 1px solid var(--border-secondary);
}

.manual-dialog-header h2,
.manual-cloud-panel h3 {
  margin: 0;
}

.manual-dialog-header h2 {
  font-size: 18px;
  line-height: 1.35;
}

.manual-dialog-header p,
.manual-cloud-panel p {
  margin: 6px 0 0;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.55;
}

.icon-button {
  display: inline-grid;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid transparent;
  border-radius: 8px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.icon-button:hover {
  border-color: var(--border-secondary);
  background: var(--control-hover);
  color: var(--text-primary);
}

.manual-dialog-body {
  padding: 20px 24px 24px;
}

.environment-fieldset {
  margin: 0;
  padding: 0;
  border: 0;
}

.environment-fieldset legend {
  margin-bottom: 10px;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}

.environment-options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.environment-option {
  display: flex;
  min-height: 86px;
  align-items: flex-start;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--border-secondary);
  border-radius: 9px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  text-align: left;
  cursor: pointer;
}

.environment-option > svg {
  width: 20px;
  height: 20px;
  flex: 0 0 auto;
  margin-top: 1px;
}

.environment-option span {
  display: grid;
  gap: 5px;
}

.environment-option strong {
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}

.environment-option small {
  font-size: 12px;
  line-height: 1.45;
}

.environment-option:hover,
.environment-option.active {
  border-color: var(--accent);
}

.environment-option.active {
  background: color-mix(in srgb, var(--accent) 10%, var(--bg-primary));
  color: var(--text-primary);
}

.manual-local-panel,
.manual-cloud-panel {
  margin-top: 18px;
  border-top: 1px solid var(--border-secondary);
  padding-top: 18px;
}

.manual-state,
.manual-hint,
.manual-copy-feedback {
  margin: 0 0 12px;
  font-size: 12px;
  line-height: 1.5;
}

.manual-state.muted,
.manual-hint {
  color: var(--text-secondary);
}

.manual-state.error,
.manual-copy-feedback.error {
  color: var(--status-failed);
}

.manual-copy-feedback.success {
  color: var(--status-running);
}

.material-metadata {
  display: grid;
  gap: 0;
  margin: 0 0 16px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
}

.material-metadata > div {
  display: grid;
  grid-template-columns: 160px minmax(0, 1fr);
  gap: 14px;
  padding: 10px 12px;
  border-top: 1px solid var(--border-secondary);
}

.material-metadata > div:first-child {
  border-top: 0;
}

.material-metadata dt {
  color: var(--text-tertiary);
  font-size: 12px;
}

.material-metadata dd {
  min-width: 0;
  margin: 0;
}

.material-metadata code {
  display: block;
  overflow: hidden;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.config-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 600;
}

pre {
  max-height: 190px;
  margin: 0;
  overflow: auto;
  user-select: text;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
  padding: 12px;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
}

.verification-check {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  margin-top: 16px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.45;
}

.verification-check input {
  margin-top: 2px;
  accent-color: var(--accent);
}

.manual-dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 18px;
}

.primary-button,
.secondary-button {
  display: inline-flex;
  min-height: 38px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 14px;
  color: var(--text-primary);
  font-size: 13px;
  cursor: pointer;
}

.primary-button {
  border-color: var(--accent);
  background: var(--accent);
}

.secondary-button {
  background: transparent;
}

.secondary-button.compact {
  min-height: 30px;
  padding: 0 10px;
  font-size: 12px;
}

.primary-button:disabled {
  cursor: not-allowed;
  opacity: 0.52;
}

.primary-button:focus-visible,
.secondary-button:focus-visible,
.environment-option:focus-visible,
.icon-button:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.manual-cloud-panel {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: start;
  gap: 14px;
}

.manual-cloud-panel > svg {
  width: 22px;
  height: 22px;
  margin-top: 2px;
  color: var(--status-starting);
}

.manual-cloud-panel h3 {
  font-size: 14px;
}

@media (max-width: 680px) {
  .manual-dialog-backdrop {
    padding: 12px;
  }

  .environment-options {
    grid-template-columns: 1fr;
  }

  .material-metadata > div {
    grid-template-columns: 1fr;
    gap: 5px;
  }

  .manual-cloud-panel {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .manual-cloud-panel .secondary-button {
    grid-column: 1 / -1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .manual-dialog,
  .environment-option {
    scroll-behavior: auto;
  }
}
</style>
