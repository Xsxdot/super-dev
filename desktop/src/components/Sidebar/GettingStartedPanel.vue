<!--
起步旅程浮层清单组件

职责：
  - 渲染 5 个主推步骤 + 1 个可选流水线步骤的完成态与说明
  - 当前步骤展开，提供提示词复制（step2 支持注入项目目录、step4 注入主机名）
  - 以 Outcome Coach 形式强调「远端实时日志」的价值兑现点

边界：
  - 不自行判定完成状态，只读取 gettingStartedStore 派生状态
  - 不发业务 API；目录选择用 plugin-dialog，复制用 navigator.clipboard
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { open } from '@tauri-apps/plugin-dialog'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useGettingStartedStore, type GettingStartedStep } from '@/stores/gettingStarted'
import { useNodeStore } from '@/stores/node'

const { t } = useAppI18n()
const gs = useGettingStartedStore()
const nodeStore = useNodeStore()

const mainSteps: GettingStartedStep[] = ['step0', 'step1', 'step2', 'step3', 'step4']
const copyableSteps: GettingStartedStep[] = ['step2', 'step3', 'step4', 'step5']
const chosenPath = ref('')
const copyState = ref<{ step: GettingStartedStep | null; ok: boolean }>({ step: null, ok: true })

const currentStep = computed<GettingStartedStep>(() => gs.currentStep ?? 'step4')
const currentStepTitle = computed(() => t(`gettingStarted.steps.${currentStep.value}.title`))
const currentStepDescription = computed(() => t(`gettingStarted.steps.${currentStep.value}.desc`))
const isRemoteLogStep = computed(() => currentStep.value === 'step4')
const panelTitle = computed(() =>
  isRemoteLogStep.value ? t('gettingStarted.panelTitle') : `${t('gettingStarted.entryTitle')} · ${currentStepTitle.value}`,
)

// readyRemoteHostName 取第一台健康远端主机名，用于把真实主机注入 step4 提示词。
const readyRemoteHostName = computed(() => {
  const node = nodeStore.nodesList.find(item => item.reachable && item.agent?.health === 'healthy')
  return node?.name || node?.host_id || ''
})

function isDone(step: GettingStartedStep): boolean {
  return gs.isStepCompleted(step)
}

function isCurrent(step: GettingStartedStep): boolean {
  return currentStep.value === step
}

function canCopy(step: GettingStartedStep): boolean {
  return copyableSteps.includes(step)
}

function promptFor(step: GettingStartedStep): string {
  if (step === 'step2') {
    return chosenPath.value
      ? t('gettingStarted.steps.step2.prompt', { path: chosenPath.value })
      : t('gettingStarted.steps.step2.promptNoPath')
  }
  if (step === 'step4') {
    return readyRemoteHostName.value
      ? t('gettingStarted.steps.step4.prompt', { hostName: readyRemoteHostName.value })
      : t('gettingStarted.steps.step4.promptNoHost')
  }
  return t(`gettingStarted.steps.${step}.prompt`)
}

async function chooseDir() {
  const selected = await open({ directory: true, multiple: false, title: t('gettingStarted.chooseDir') })
  if (typeof selected === 'string') chosenPath.value = selected
}

async function copyPrompt(step: GettingStartedStep) {
  try {
    await navigator.clipboard.writeText(promptFor(step))
    copyState.value = { step, ok: true }
  } catch {
    copyState.value = { step, ok: false }
  }
}

function dismiss() {
  gs.dismiss()
}
</script>

<template>
  <aside class="gs-panel" data-test="getting-started-panel">
    <header class="gs-header">
      <div class="gs-title-block">
        <strong>{{ panelTitle }}</strong>
        <span>{{ gs.completedCount }} / {{ gs.totalSteps }}</span>
      </div>
      <button type="button" class="gs-icon-btn" data-test="gs-dismiss" :title="t('gettingStarted.dismiss')" @click="dismiss">
        ×
      </button>
    </header>

    <p class="gs-subtitle">
      {{ isRemoteLogStep ? t('gettingStarted.panelSubtitle') : currentStepDescription }}
    </p>

    <div class="gs-completed-strip" :aria-label="t('gettingStarted.sectionMain')">
      <span
        v-for="step in mainSteps"
        :key="step"
        class="gs-strip-item"
        :class="{ 'is-done': isDone(step), 'is-current': isCurrent(step) }"
        :data-test="`step-${step}`"
      >
        <span class="gs-strip-mark">{{ isDone(step) ? '✓' : mainSteps.indexOf(step) }}</span>
        <span class="gs-strip-label">{{ t(`gettingStarted.steps.${step}.title`) }}</span>
      </span>
    </div>

    <section class="gs-current">
      <div class="gs-current-head">
        <span class="gs-current-number">{{ mainSteps.indexOf(currentStep) }}</span>
        <div>
          <h2>{{ currentStepTitle }}</h2>
          <p>{{ currentStepDescription }}</p>
        </div>
      </div>

      <div v-if="currentStep === 'step2'" class="gs-field-row">
        <span>{{ t('gettingStarted.chooseDir') }}</span>
        <button type="button" class="gs-secondary-btn" data-test="choose-step2-dir" @click="chooseDir">
          {{ t('gettingStarted.chooseDir') }}
        </button>
        <code v-if="chosenPath" class="gs-inline-code">{{ chosenPath }}</code>
      </div>

      <div v-if="currentStep === 'step4'" class="gs-field-row">
        <span>{{ t('gettingStarted.targetHost') }}</span>
        <code class="gs-host-chip">{{ readyRemoteHostName || t('gettingStarted.noHost') }}</code>
        <button type="button" class="gs-secondary-btn">{{ t('gettingStarted.selectHost') }}</button>
      </div>

      <div v-if="canCopy(currentStep)" class="gs-prompt-block">
        <div class="gs-prompt-head">
          <span>{{ t('gettingStarted.promptLabel') }}</span>
          <button type="button" class="gs-link-btn">{{ t('gettingStarted.howToUse') }}</button>
        </div>
        <pre>{{ promptFor(currentStep) }}</pre>
        <button
          type="button"
          class="gs-primary-btn"
          :data-test="`copy-${currentStep}`"
          @click="copyPrompt(currentStep)"
        >
          {{ copyState.step === currentStep ? (copyState.ok ? t('gettingStarted.copied') : t('gettingStarted.copyFailed')) : t('gettingStarted.copyPrompt') }}
        </button>
      </div>

      <div class="gs-outcome">
        <p>{{ t('gettingStarted.outcomeTitle') }}</p>
        <div class="gs-outcome-grid">
          <span>{{ t('gettingStarted.outcomeSidebar') }}</span>
          <span>{{ t('gettingStarted.outcomeRuntime') }}</span>
          <span>{{ t('gettingStarted.outcomeLogs') }}</span>
        </div>
      </div>
    </section>

    <section class="gs-optional" :class="{ 'is-done': isDone('step5') }" data-test="step-step5">
      <span class="gs-current-number">5</span>
      <div class="gs-optional-main">
        <div class="gs-optional-title">
          <strong>{{ t('gettingStarted.steps.step5.title') }}</strong>
          <em>{{ t('gettingStarted.optional') }}</em>
        </div>
        <p>{{ t('gettingStarted.steps.step5.desc') }}</p>
      </div>
      <button type="button" class="gs-icon-btn" data-test="copy-step5" @click="copyPrompt('step5')">
        ↗
      </button>
    </section>
  </aside>
</template>

<style scoped>
.gs-panel {
  display: grid;
  width: min(520px, calc(100vw - 32px));
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 18px 44px rgba(0, 0, 0, 0.34);
  color: var(--text-primary);
}

.gs-header,
.gs-field-row,
.gs-prompt-head,
.gs-optional,
.gs-optional-title {
  display: flex;
  align-items: center;
}

.gs-header {
  justify-content: space-between;
  gap: 12px;
}

.gs-title-block {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.gs-title-block strong {
  font-size: 15px;
  font-weight: 700;
}

.gs-title-block span,
.gs-subtitle,
.gs-current-head p,
.gs-field-row,
.gs-prompt-head,
.gs-optional-main p,
.gs-outcome p {
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.45;
}

.gs-subtitle,
.gs-current-head p,
.gs-optional-main p,
.gs-outcome p {
  margin: 0;
}

.gs-icon-btn,
.gs-secondary-btn,
.gs-primary-btn,
.gs-link-btn {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}

.gs-icon-btn {
  width: 28px;
  height: 28px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
}

.gs-icon-btn:hover,
.gs-secondary-btn:hover,
.gs-link-btn:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.gs-completed-strip {
  display: flex;
  gap: 6px;
  min-width: 0;
  padding: 7px;
  border: 1px solid rgba(139, 148, 158, 0.14);
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.025);
}

.gs-strip-item {
  display: inline-flex;
  min-width: 0;
  flex: 1 1 0;
  align-items: center;
  gap: 5px;
  color: var(--text-tertiary);
  font-size: 10px;
}

.gs-strip-item.is-done {
  color: var(--success);
}

.gs-strip-item.is-current {
  color: var(--accent);
}

.gs-strip-mark {
  display: inline-flex;
  width: 16px;
  height: 16px;
  flex: 0 0 16px;
  align-items: center;
  justify-content: center;
  border: 1px solid currentColor;
  border-radius: 50%;
  font-size: 10px;
  font-weight: 700;
}

.gs-strip-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gs-current {
  display: grid;
  gap: 12px;
  padding: 12px;
  border-left: 3px solid var(--accent);
  border-radius: 7px;
  background: rgba(31, 111, 235, 0.08);
}

.gs-current-head {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr);
  gap: 10px;
  align-items: flex-start;
}

.gs-current-number {
  display: inline-flex;
  width: 24px;
  height: 24px;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
}

.gs-current-head h2 {
  margin: 1px 0 4px;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 700;
}

.gs-field-row {
  gap: 8px;
  min-width: 0;
}

.gs-field-row > span {
  flex: 0 0 auto;
}

.gs-inline-code,
.gs-host-chip {
  min-width: 0;
  overflow: hidden;
  padding: 4px 7px;
  border: 1px solid rgba(139, 148, 158, 0.16);
  border-radius: 6px;
  background: rgba(13, 20, 29, 0.78);
  color: var(--success);
  font-family: var(--font-mono);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gs-inline-code {
  color: var(--text-secondary);
}

.gs-secondary-btn,
.gs-link-btn {
  min-height: 28px;
  padding: 0 9px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
}

.gs-link-btn {
  min-height: 24px;
  margin-left: auto;
  border-color: transparent;
  background: transparent;
}

.gs-prompt-block {
  display: grid;
  gap: 8px;
}

.gs-prompt-block pre {
  max-height: 104px;
  overflow: auto;
  margin: 0;
  padding: 10px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  border-radius: 7px;
  background: rgba(6, 12, 19, 0.72);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 1.55;
  white-space: pre-wrap;
}

.gs-primary-btn {
  min-height: 32px;
  justify-self: start;
  padding: 0 14px;
  border-color: rgba(88, 166, 255, 0.42);
  background: var(--accent);
  color: #fff;
  font-weight: 650;
}

.gs-primary-btn:hover {
  filter: brightness(1.08);
}

.gs-outcome {
  display: grid;
  gap: 8px;
}

.gs-outcome-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 7px;
}

.gs-outcome-grid span {
  min-height: 44px;
  padding: 9px;
  border: 1px solid rgba(139, 148, 158, 0.14);
  border-radius: 7px;
  background: rgba(13, 20, 29, 0.62);
  color: var(--text-secondary);
  font-size: 10.5px;
  line-height: 1.35;
}

.gs-optional {
  gap: 10px;
  padding: 10px 12px;
  border-top: 1px solid var(--border-secondary);
}

.gs-optional .gs-current-number {
  background: rgba(139, 148, 158, 0.18);
  color: var(--text-secondary);
}

.gs-optional.is-done .gs-current-number {
  background: rgba(63, 185, 80, 0.16);
  color: var(--success);
}

.gs-optional-main {
  min-width: 0;
  flex: 1;
}

.gs-optional-title {
  gap: 8px;
}

.gs-optional-title strong {
  font-size: 12px;
}

.gs-optional-title em {
  padding: 1px 6px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 10px;
  font-style: normal;
}
</style>
