/**
 * 操作审批系统通知服务。
 *
 * 职责：
 *   - 为新出现的 pending operation approval 发送原生系统通知
 *   - 管理通知权限、同一审批去重、点击通知回到 SuperDev 首页
 *
 * 边界：
 *   - 不执行批准/拒绝操作
 *   - 不读取 approval token
 *   - 不管理 pending approval 列表
 */
import { invoke } from '@tauri-apps/api/core'
import { isPermissionGranted, requestPermission } from '@tauri-apps/plugin-notification'
import type { OperationApproval } from '@/api/agent'

const notifiedApprovalIds = new Set<string>()

function targetSummary(approval: OperationApproval): string {
  return approval.plan.target_summary
    || approval.plan.target.deployment_id
    || approval.plan.target.template_path
    || approval.plan.kind
}

function notificationBody(approval: OperationApproval): string {
  return [
    approval.plan.kind,
    targetSummary(approval),
    `风险 ${approval.plan.risk_level}`,
  ].filter(Boolean).join(' · ')
}

async function canNotify(): Promise<boolean> {
  if (typeof Notification === 'undefined') return false
  if (await isPermissionGranted()) return true
  const permission = await requestPermission()
  return permission === 'granted'
}

// notifyOperationApproval 发送审批系统通知。
//
// 参数：
//   - approval: 新出现的 pending operation approval
//
// 返回：
//   - Promise<void>
//
// 注意：
//   - 通知失败不能阻断审批流程，因此所有异常都在本函数内吞掉
export async function notifyOperationApproval(approval: OperationApproval): Promise<void> {
  if (notifiedApprovalIds.has(approval.id)) return
  notifiedApprovalIds.add(approval.id)

  try {
    if (!(await canNotify())) return

    const notification = new Notification('需要操作审批', {
      body: notificationBody(approval),
    })
    notification.onclick = () => {
      void invoke('show_home_window').catch(() => undefined)
      notification.close()
    }
  } catch {
    // 系统通知只是提醒入口；失败时保留现有应用内审批浮层即可。
  }
}

// resetApprovalNotificationState 重置通知去重状态。
//
// 参数：无
//
// 返回：无
//
// 注意：
//   - 仅供测试隔离使用，业务代码不应调用
export function resetApprovalNotificationState(): void {
  notifiedApprovalIds.clear()
}
