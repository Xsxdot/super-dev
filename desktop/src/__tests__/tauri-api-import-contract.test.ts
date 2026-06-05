/**
 * Tauri API 导入合约测试。
 *
 * 职责：
 *   - 约束前端 core invoke 使用静态导入，避免生产构建动态 chunk 丢失命名导出
 *
 * 边界：
 *   - 不启动 Tauri runtime
 *   - 不验证 command 的真实执行结果
 */
import { describe, expect, it } from 'vitest'
import mcpInstallSource from '../api/mcpInstall.ts?raw'
import popoverServicePanelSource from '../components/Popover/PopoverServicePanel.vue?raw'

const tauriCoreInvokeUsers = [
  { name: 'mcpInstall API', source: mcpInstallSource },
  { name: 'popover service panel', source: popoverServicePanelSource },
]

describe('Tauri API import contract', () => {
  it('uses static imports for core invoke callers', () => {
    for (const file of tauriCoreInvokeUsers) {
      expect(file.source, file.name).not.toContain("import('@tauri-apps/api/core')")
      expect(file.source, file.name).toContain("from '@tauri-apps/api/core'")
    }
  })
})
