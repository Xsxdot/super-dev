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
import { setLocale } from '@/i18n'
import { useOnboardingStore } from '../onboarding'

describe('onboardingStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale('zh-CN')
    vi.restoreAllMocks()
  })

  it('detectInstalledAgents stores availability and selects installed agents', async () => {
    vi.spyOn(mcpInstall, 'detectCodingAgents').mockResolvedValue([
      { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      { agent: 'codex', installed: true, detection_path: '/usr/local/bin/codex' },
      { agent: 'cursor', installed: false, detection_path: null },
    ])
    const store = useOnboardingStore()

    await store.detectInstalledAgents()

    expect(store.isAgentInstalled('claude-code')).toBe(true)
    expect(store.isAgentInstalled('codex')).toBe(true)
    expect(store.isAgentInstalled('cursor')).toBe(false)
    expect(store.selectedAgents).toEqual(['claude-code', 'codex'])
  })

  it('toggleAgentSelection ignores unavailable agents', async () => {
    vi.spyOn(mcpInstall, 'detectCodingAgents').mockResolvedValue([
      { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      { agent: 'codex', installed: false, detection_path: null },
      { agent: 'cursor', installed: false, detection_path: null },
    ])
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    store.toggleAgentSelection('claude-code')
    store.toggleAgentSelection('codex')

    expect(store.selectedAgents).toEqual([])
  })

  it('installSelectedMcp stores successful outcomes for selected agents', async () => {
    vi.spyOn(mcpInstall, 'detectCodingAgents').mockResolvedValue([
      { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      { agent: 'codex', installed: true, detection_path: '/usr/local/bin/codex' },
      { agent: 'cursor', installed: false, detection_path: null },
    ])
    vi.spyOn(mcpInstall, 'installMcp').mockResolvedValue({
      agent: 'claude-code',
      installed: true,
      already_present: false,
      backup_path: '/home/me/.claude.json.superdev-bak',
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
      skill: {
        installed: true,
        already_present: false,
        target_path: '/home/me/.claude/skills/superdev',
        backup_path: null,
        error: null,
      },
    })
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    await store.installSelectedMcp()

    expect(mcpInstall.installMcp).toHaveBeenCalledWith('claude-code')
    expect(mcpInstall.installMcp).toHaveBeenCalledWith('codex')
    expect(store.installOutcomes).toHaveLength(2)
    expect(store.installOutcomes[0]?.installed).toBe(true)
    expect(store.installOutcomes[0]?.skill.installed).toBe(true)
    expect(store.installOutcomes[0]?.skill.target_path).toBe('/home/me/.claude/skills/superdev')
    expect(store.installError).toBe('')
  })

  it('installSelectedMcp keeps mcp success when bundled skill install is degraded', async () => {
    vi.spyOn(mcpInstall, 'detectCodingAgents').mockResolvedValue([
      { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      { agent: 'codex', installed: false, detection_path: null },
      { agent: 'cursor', installed: false, detection_path: null },
    ])
    vi.spyOn(mcpInstall, 'installMcp').mockResolvedValue({
      agent: 'claude-code',
      installed: true,
      already_present: false,
      backup_path: null,
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
      skill: {
        installed: false,
        already_present: false,
        target_path: '/home/me/.claude/skills/superdev',
        backup_path: null,
        error: '找不到 SuperDev skill 资源目录，请检查桌面端打包配置',
      },
    })
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    await store.installSelectedMcp()

    expect(store.installOutcomes).toHaveLength(1)
    expect(store.installOutcomes[0]?.installed).toBe(true)
    expect(store.installOutcomes[0]?.skill.error).toContain('找不到 SuperDev skill')
    expect(store.installError).toBe('')
  })

  it('installSelectedMcp stores error text', async () => {
    vi.spyOn(mcpInstall, 'detectCodingAgents').mockResolvedValue([
      { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      { agent: 'codex', installed: false, detection_path: null },
      { agent: 'cursor', installed: false, detection_path: null },
    ])
    vi.spyOn(mcpInstall, 'installMcp').mockRejectedValue(new Error('配置文件格式异常'))
    vi.spyOn(mcpInstall, 'getMcpInstallHint').mockResolvedValue({
      agent: 'claude-code',
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{"superdev":{"command":"/app/superdev-mcp"}}}',
      skill_target_path: '/home/me/.claude/skills/superdev',
    })
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    await store.installSelectedMcp()

    expect(store.installError).toBe('Claude Code: 配置文件格式异常')
    expect(store.installFailures[0]?.hint?.config_path).toBe('/home/me/.claude.json')
  })

  it('demoPrompt guides approval flow', () => {
    const store = useOnboardingStore()

    expect(store.demoPrompt).toContain('superdev-sample')
    expect(store.demoPrompt).toContain('restart_service')
    expect(store.demoPrompt).toContain('安全审批')
  })
})
