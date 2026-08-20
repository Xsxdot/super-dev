<!--
ConfigMigrationDialog：legacy 单文件配置 → 分层配置（project.yaml + local.yaml）
的预览与执行弹窗。

职责：
  - 挂载时拉取迁移预览（GET /config-migration），渲染概要、疑似密钥清单与
    .gitignore 变更 diff
  - 收集用户对每条疑似密钥的处置（「不挡、只亮」——默认本机层，用户显式选
    共享层才会入库），执行迁移（POST /config-migration）并展示成功/失败态
  - warn_only 条目（流水线里的疑似密钥）只展示不给处置：机器层 local.yaml 对
    流水线没有 schema 表达，能做的只有把「这些会随 git 提交出去」讲清楚
  - 项目已是 split 格式时（后端返回 not_needed）直接展示已完成态，不当作错误

边界：
  - 不做处置校验——某个值是不是密钥、该留共享层还是本机层，是人的判断，
    后端 ApplyMigration 只按传入的 decisions 执行，本组件只负责收集与透传
  - 不直接改配置文件——一切经 Task 8 的两个 HTTP 端点完成
  - 不反脱敏：预览里的 masked_value 已由后端打码，本组件不请求也不还原明文
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getConfigMigrationPreview, applyConfigMigration, type MigrationPlan, type MigrationSuspect, type Project } from '@/api/agent'

const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ cancel: []; migrated: [Project] }>()

const { t } = useI18n()

type Phase = 'loading' | 'preview' | 'applying' | 'done' | 'not_needed' | 'error'
type Disposition = 'shared' | 'local'

const phase = ref<Phase>('loading')
const plan = ref<MigrationPlan | null>(null)
// 「不挡、只亮」的安全默认：数组下标与 plan.suspects 一一对应，初始值统一为
// 'local'——一个值错留本机的代价是手动搬一次，错发共享层入库的代价是泄露且
// 无法撤回，两者不对称，默认必须偏向安全一侧。
const dispositions = ref<Disposition[]>([])
const loadError = ref<string | null>(null)
const applyError = ref<string | null>(null)

onMounted(load)

async function load() {
  phase.value = 'loading'
  loadError.value = null
  try {
    const result = await getConfigMigrationPreview(props.projectId)
    if (!('suspects' in result)) {
      // 项目在弹窗打开前已是 split 格式（例如横幅渲染后项目被并发迁移）。
      // 这不是失败路径，直接展示已完成态。
      phase.value = 'not_needed'
      return
    }
    // 用 'suspects' in result 正向收窄：'status' in result 的否定分支在部分
    // TS 版本下不会把 {status:'not_needed'} 从联合中剔除，导致下方类型错误。
    plan.value = result
    dispositions.value = result.suspects.map(() => 'local')
    phase.value = 'preview'
    // 下标必须与 plan.suspects 严格一一对应（含 warn_only 条目），否则勾选会串行。

  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err)
    phase.value = 'error'
  }
}

function suspectRowKey(suspect: MigrationSuspect, index: number): string {
  return `${suspect.scope}:${suspect.service ?? ''}:${suspect.env ?? ''}:${suspect.key}:${index}`
}

function setDisposition(index: number, value: Disposition) {
  dispositions.value[index] = value
}

async function apply() {
  if (!plan.value) return
  phase.value = 'applying'
  applyError.value = null
  try {
    // 逐条显式带上 disposition：省略未处置项虽然后端也会按 local 兜底，但
    // 显式传递让「用户到底选了什么」在请求体里一目了然，便于排查和测试。
    // warn_only 条目（流水线里的疑似密钥）不进 decisions：机器层没有承接它们的
    // schema，后端也不会搬动，传一个永远无效的处置只会让请求体自相矛盾。
    const decisions = plan.value.suspects
      .map((suspect, index) => ({ suspect, disposition: dispositions.value[index] }))
      .filter(row => !row.suspect.warn_only)
      .map(row => ({
        scope: row.suspect.scope,
        service: row.suspect.service,
        env: row.suspect.env,
        key: row.suspect.key,
        disposition: row.disposition,
      }))
    const updated = await applyConfigMigration(props.projectId, decisions)
    phase.value = 'done'
    emit('migrated', updated)
  } catch (err) {
    // 失败展示后端 error 文本，不吞错；退回 preview 态允许用户调整处置后重试。
    applyError.value = err instanceof Error ? err.message : String(err)
    phase.value = 'preview'
  }
}

function dismiss() {
  if (phase.value === 'applying') return
  emit('cancel')
}
</script>

<template>
  <div class="settings-modal-backdrop" data-test="config-migration-dialog" @click.self="dismiss">
    <section class="settings-modal settings-modal-wide migration-modal" role="dialog" aria-modal="true" aria-labelledby="config-migration-title">
      <header class="settings-modal-header">
        <h2 id="config-migration-title" class="settings-modal-title">{{ t('configMigration.title') }}</h2>
        <button class="settings-btn settings-btn-icon" type="button" :disabled="phase === 'applying'" @click="dismiss">×</button>
      </header>

      <div class="settings-modal-body migration-body">
        <div v-if="phase === 'loading'" class="settings-alert" data-test="migration-loading">{{ t('common.loading') }}</div>

        <div v-else-if="phase === 'error'" class="settings-alert settings-alert-danger" data-test="migration-error">{{ loadError }}</div>

        <div v-else-if="phase === 'not_needed'" class="settings-alert settings-alert-success" data-test="migration-done">
          {{ t('configMigration.done') }}
        </div>

        <div v-else-if="phase === 'done'" class="settings-alert settings-alert-success" data-test="migration-done">
          <strong>{{ t('configMigration.done') }}</strong>
          <p>{{ t('configMigration.doneDetail') }}</p>
        </div>

        <template v-else-if="plan">
          <section class="migration-section" data-test="migration-summary">
            <p>{{ t('configMigration.summary', { services: plan.service_count, paths: plan.relativized_paths.length }) }}</p>
            <pre v-if="plan.relativized_paths.length" class="migration-mono-list" data-test="migration-relativized-paths">{{ plan.relativized_paths.join('\n') }}</pre>
            <p v-if="plan.ui_state_envs.length" class="migration-note" data-test="migration-ui-state-note">
              {{ t('configMigration.uiStateNote', { envs: plan.ui_state_envs.join(', ') }) }}
            </p>
          </section>

          <section class="migration-section" data-test="migration-gitignore">
            <h3 class="migration-subtitle">{{ t('configMigration.gitignoreTitle') }}</h3>
            <div class="migration-diff">
              <div v-for="line in plan.gitignore.remove_lines" :key="`rm-${line}`" class="diff-line diff-remove">- {{ line }}</div>
              <div v-for="line in plan.gitignore.add_lines" :key="`add-${line}`" class="diff-line diff-add">+ {{ line }}</div>
            </div>
          </section>

          <section class="migration-section" data-test="migration-suspects">
            <h3 class="migration-subtitle">{{ t('configMigration.suspectsTitle') }}</h3>
            <div v-if="plan.suspects.length === 0" class="settings-empty" data-test="migration-suspects-empty">
              {{ t('configMigration.suspectsEmpty') }}
            </div>
            <ul v-else class="migration-suspect-list">
              <li
                v-for="(suspect, index) in plan.suspects"
                :key="suspectRowKey(suspect, index)"
                class="migration-suspect-row"
                :data-test="`suspect-row-${index}`"
              >
                <div class="suspect-info">
                  <code class="settings-mono">{{ suspect.key }}</code>
                  <code class="settings-mono suspect-masked">{{ suspect.masked_value }}</code>
                  <span class="suspect-reason">{{ suspect.reason }}</span>
                  <span v-if="suspect.service" class="suspect-scope">{{ suspect.service }} · {{ suspect.env }}</span>
                  <span v-if="suspect.pipeline" class="suspect-scope" :data-test="`suspect-pipeline-${index}`">
                    {{ suspect.pipeline }}<template v-if="suspect.env"> · {{ suspect.env }}</template><template v-if="suspect.detail"> · {{ suspect.detail }}</template>
                  </span>
                </div>
                <!-- warn_only：机器层对流水线没有 schema，给不出「搬去本机」这个选项，
                     渲染成单选只会诱导用户点一个什么也不会发生的按钮。 -->
                <div v-if="suspect.warn_only" class="suspect-warn-only" :data-test="`suspect-warn-only-${index}`">
                  {{ t('configMigration.warnOnly') }}
                </div>
                <div v-else class="suspect-disposition">
                  <label class="dep-choice">
                    <input
                      type="radio"
                      :data-test="`suspect-shared-${index}`"
                      :checked="dispositions[index] === 'shared'"
                      @change="setDisposition(index, 'shared')"
                    />
                    {{ t('configMigration.dispositionShared') }}
                  </label>
                  <label class="dep-choice">
                    <input
                      type="radio"
                      :data-test="`suspect-local-${index}`"
                      :checked="dispositions[index] === 'local'"
                      @change="setDisposition(index, 'local')"
                    />
                    {{ t('configMigration.dispositionLocal') }}
                  </label>
                </div>
              </li>
            </ul>
          </section>

          <div v-if="applyError" class="settings-alert settings-alert-danger" data-test="migration-apply-error">{{ applyError }}</div>
        </template>
      </div>

      <footer class="settings-modal-footer">
        <template v-if="phase === 'done' || phase === 'not_needed'">
          <button class="settings-btn settings-btn-primary" type="button" data-test="migration-close" @click="dismiss">
            {{ t('common.close') }}
          </button>
        </template>
        <template v-else>
          <button class="settings-btn settings-btn-secondary" type="button" data-test="migration-cancel" :disabled="phase === 'applying'" @click="dismiss">
            {{ t('common.cancel') }}
          </button>
          <button
            class="settings-btn settings-btn-primary"
            type="button"
            data-test="migration-apply"
            :disabled="phase !== 'preview'"
            @click="apply"
          >
            {{ phase === 'applying' ? t('common.loading') : t('configMigration.apply') }}
          </button>
        </template>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.migration-modal {
  width: min(720px, calc(100vw - 48px));
}
.migration-body {
  display: grid;
  gap: 16px;
}
.migration-section {
  display: grid;
  gap: 8px;
}
.migration-section > p {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}
.migration-note {
  color: var(--text-tertiary) !important;
}
.migration-subtitle {
  margin: 0;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 650;
}
.migration-mono-list {
  margin: 0;
  overflow: auto;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 8px 10px;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 1.6;
  white-space: pre;
}
.migration-diff {
  display: grid;
  gap: 2px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 8px 10px;
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 1.6;
}
.diff-line {
  white-space: pre-wrap;
  word-break: break-all;
}
.diff-remove {
  color: var(--danger);
}
.diff-add {
  color: var(--success);
}
.migration-suspect-list {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.migration-suspect-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 8px 10px;
}
.suspect-info {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 12px;
}
.suspect-masked {
  color: var(--text-tertiary);
}
.suspect-reason {
  color: var(--text-secondary);
}
.suspect-scope {
  color: var(--text-tertiary);
  font-size: 11px;
}
.suspect-disposition {
  display: flex;
  flex-shrink: 0;
  gap: 12px;
}
.suspect-warn-only {
  flex-shrink: 0;
  color: var(--status-failed);
  font-size: 11px;
  white-space: nowrap;
}
.dep-choice {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}
</style>
