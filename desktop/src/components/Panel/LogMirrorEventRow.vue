<!--
端口镜像事件行

职责：
  - 在日志流中以类日志行外观展示端口镜像状态跃迁（建立/失效/冲突/拆除）
  - 用 [本机] 徽标 + 事件配色，让用户一眼区分"这不是被调试服务打的真实日志"

边界：
  - 不代表真实 LogEntry：本行数据来自 usePortMirrorStore().events（Task 9），
    只在 makeDisplayItems 的输出里存在，不进日志数组、不参与过滤/导出/搜索/证据钉
  - 纯展示，无交互（不可选中钉证据、无右键菜单、无双击操作）
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  at: number
  port: number
  hostName: string
  event: 'established' | 'failed' | 'conflict' | 'removed'
}>()

const { t } = useAppI18n()

const time = computed(() => {
  const d = new Date(props.at)
  return d.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
})

// eventColor 复用应用既有的语义色变量（success/warning/danger，见 src/style.css），
// 与 NodeCard 镜像状态标签同源配色。conflict 用 warning 而非 danger——它通常可以
// 通过 retry/停止占用进程自愈，不是不可逆故障，语气应比 failed 轻一档。
const eventColor = computed(() => {
  if (props.event === 'established') return 'var(--success)'
  if (props.event === 'conflict') return 'var(--warning)'
  if (props.event === 'failed') return 'var(--danger)'
  return 'var(--text-tertiary)' // removed：镜像被主动拆除是中性信息，不代表异常
})

const message = computed(() =>
  t(`panel.log.mirrorEvent.${props.event}`, { port: props.port, host: props.hostName }),
)
</script>

<template>
  <div class="mirror-event-row" data-test="log-mirror-event-row">
    <span class="pin-slot" />
    <span class="ts">{{ time }}</span>
    <span class="badge" data-test="log-mirror-event-badge">[{{ t('panel.log.mirrorEvent.badge') }}]</span>
    <span class="level-slot" />
    <span class="msg" :style="{ color: eventColor }">{{ message }}</span>
  </div>
</template>

<style scoped>
/* grid 列宽刻意与 LogRow.vue 的 .log-row 保持一致，让这一行在虚拟列表里和真实
   日志行的时间/内容列对齐——"像日志行"，靠 badge 与配色区分"不是真实日志"。 */
.mirror-event-row {
  display: grid;
  grid-template-columns: 30px 76px minmax(98px, 150px) 58px minmax(0, 1fr) auto;
  align-items: start;
  column-gap: 8px;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  line-height: 1.58;
}
.pin-slot {
  width: 30px;
  min-width: 30px;
}
.ts {
  color: var(--text-tertiary);
}
.badge {
  min-width: 0;
  overflow: hidden;
  color: var(--accent);
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.level-slot {
  width: auto;
}
.msg {
  min-width: 0;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

@container (max-width: 520px) {
  .mirror-event-row {
    grid-template-columns: 30px 70px 50px minmax(0, 1fr) auto;
    column-gap: 6px;
    padding: 2px 6px;
    font-size: 10.5px;
  }
  .badge {
    display: none;
  }
}
</style>
