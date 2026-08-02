/**
 * portMirrorView 提供服务行 meta、底栏 chips、节点卡镜像区、冲突详情弹窗四处呈现共享的
 * 纯视图模型。
 *
 * 职责：
 *   - 把端口镜像原始状态（MirrorStatus[]）按 deployment 或按 host 两种归属方式转换成
 *     可直接渲染的行视图（MirrorRowView[]，按端口升序）：
 *       - mirrorRowsForDeployment：按 deployment 归属，供服务行/底栏 chip 消费
 *       - mirrorRowsForHost：按 host 归属，汇总该主机上所有 deployment 声明的镜像行，
 *         供节点中心的节点卡镜像区（Task 11）消费——节点卡按主机而非按 deployment 组织
 *   - mirrorSummaryForDeployment 进一步把 deployment 的镜像行浓缩成一句话摘要，供服务行
 *     meta 这种单行空间使用
 *   - 剔除"重复声明"型 failed 条目——它们从未真正尝试建立转发（同 host 同端口被另一个
 *     deployment 抢先声明，见 agent/portmirror/manager.go 的 markDuplicate），不该被呈现
 *     成"一次转发失败"，否则用户会去 retry 一个根本不存在的转发；两个 mirrorRowsFor*
 *     函数共享同一条剔除规则（isRealForwardAttempt），归属维度不同但呈现规则一致
 *
 * 边界：
 *   - 纯函数：参数进、参数出，不读任何 Pinia store、不感知 Vue 响应式
 *   - 不做 i18n——label 只承载结构性（数字/符号/IP 地址）文本，不含任何自然语言词汇；
 *     "本机"/"已镜像"/"镜像冲突"这类可译文案由组件层（EnvGroup.vue/BottomBar.vue/
 *     NodeCard.vue）用 i18n key 包裹 port/hostName 等结构化字段拼出，本文件不导入 vue-i18n
 */
import type { MirrorState, MirrorStatus } from '@/api/agent'

/** MirrorRowView 是单条端口镜像状态的呈现行视图，一行对应一条 MirrorStatus。 */
export interface MirrorRowView {
  port: number
  state: MirrorState
  /**
   * 结构性文本（数字/符号/IP），不含自然语言词汇。active 态形如 ":9100 ⇄ 127.0.0.1:9100"
   * （目标地址与 openUrl 的 host:port 一致）；其余状态只有端口号，形如 ":9100"。
   * 组件层渲染服务行/底栏时不会拼接本字段——服务行要接“本机”这类可译词，底栏要接
   * hostName 而非 127.0.0.1——但保留给 Task 11 冲突弹窗等需要纯技术摘要的场景使用。
   */
  label: string
  conflict: boolean
  /** 仅 state === 'active' 时给出，形如 "http://127.0.0.1:9100"，供"↗ 打开"按钮使用。 */
  openUrl?: string
  /** 该镜像来源主机，底栏 chip 文本（":9100 ⇄ dev-box"）与冲突点击 emit 载荷都需要。 */
  hostId: string
  hostName: string
}

/** MirrorSummary 是服务行 meta 单行空间用的浓缩摘要：none 无镜像；ok 全部正常；conflict 存在冲突。 */
export type MirrorSummary =
  | { kind: 'none' }
  | { kind: 'ok'; ports: number[] }
  | { kind: 'conflict'; port: number }

const DUPLICATE_DECLARATION_ERROR = 'duplicate_port_declaration'

/**
 * isRealForwardAttempt 判断一条 MirrorStatus 是否代表过一次真实的转发尝试。
 *
 * 注意：
 *   - failed 态有两种成因：一种是真的试图转发但失败（值得展示，用户可能想 retry）；
 *     另一种是"重复声明"（duplicate_port_declaration）——这条从未真正尝试转发，见本文件
 *     头部注释。只有后者需要被剔除。
 */
function isRealForwardAttempt(m: MirrorStatus): boolean {
  return !(m.state === 'failed' && m.error === DUPLICATE_DECLARATION_ERROR)
}

function toRowView(m: MirrorStatus): MirrorRowView {
  return {
    port: m.port,
    state: m.state,
    label: m.state === 'active' ? `:${m.port} ⇄ 127.0.0.1:${m.port}` : `:${m.port}`,
    conflict: m.state === 'conflict',
    openUrl: m.state === 'active' ? `http://127.0.0.1:${m.port}` : undefined,
    hostId: m.host_id,
    hostName: m.host_name,
  }
}

/** toRows 是 mirrorRowsForDeployment/mirrorRowsForHost 共享的过滤+转换+排序流水线。 */
function toRows(mirrors: MirrorStatus[]): MirrorRowView[] {
  return mirrors
    .filter(isRealForwardAttempt)
    .map(toRowView)
    .sort((a, b) => a.port - b.port)
}

/**
 * mirrorRowsForDeployment 取出某 deployment 的端口镜像行视图，按端口升序排列。
 *
 * 参数：
 *   - depId: 目标 deployment id
 *   - mirrors: portMirrorStore 的全量快照（调用方传入，本函数不读 store）
 *
 * 返回：
 *   - 该 deployment 下的镜像行视图列表，端口升序；重复声明型 failed 条目已被剔除
 */
export function mirrorRowsForDeployment(depId: string, mirrors: MirrorStatus[]): MirrorRowView[] {
  return toRows(mirrors.filter(m => m.deployment_id === depId))
}

/**
 * mirrorRowsForHost 取出某 host 上全部 deployment 声明的端口镜像行视图，按端口升序排列。
 *
 * 参数：
 *   - hostId: 目标 host id
 *   - mirrors: portMirrorStore 的全量快照（调用方传入，本函数不读 store）
 *
 * 返回：
 *   - 该 host 下的镜像行视图列表，端口升序；重复声明型 failed 条目已被剔除
 *
 * 注意：
 *   - 与 mirrorRowsForDeployment 的区别只是归属维度（host vs deployment）——节点中心的
 *     节点卡（Task 11）是按主机组织的视图，一张卡可能同时展示该主机上多个 deployment
 *     各自声明的端口，所以要按 host_id 聚合而不是按单个 deployment_id 过滤
 */
export function mirrorRowsForHost(hostId: string, mirrors: MirrorStatus[]): MirrorRowView[] {
  return toRows(mirrors.filter(m => m.host_id === hostId))
}

/**
 * mirrorSummaryForDeployment 把某 deployment 的镜像行浓缩成服务行 meta 用的一句话摘要。
 *
 * 参数：
 *   - depId: 目标 deployment id
 *   - mirrors: portMirrorStore 的全量快照（调用方传入，本函数不读 store）
 *
 * 返回：
 *   - none：没有可展示的镜像（含"只有 pending"——还没有确定态可说；含"只剩 duplicate
 *     失败条目"——已被 mirrorRowsForDeployment 剔除）
 *   - conflict：存在至少一个冲突端口，取端口最小的一个代表（服务行 meta 只有一行空间，
 *     冲突是需要用户处理的异常态，比"一切正常"的 ok 列表更值得占用这唯一的一行）
 *   - ok：不存在冲突，且存在至少一个 active 端口，给出全部 active 端口（升序）
 *
 * 注意：
 *   - 非 duplicate 的 genuine failed 条目不参与 ok/conflict 判定，与 pending 一样落
 *     到 none——一次性的转发失败通过 portMirrorStore 的 MirrorEvent 走日志事件流呈现
 *     （见 stores/portMirror.ts 的 stateToEventKind），服务行 meta 这个"稳态一览"位置
 *     不重复呈现瞬时故障，避免同一件事在两个地方各说一遍。
 */
export function mirrorSummaryForDeployment(depId: string, mirrors: MirrorStatus[]): MirrorSummary {
  const rows = mirrorRowsForDeployment(depId, mirrors)
  const firstConflict = rows.find(r => r.conflict)
  if (firstConflict) return { kind: 'conflict', port: firstConflict.port }
  const activePorts = rows.filter(r => r.state === 'active').map(r => r.port)
  if (activePorts.length === 0) return { kind: 'none' }
  return { kind: 'ok', ports: activePorts }
}
