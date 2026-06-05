/**
 * McpManagerTab 测试设置页 MCP 管理能力。
 *
 * 职责：
 *   - 验证 MCP 状态、安装更新、卸载和手动配置入口
 *   - 验证 MCP 功能说明和 skill 文档查看
 *
 * 边界：
 *   - 不调用真实 Tauri command
 *   - 不读写真实 Agent 配置或 skill 文件
 */
import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ask } from '@tauri-apps/plugin-dialog'
import McpManagerTab from '@/components/Settings/McpManagerTab.vue'
import { installTestI18n } from '@/test-utils/i18n'
import {
  getMcpDocs,
  getMcpInstallHint,
  getMcpStatus,
  installMcp,
  uninstallMcp,
  type McpDocs,
  type McpStatus,
} from '@/api/mcpInstall'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  ask: vi.fn(),
}))

vi.mock('@/api/mcpInstall', () => ({
  getMcpStatus: vi.fn(),
  installMcp: vi.fn(),
  uninstallMcp: vi.fn(),
  getMcpInstallHint: vi.fn(),
  getMcpDocs: vi.fn(),
}))

const codexStatus: McpStatus = {
  agent: 'codex',
  agent_installed: true,
  detection_path: '/opt/homebrew/bin/codex',
  config_path: '/Users/alice/.codex/config.toml',
  config_exists: true,
  mcp_configured: true,
  mcp_command: '/Applications/SuperDev/superdev-mcp',
  agent_url: 'http://127.0.0.1:57017',
  config_error: null,
  skill_path: '/Users/alice/.codex/skills/superdev',
  skill_installed: true,
  skill_matches_bundled: true,
  skill_error: null,
}

const missingClaudeStatus: McpStatus = {
  agent: 'claude-code',
  agent_installed: false,
  detection_path: null,
  config_path: '/Users/alice/.claude.json',
  config_exists: false,
  mcp_configured: false,
  mcp_command: null,
  agent_url: null,
  config_error: null,
  skill_path: '/Users/alice/.claude/skills/superdev',
  skill_installed: false,
  skill_matches_bundled: false,
  skill_error: null,
}

const cursorStatus: McpStatus = {
  agent: 'cursor',
  agent_installed: true,
  detection_path: '/Applications/Cursor.app',
  config_path: '/Users/alice/.cursor/mcp.json',
  config_exists: true,
  mcp_configured: false,
  mcp_command: null,
  agent_url: null,
  config_error: null,
  skill_path: '/Users/alice/.cursor/skills/superdev',
  skill_installed: false,
  skill_matches_bundled: false,
  skill_error: null,
}

const docs: McpDocs = {
  summary_sections: [{
    id: 'logs',
    title: '日志与诊断',
    description: '读取日志、搜索错误、采集诊断证据和 trace 线索。',
    tools: [
      { name: 'tail_logs', purpose: '查看近期日志', access: '读', reference: 'references/log-tools.md' },
      { name: 'restart_service', purpose: '重启 deployment', access: '写, 需审批纪律', reference: 'references/safe-operations.md' },
    ],
  }],
  documents: [
    { id: 'skill', title: 'SKILL.md', path: '/bundle/SKILL.md', content: '# SuperDev MCP 使用指南' },
    { id: 'references/log-tools.md', title: 'log-tools.md', path: '/bundle/references/log-tools.md', content: '# Log Tools\n`tail_logs`' },
  ],
}

async function mountTab() {
  const wrapper = mount(McpManagerTab, {
    global: { plugins: [installTestI18n('zh-CN')] },
  })
  await flushPromises()
  return wrapper
}

describe('McpManagerTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getMcpStatus).mockResolvedValue([codexStatus, missingClaudeStatus, cursorStatus])
    vi.mocked(getMcpDocs).mockResolvedValue(docs)
    vi.mocked(installMcp).mockResolvedValue({
      agent: 'codex',
      installed: false,
      already_present: true,
      config_path: codexStatus.config_path,
      manual_config: '',
      backup_path: null,
      skill: {
        installed: false,
        already_present: true,
        target_path: codexStatus.skill_path,
        backup_path: null,
        error: null,
      },
    })
    vi.mocked(uninstallMcp).mockResolvedValue({
      agent: 'codex',
      config_path: codexStatus.config_path,
      removed_config: true,
      config_backup_path: '/Users/alice/.codex/config.toml.superdev-bak',
      skill_path: codexStatus.skill_path,
      removed_skill: true,
    })
    vi.mocked(getMcpInstallHint).mockResolvedValue({
      agent: 'codex',
      config_path: codexStatus.config_path,
      manual_config: '[mcp_servers.superdev]\ncommand = "/Applications/SuperDev/superdev-mcp"\n',
      skill_target_path: codexStatus.skill_path,
    })
    vi.mocked(ask).mockResolvedValue(true)
  })

  it('展示 Agent MCP 状态和共享设置样式', async () => {
    const wrapper = await mountTab()

    expect(wrapper.find('.settings-pane-header').exists()).toBe(true)
    expect(wrapper.find('.settings-card-list').exists()).toBe(true)
    expect(wrapper.text()).toContain('Codex')
    expect(wrapper.text()).toContain('/Users/alice/.codex/config.toml')
    expect(wrapper.text()).toContain('/Applications/SuperDev/superdev-mcp')
    expect(wrapper.text()).toContain('已配置')
    expect(wrapper.text()).toContain('未检测到')
    expect(wrapper.find('[data-test="mcp-install-claude-code"]').attributes('disabled')).toBeDefined()
  })

  it('安装更新后刷新状态', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-install-codex"]').trigger('click')
    await flushPromises()

    expect(installMcp).toHaveBeenCalledWith('codex')
    expect(getMcpStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('已是最新')
  })

  it('卸载前确认并在确认后刷新状态', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-uninstall-codex"]').trigger('click')
    await flushPromises()

    expect(ask).toHaveBeenCalled()
    expect(uninstallMcp).toHaveBeenCalledWith('codex')
    expect(getMcpStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('已卸载')
  })

  it('取消卸载时不调用卸载 API', async () => {
    vi.mocked(ask).mockResolvedValue(false)
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-uninstall-codex"]').trigger('click')
    await flushPromises()

    expect(uninstallMcp).not.toHaveBeenCalled()
  })

  it('展示手动配置片段', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-manual-codex"]').trigger('click')
    await flushPromises()

    expect(getMcpInstallHint).toHaveBeenCalledWith('codex')
    expect(wrapper.text()).toContain('[mcp_servers.superdev]')
    expect(wrapper.text()).toContain(codexStatus.skill_path)
  })

  it('展示 MCP 功能说明并可切换 skill 文档', async () => {
    const wrapper = await mountTab()

    expect(wrapper.text()).toContain('日志与诊断')
    expect(wrapper.text()).toContain('tail_logs')
    expect(wrapper.text()).toContain('写, 需审批纪律')

    await wrapper.find('[data-test="mcp-doc-references/log-tools.md"]').trigger('click')

    expect(wrapper.find('[data-test="mcp-doc-content"]').text()).toContain('# Log Tools')
    expect(wrapper.find('[data-test="mcp-doc-content"]').text()).toContain('tail_logs')
  })
})
