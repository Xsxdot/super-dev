/**
 * onboardingStore 测试零操作引导状态。
 *
 * 职责：
 *   - 验证 MCP 安装状态流转
 *   - 验证启动提示词包含审批演示路径
 *
 * 边界：
 *   - 不调用真实 Tauri command
 *   - 不渲染 Vue 页面
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as mcpInstall from '@/api/mcpInstall'
import { useOnboardingStore } from '../onboarding'

describe('onboardingStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('installSelectedMcp stores successful outcome', async () => {
    vi.spyOn(mcpInstall, 'installMcp').mockResolvedValue({
      installed: true,
      already_present: false,
      backup_path: '/home/me/.claude.json.superdev-bak',
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
    })
    const store = useOnboardingStore()

    await store.installSelectedMcp()

    expect(mcpInstall.installMcp).toHaveBeenCalledWith('claude-code')
    expect(store.installOutcome?.installed).toBe(true)
    expect(store.installError).toBe('')
  })

  it('installSelectedMcp stores error text', async () => {
    vi.spyOn(mcpInstall, 'installMcp').mockRejectedValue(new Error('配置文件格式异常'))
    vi.spyOn(mcpInstall, 'getMcpInstallHint').mockResolvedValue({
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{"superdev":{"command":"/app/superdev-mcp"}}}',
    })
    const store = useOnboardingStore()

    await store.installSelectedMcp()

    expect(store.installError).toBe('配置文件格式异常')
    expect(store.installHint?.config_path).toBe('/home/me/.claude.json')
  })

  it('demoPrompt guides approval flow', () => {
    const store = useOnboardingStore()

    expect(store.demoPrompt).toContain('superdev-sample')
    expect(store.demoPrompt).toContain('restart_service')
    expect(store.demoPrompt).toContain('安全审批')
  })
})
