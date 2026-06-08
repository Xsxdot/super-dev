/**
 * Tauri capability 合约测试。
 *
 * 职责：
 *   - 约束桌面端窗口层能力声明与前端调用保持一致
 *
 * 边界：
 *   - 不启动 Tauri runtime
 *   - 不验证插件内部权限实现
 */
import { describe, expect, it } from 'vitest'
import defaultCapability from '../../src-tauri/capabilities/default.json'

describe('Tauri capabilities', () => {
  it('allows manual window dragging from custom runtime chrome', () => {
    expect(defaultCapability.permissions).toContain('core:window:allow-start-dragging')
  })

  it('allows native notification access for approval reminders', () => {
    expect(defaultCapability.permissions).toContain('notification:default')
  })
})
