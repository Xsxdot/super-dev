/**
 * mirrorConflictModalStore 管理端口镜像冲突详情弹窗的开关状态——一条跨子树共享的轻量 UI
 * 决策通道。
 *
 * 职责：
 *   - 记录当前要展示冲突详情的目标 {hostId, port}（null 表示未打开）
 *   - 提供 open/close 两个动作：EnvGroup 的冲突段点击（经 SidebarView 转发）与 NodeCard
 *     的冲突行点击（经 NodeCenterView 转发）是两个互不相通的子树，只在 MainPage 汇合，
 *     两条触发路径都调用本 store 的 open() 来打开同一个弹窗实例
 *   - 挂在 MainPage.vue 的 MirrorConflictModal.vue 读取 target 并渲染；结构与既有的
 *     useOperationApprovalStore().notice / clearNotice（App.vue 挂 OperationApprovalNotice
 *     一次、多处触发写 notice）是同一先例
 *
 * 边界：
 *   - 不持有镜像数据本身——host/port 对应的 MirrorStatus 明细（含 occupier）仍读
 *     usePortMirrorStore()，本 store 只是"弹哪一个"的开关，不做 UI 之外的任何决策
 *   - 不渲染 UI，也不发起任何网络请求
 */
import { ref } from 'vue'
import { defineStore } from 'pinia'

export interface MirrorConflictTarget {
  hostId: string
  port: number
}

export const useMirrorConflictModalStore = defineStore('mirrorConflictModal', () => {
  const target = ref<MirrorConflictTarget | null>(null)

  /** open 打开冲突详情弹窗，指向给定 host+port；重复调用直接覆盖为最新目标。 */
  function open(hostId: string, port: number) {
    target.value = { hostId, port }
  }

  /** close 关闭弹窗。 */
  function close() {
    target.value = null
  }

  return { target, open, close }
})
