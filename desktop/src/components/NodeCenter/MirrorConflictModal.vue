<!--
端口镜像冲突详情弹窗

职责：
  - 展示某个 {hostId, port} 端口镜像冲突的详情：固定说明文案 + 占用者信息
  - 提供「停止占用者并重试镜像」动作入口（托管/非托管两种文案，调
    portMirrorStore.stopOccupier 触发）

边界：
  - 不判定托管性——托管/非托管由后端 occupier.managed_deployment_id 裁决，本组件只读
    这个字段选文案，不自己猜测某个 pid 是否属于 SuperDev 托管 deployment
  - 动作结果不轮询，靠 WS 快照回流——stopOccupier 的 HTTP 调用只代表"停止请求已被后端
    接受并同步执行完成"，冲突是否真正解除（conflict → active）由 portMirrorStore 的
    WS 订阅在后台持续更新，服务行/节点卡的镜像行会自然跟着刷新，本弹窗不额外发起轮询
    等待收敛
  - 只读 usePortMirrorStore()/useMirrorConflictModalStore()，不管理这两个 store 的
    生命周期（订阅启动、target 之外的状态清理均不是本组件的职责）
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { formatRelativeAge } from '@/lib/timeDisplay'
import { useMirrorConflictModalStore } from '@/stores/mirrorConflictModal'
import { usePortMirrorStore } from '@/stores/portMirror'
import type { MirrorOccupier } from '@/api/agent'

const { t } = useAppI18n()
const modalStore = useMirrorConflictModalStore()
const mirrorStore = usePortMirrorStore()

const stopping = ref(false)
const stopError = ref<string | null>(null)

// target 变化（含重新打开为另一个 host/port）时清掉上一轮遗留的错误/处理中状态，
// 避免旧冲突的失败提示误盖在新打开的冲突详情上。
watch(
  () => modalStore.target,
  () => {
    stopError.value = null
    stopping.value = false
  },
)

/**
 * mirror 是当前 target 对应的镜像状态明细（含 occupier）。
 *
 * 注意：可能为 undefined——WS 快照更新、target 指向的条目已消失（例如冲突已被别处
 * 解决）都是正常竞态，不是 bug；下游 occupier/managed 计算对 undefined 全程安全降级，
 * 与"占用进程识别不可用"是同一条兜底路径，不需要额外特判。
 */
const mirror = computed(() => {
  const target = modalStore.target
  if (!target) return undefined
  return mirrorStore.mirrors.find(m => m.host_id === target.hostId && m.port === target.port)
})

const occupier = computed<MirrorOccupier | undefined>(() => mirror.value?.occupier)
const managed = computed(() => !!occupier.value?.managed_deployment_id)

function occupierRelativeTime(startedAt: string): string {
  return formatRelativeAge(
    startedAt,
    count => t('mirrorConflict.secondsAgo', { count }),
    count => t('mirrorConflict.minutesAgo', { count }),
    count => t('mirrorConflict.hoursAgo', { count }),
  )
}

function occupierLine(occ: MirrorOccupier): string {
  return t('mirrorConflict.occupier', { name: occ.name, pid: occ.pid, relative: occupierRelativeTime(occ.started_at) })
}

function close() {
  modalStore.close()
}

/**
 * stopAndRetry 调用 stopOccupier 触发停止+重试；成功即关闭弹窗（HTTP 调用本身已经代表
 * "停止动作已被接受并同步执行完成"，冲突是否真正解除交给 WS 快照自然回流到其它已在
 * 消费 portMirrorStore.mirrors 的呈现位置，见文件头边界注释）；失败则原地展示后端
 * error 文案，弹窗保持打开，让用户能看清原因、决定要不要再试一次或换成"稍后处理"。
 */
async function stopAndRetry() {
  const target = modalStore.target
  if (!target) return
  stopping.value = true
  stopError.value = null
  try {
    await mirrorStore.stopOccupier(target.hostId, target.port)
    modalStore.close()
  } catch (err) {
    stopError.value = err instanceof Error ? err.message : String(err)
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <div v-if="modalStore.target" class="settings-modal-backdrop" data-test="mirror-conflict-modal" @click.self="close">
    <section class="settings-modal" role="dialog" aria-modal="true" aria-labelledby="mirror-conflict-title">
      <header class="settings-modal-header">
        <h2 id="mirror-conflict-title" class="settings-modal-title" data-test="mirror-conflict-title">
          {{ t('mirrorConflict.title', { port: modalStore.target.port }) }}
        </h2>
        <button class="settings-btn settings-btn-icon" type="button" @click="close">×</button>
      </header>

      <div class="settings-modal-body mirror-conflict-body">
        <p>{{ t('mirrorConflict.body') }}</p>
        <p class="mirror-conflict-occupier" data-test="mirror-conflict-occupier">
          {{ occupier ? occupierLine(occupier) : t('mirrorConflict.occupierUnknown') }}
        </p>
        <p v-if="stopError" class="settings-alert settings-alert-danger" data-test="mirror-conflict-stop-error">{{ stopError }}</p>
      </div>

      <footer class="settings-modal-footer">
        <button class="settings-btn settings-btn-secondary" type="button" data-test="mirror-conflict-later" @click="close">
          {{ t('mirrorConflict.later') }}
        </button>
        <button
          v-if="occupier"
          class="settings-btn settings-btn-primary"
          type="button"
          data-test="mirror-conflict-stop"
          :disabled="stopping"
          @click="stopAndRetry"
        >
          {{ stopping ? t('common.loading') : t(managed ? 'mirrorConflict.stopManaged' : 'mirrorConflict.stopUnmanaged') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.mirror-conflict-body {
  display: grid;
  gap: 10px;
}
.mirror-conflict-body p {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.55;
}
.mirror-conflict-occupier {
  font-family: var(--font-mono);
}
</style>
