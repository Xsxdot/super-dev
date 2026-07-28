<!--
HostFormModal：单 Host 身份信息新建与编辑表单。

职责：
  - 收集 Host 展示名、入口地址元数据、SSH 登录信息和 tag 字段
  - 在 Host 尚无 host key 指纹时，保存前自动采集并要求用户显式确认
  - 提供「从本机 ~/.ssh 一键导入私钥路径」入口，与手填私钥内容互斥归一
  - 将 Host payload 交由父组件保存

边界：
  - 不收集 Agent 端口或 transport chain
  - 不做信任决策：采集只呈现事实，是否信任由用户点击确认
  - 不处理指纹变更后的重采（由 HostManagerTab 的重采流程负责）
  - 不负责 Agent 安装或连接测试
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Host, HostCreatePayload, SshKey } from '@/api/agent'
import { useRemoteStore } from '@/stores/remote'
import TagInput from './TagInput.vue'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
  error?: string | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const { t } = useAppI18n()

const store = useRemoteStore() // 仅用于只读采集；落库仍由父组件经 emit('submit') 完成

type ScanPhase = 'idle' | 'scanning' | 'confirm' | 'failed'
const scanPhase = ref<ScanPhase>('idle')
const scannedFingerprint = ref('')
const scanErrorMessage = ref('')
const manualEntryOpen = ref(false)

// 私钥导入区状态。keyPath 一旦有值即接管私钥来源，textarea 会被清空并禁用——
// 后端 importHostPrivateKey 是后置覆盖，两个来源同时给会让用户以为粘贴的内容生效了。
const keyScanning = ref(false)
const keyScanError = ref('')
const keyCandidates = ref<SshKey[]>([])
const keyScanned = ref(false)

// 已选中的候选，用于展示类型与 passphrase 提示；路径本身存在 form.ssh_key_path。
const selectedKey = ref<SshKey | null>(null)

async function importLocalKey() {
  keyScanning.value = true
  keyScanError.value = ''
  keyCandidates.value = []
  keyScanned.value = false
  try {
    const keys = await store.listSshKeys()
    keyScanned.value = true
    // 唯一候选直接填入，省掉一次无意义的点击；多个候选才需要用户裁决。
    if (keys.length === 1) {
      chooseKey(keys[0])
      return
    }
    keyCandidates.value = keys
  } catch (err) {
    keyScanError.value = err instanceof Error ? err.message : t('settings.hostForm.keyScanFailed')
  } finally {
    keyScanning.value = false
  }
}

function chooseKey(key: SshKey) {
  selectedKey.value = key
  form.value.ssh_key_path = key.path
  // 导入路径与粘贴内容互斥：清空 textarea，避免提交时两个来源冲突。
  form.value.ssh_private_key = ''
  keyCandidates.value = []
}

function clearKeyPath() {
  selectedKey.value = null
  form.value.ssh_key_path = ''
  keyCandidates.value = []
  keyScanned.value = false
}

// 采集状态重置曾经在 cancelScan/saveWithoutFingerprint/trustAndSave/地址 watcher
// 四处各自内联实现，导致新增的失效点（如切换编辑对象的 hydration watcher）很容易
// 被漏掉——这正是本文件曾经出现过一次真实漏洞的原因。所有需要作废采集状态的地方
// 必须调用这唯一入口，不再各自写重置逻辑。
function resetScan() {
  scanPhase.value = 'idle'
  scannedFingerprint.value = ''
  scanErrorMessage.value = ''
}

// 已配置指纹的 Host 不再自动采集：换指纹必须走「主机已重装」的重采流程，
// 避免把唯一的安全确认点稀释成一次普通保存。
const needsScan = computed(() => {
  if (props.initial?.ssh_host_key_fingerprint_configured) return false
  return !form.value.ssh_host_key_fingerprint?.trim()
})

// 指纹只对采集它时的那个地址有意义：确认卡片/失败卡片展示期间如果用户又改了
// ssh_host 或 ssh_port，之前采集到的指纹就不再对应当前地址了，必须作废重采，
// 否则会出现"确认卡片显示地址 B，却把地址 A 的指纹存进去"的安全漏洞——
// 这正是本功能要防止的失败模式，不能用禁用输入框的方式规避（用户打错地址应该能改）。
watch(
  () => [form.value.ssh_host, form.value.ssh_port] as const,
  () => {
    if (scanPhase.value === 'idle') return
    // scanning 阶段也要作废：若不重置，飞行中请求返回时会把「旧地址采到的指纹」
    // 套用到「新地址」上，与 confirm/failed 阶段是同一个漏洞，只是时间点更早。
    resetScan()
  },
)

const tunnelInvalidationPending = computed(() => {
  const initial = props.initial
  if (!initial) return false
  return form.value.ssh_host !== (initial.ssh_host ?? '')
    || (Number(form.value.ssh_port) || 22) !== (initial.ssh_port || 22)
    || form.value.ssh_user !== (initial.ssh_user ?? 'root')
    || Boolean(form.value.ssh_password?.trim())
    || Boolean(form.value.ssh_private_key?.trim())
    || Boolean(form.value.ssh_key_path?.trim())
    || Boolean(form.value.ssh_host_key_fingerprint?.trim())
    || form.value.clear_ssh_password === true
    || form.value.clear_ssh_private_key === true
})

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    public_ip: '',
    private_ip: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_password: '',
    ssh_private_key: '',
    ssh_key_path: '',
    ssh_host_key_fingerprint: '',
    clear_ssh_password: false,
    clear_ssh_private_key: false,
    clear_ssh_host_key_fingerprint: false,
    tags: [],
  }
}

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    // 切换弹窗正在编辑的 Host 会让任何已采集的指纹立即失效——即使新旧 Host 的
    // ssh_host 文本恰好相同（同地址多主机在故障机群里完全可能出现）。地址 watcher
    // 只在地址「文本变化」时触发，识别不了「同地址换了身份」这种切换，所以这里必须
    // 独立重置，否则确认卡片会在 Host B 的上下文里残留 Host A 采集到的指纹。
    resetScan()
    // 切换弹窗正在编辑的 Host 也必须作废上一台 Host 已选中的导入路径，理由与
    // resetScan 相同：否则确认卡片/私钥框会在新 Host 的上下文里残留旧 Host 的导入选择。
    clearKeyPath()
    if (initial) {
      form.value = {
        name: initial.name,
        public_ip: initial.public_ip ?? '',
        private_ip: initial.private_ip ?? '',
        ssh_host: initial.ssh_host ?? '',
        ssh_port: initial.ssh_port || 22,
        ssh_user: initial.ssh_user ?? 'root',
        // Host read view 永不回显秘密或 pin；空值由后端解释为保留既有配置。
        ssh_password: '',
        ssh_private_key: '',
        ssh_key_path: '',
        ssh_host_key_fingerprint: '',
        clear_ssh_password: false,
        clear_ssh_private_key: false,
        clear_ssh_host_key_fingerprint: false,
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

function buildPayload(fingerprint: string): HostCreatePayload {
  // clear 与 replacement 是后端互斥的两个意图；disabled 只影响交互，不会清空 Vue model。
  // 在唯一提交边界归一化，才能保证用户先输入再勾选清除时不会产生冲突请求。
  // clear_ssh_host_key_fingerprint 恒为 false：清除指纹改走 Task 7 的显式重采流程，
  // 但字段仍保留在 payload 类型与后端契约中，此处不删除。
  return {
    name: form.value.name,
    public_ip: form.value.public_ip,
    private_ip: form.value.private_ip,
    ssh_host: form.value.ssh_host,
    ssh_port: Number(form.value.ssh_port) || 22,
    ssh_user: form.value.ssh_user,
    ssh_password: form.value.clear_ssh_password ? '' : form.value.ssh_password,
    // ssh_private_key 与 ssh_key_path 互斥：后端 importHostPrivateKey 是后置覆盖，
    // 若两者都非空，路径会静默吞掉粘贴内容，用户却以为粘贴的内容生效了。
    // 在这唯一的提交边界归一化，而不是分散在交互路径里各自处理。
    ssh_private_key: form.value.clear_ssh_private_key || form.value.ssh_key_path ? '' : form.value.ssh_private_key,
    ssh_key_path: form.value.clear_ssh_private_key ? '' : (form.value.ssh_key_path ?? ''),
    ssh_host_key_fingerprint: fingerprint,
    clear_ssh_password: form.value.clear_ssh_password,
    clear_ssh_private_key: form.value.clear_ssh_private_key,
    clear_ssh_host_key_fingerprint: false,
    tags: form.value.tags ?? [],
  }
}

async function submit() {
  // 手填或已有指纹时直接提交；否则先采集再由用户确认。
  if (!needsScan.value) {
    emit('submit', buildPayload(form.value.ssh_host_key_fingerprint ?? ''))
    return
  }
  await runScan()
}

async function runScan() {
  scanPhase.value = 'scanning'
  scanErrorMessage.value = ''
  // 记录发起采集时的地址，用于在异步返回时判断地址是否已被用户改动（见下方 identity 校验）。
  // 归一成空串而非保留 undefined：ssh_host 在表单 payload 上是可选字段，但采集接口要求
  // string；在这里统一，下方三处 identity 比较才是在同一种表示上做的，不会因 undefined
  // 与 '' 的差异产生假阳性「地址已变」。
  const scannedHost = form.value.ssh_host ?? ''
  const scannedPort = Number(form.value.ssh_port) || 22
  // 同时记录发起采集时弹窗正在编辑的 Host 身份。地址/端口的 watcher 只能识别「文本变了」，
  // 识别不了「同地址换了一台 Host」——这正是第三处 stale-async 漏洞的成因。新建 Host 时
  // props.initial 恒为 undefined，undefined === undefined 判定为「未切换」，创建流程的采集
  // 不受影响；这里显式写出比较逻辑而非依赖巧合，避免以后重构悄悄破坏这个前提。
  const scannedHostId = props.initial?.id
  try {
    const result = await store.scanHostKey({ ssh_host: scannedHost, ssh_port: scannedPort })
    // 飞行中地址已变：上面的 watcher 早已把 scanPhase 重置为 idle，此时绝不能
    // 用旧地址的采集结果把它又推回 confirm，否则会出现「新地址配旧指纹」的漏洞。
    if ((form.value.ssh_host ?? '') !== scannedHost || (Number(form.value.ssh_port) || 22) !== scannedPort) return
    // 飞行中编辑对象已切换（同地址换了 Host）：hydration watcher 已经 resetScan，
    // 此时绝不能把上一台 Host 采到的指纹又推回 confirm，否则会把 A 的指纹展示在 B 的确认卡片里。
    if (props.initial?.id !== scannedHostId) return
    scannedFingerprint.value = result.fingerprint
    scanPhase.value = 'confirm'
  } catch (err) {
    if ((form.value.ssh_host ?? '') !== scannedHost || (Number(form.value.ssh_port) || 22) !== scannedPort) return
    if (props.initial?.id !== scannedHostId) return
    // 采集失败绝不降级为「跳过指纹直接保存」——必须由用户显式选择后续动作，
    // 否则「未采集」与「采集出错」会被混淆，用户会以为已经配好。
    scanErrorMessage.value = err instanceof Error ? err.message : String(err)
    scanPhase.value = 'failed'
  }
}

function trustAndSave() {
  emit('submit', buildPayload(scannedFingerprint.value))
  resetScan()
}

function cancelScan() {
  resetScan()
}

function openManualEntry() {
  manualEntryOpen.value = true
  scanPhase.value = 'idle'
}

function saveWithoutFingerprint() {
  emit('submit', buildPayload(''))
  resetScan()
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ initial ? t('settings.hosts.edit') : t('settings.hosts.add') }}</h2>
      </div>

      <div class="settings-modal-body host-form-body">
        <div v-if="error" class="settings-alert settings-alert-danger" data-test="host-form-error">
          {{ error }}
        </div>
        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.name') }} <span class="req">*</span></label>
          <input v-model="form.name" class="settings-input" placeholder="nova-api-prod-01" data-test="host-form-name" />
        </div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.publicIP') }}</label>
            <input v-model="form.public_ip" class="settings-input" placeholder="203.0.113.10" data-test="host-form-public-ip" />
          </div>
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.privateIP') }}</label>
            <input v-model="form.private_ip" class="settings-input" placeholder="10.0.0.10" data-test="host-form-private-ip" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.tags') }}</label>
          <TagInput v-model="form.tags!" data-test="host-form-tags" />
        </div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.sshAddress') }}</label>
            <input v-model="form.ssh_host" class="settings-input" placeholder="10.0.0.10" data-test="host-form-ssh-host" />
          </div>
          <div class="settings-field port">
            <label class="settings-field-label">{{ t('settings.hostForm.port') }}</label>
            <input v-model.number="form.ssh_port" class="settings-input" type="number" min="1" data-test="host-form-ssh-port" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshUser') }}</label>
          <input v-model="form.ssh_user" class="settings-input" placeholder="root" data-test="host-form-ssh-user" />
        </div>

        <div class="settings-field">
          <button
            v-if="!manualEntryOpen"
            type="button"
            class="settings-btn settings-btn-text"
            data-test="host-form-manual-fingerprint-toggle"
            @click="manualEntryOpen = true"
          >
            {{ t('settings.hostForm.manualFingerprintToggle') }}
          </button>
          <template v-else>
            <label class="settings-field-label">{{ t('settings.hostForm.hostKeyFingerprint') }}</label>
            <input
              v-model="form.ssh_host_key_fingerprint"
              class="settings-input mono-input"
              placeholder="SHA256:..."
              data-test="host-form-ssh-host-key-fingerprint"
            />
            <span class="settings-field-hint">{{ t('settings.hostForm.hostKeyFingerprintHint') }}</span>
          </template>
        </div>

        <div v-if="scanPhase === 'scanning'" class="settings-alert" data-test="host-form-scanning">
          {{ t('settings.hostForm.scanning') }}
        </div>

        <div v-if="scanPhase === 'confirm'" class="settings-alert settings-alert-warning" data-test="host-form-scan-confirm">
          <strong>{{ t('settings.hostForm.scanConfirmTitle') }}</strong>
          <p>{{ t('settings.hostForm.scanConfirmBody', { addr: `${form.ssh_host}:${form.ssh_port}` }) }}</p>
          <code class="mono-input" data-test="host-form-scan-fingerprint">{{ scannedFingerprint }}</code>
          <div class="scan-actions">
            <button type="button" class="settings-btn" data-test="host-form-scan-cancel" @click="cancelScan">
              {{ t('common.cancel') }}
            </button>
            <button type="button" class="settings-btn settings-btn-primary" data-test="host-form-scan-trust" @click="trustAndSave">
              {{ t('settings.hostForm.scanTrustAndSave') }}
            </button>
          </div>
        </div>

        <div v-if="scanPhase === 'failed'" class="settings-alert settings-alert-danger" data-test="host-form-scan-failed">
          <strong>{{ t('settings.hostForm.scanFailed') }}</strong>
          <p>{{ scanErrorMessage }}</p>
          <div class="scan-actions">
            <button type="button" class="settings-btn" data-test="host-form-scan-retry" @click="runScan">
              {{ t('settings.hostForm.scanRetry') }}
            </button>
            <button type="button" class="settings-btn" data-test="host-form-scan-manual" @click="openManualEntry">
              {{ t('settings.hostForm.scanManualEntry') }}
            </button>
            <button type="button" class="settings-btn settings-btn-text" data-test="host-form-save-without-fingerprint" @click="saveWithoutFingerprint">
              {{ t('settings.hostForm.saveWithoutFingerprint') }}
            </button>
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshPassword') }}</label>
          <input
            v-model="form.ssh_password"
            class="settings-input"
            type="password"
            :disabled="form.clear_ssh_password"
            :placeholder="initial?.ssh_password_configured ? t('settings.hostForm.storedSecretHint') : t('settings.hostForm.passwordHint')"
            data-test="host-form-ssh-password"
          />
          <label v-if="initial?.ssh_password_configured" class="credential-clear">
            <input v-model="form.clear_ssh_password" type="checkbox" data-test="host-form-clear-ssh-password" />
            {{ t('settings.hostForm.clearStoredPassword') }}
          </label>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshPrivateKey') }}</label>

          <div class="key-import">
            <button
              type="button"
              class="settings-btn settings-btn-text"
              :disabled="keyScanning"
              data-test="host-form-import-key"
              @click="importLocalKey"
            >
              {{ keyScanning ? t('settings.hostForm.reading') : t('settings.hostForm.importLocalKey') }}
            </button>

            <div v-if="keyScanError" class="settings-alert settings-alert-danger" data-test="host-form-key-error">
              {{ keyScanError }}
            </div>

            <div v-if="keyScanned && !selectedKey && keyCandidates.length === 0 && !keyScanError" class="settings-field-hint" data-test="host-form-key-empty">
              {{ t('settings.hostForm.noPrivateKey') }}
            </div>

            <ul v-if="keyCandidates.length > 0" class="key-list" data-test="host-form-key-candidates">
              <li
                v-for="key in keyCandidates"
                :key="key.path"
                data-test="host-form-key-row"
                @click="chooseKey(key)"
              >
                <span class="key-name">{{ key.name }}</span>
                <span class="key-meta">{{ key.type }}</span>
                <span v-if="key.encrypted" class="key-meta">{{ t('settings.hostForm.keyEncryptedHint') }}</span>
              </li>
            </ul>

            <div v-if="selectedKey" class="key-selected">
              <span class="settings-field-label">{{ t('settings.hostForm.sshKeyPath') }}</span>
              <code class="mono-input" data-test="host-form-key-path">{{ selectedKey.path }}</code>
              <span v-if="selectedKey.encrypted" class="settings-field-hint">{{ t('settings.hostForm.keyEncryptedHint') }}</span>
              <button
                type="button"
                class="settings-btn settings-btn-text"
                data-test="host-form-clear-key-path"
                @click="clearKeyPath"
              >
                {{ t('settings.hostForm.clearImportedKeyPath') }}
              </button>
            </div>
          </div>

          <textarea
            v-model="form.ssh_private_key"
            class="settings-input key-box"
            :disabled="form.clear_ssh_private_key || Boolean(form.ssh_key_path)"
            :placeholder="form.ssh_key_path ? t('settings.hostForm.keyStoredHint') : (initial?.ssh_private_key_configured ? t('settings.hostForm.keyStoredPlaceholder') : '')"
            data-test="host-form-ssh-private-key"
          />
          <label v-if="initial?.ssh_private_key_configured" class="credential-clear">
            <input v-model="form.clear_ssh_private_key" type="checkbox" data-test="host-form-clear-ssh-private-key" />
            {{ t('settings.hostForm.clearStoredPrivateKey') }}
          </label>
        </div>

        <div v-if="tunnelInvalidationPending" class="settings-alert settings-alert-warning" data-test="host-form-tunnel-invalidation">
          {{ t('settings.hostForm.tunnelInvalidationWarning') }}
        </div>
      </div>

      <div class="settings-modal-footer">
        <button type="button" class="settings-btn" data-test="host-form-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="settings-btn settings-btn-primary" data-test="host-form-submit" @click="submit">{{ t('common.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.host-form-body {
  display: grid;
  gap: 10px;
}
.req { color: var(--status-failed); }
.row { display: flex; gap: 8px; }
.settings-field.flex { flex: 1; }
.port { width: 96px; }
.key-box {
  min-height: 112px;
  font-family: var(--font-mono, monospace);
  resize: vertical;
}
.mono-input {
  font-family: var(--font-mono, monospace);
}
.credential-clear {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
.scan-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}
.key-import {
  display: grid;
  gap: 6px;
  margin-bottom: 6px;
}
.key-list {
  max-height: 180px;
  padding: 0;
  margin: 0;
  overflow-y: auto;
  list-style: none;
  border: 1px solid var(--border-secondary);
}
.key-list li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border-secondary);
  cursor: pointer;
}
.key-name {
  font-weight: 600;
  font-size: 12px;
}
.key-meta {
  color: var(--text-tertiary);
  font-size: 11px;
}
.key-selected {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
