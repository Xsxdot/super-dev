/**
 * Dynamic Connector onboarding store tests.
 *
 * Responsibility: protect open IDs, MCP truth, retry retention, and safe diagnostics.
 * Boundary: all Tauri calls are mocked; no local configuration is touched.
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as api from '@/api/mcpInstall'
import { setLocale } from '@/i18n'
import { hasWorkingMcp, useOnboardingStore } from '../onboarding'
import type { AgentConnectorSummary, ConnectorOperationOutcome } from '@/api/mcpInstall'

const diagnosticMock = vi.hoisted(() => vi.fn())
vi.mock('@/lib/connectorDiagnostics', () => ({ emitConnectorDiagnostic: diagnosticMock }))

function summary(id: string, detected: boolean, builtIn = true): AgentConnectorSummary {
  return {
    descriptor: {
      id,
      display_name: id === 'fixture-json-agent' ? 'Fixture JSON Agent' : id,
      built_in: builtIn,
      platforms: ['macos'],
      support_level: id === 'fixture-json-agent' ? 'standard' : 'full',
      integrations: [
        { capability: 'mcp', support: 'automatic' },
        { capability: 'skill', support: id === 'fixture-json-agent' ? 'unsupported' : 'automatic' },
        { capability: 'session_hook', support: id === 'fixture-json-agent' ? 'unsupported' : 'automatic' },
      ],
      operations: [
        { operation: 'install', support: 'automatic' },
        { operation: 'update', support: 'automatic' },
        { operation: 'verify', support: 'automatic' },
        { operation: 'uninstall', support: 'automatic' },
      ],
    },
    state: {
      detected,
      detection_path: detected ? `/bin/${id}` : null,
      integrations: [
        { capability: 'mcp', status: 'missing' },
        { capability: 'skill', status: 'missing' },
        { capability: 'session_hook', status: 'missing' },
      ],
      requires_restart: false,
    },
  }
}

function outcome(
  id: string,
  result: ConnectorOperationOutcome['result'],
  mcp: ConnectorOperationOutcome['integrations'][number]['result'],
): ConnectorOperationOutcome {
  return {
    connector_id: id,
    operation: 'install',
    result,
    integrations: [
      { capability: 'mcp', result: mcp },
      { capability: 'skill', result: result === 'partial' ? 'failed' : 'unsupported' },
      { capability: 'session_hook', result: 'unsupported' },
    ],
    requires_restart: false,
  }
}

describe('onboardingStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale('zh-CN')
    vi.restoreAllMocks()
    diagnosticMock.mockReset()
    window.history.replaceState({}, '', '/')
  })

  it('retains registry order and selects every detected open id', async () => {
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue([
      summary('claude-code', true),
      summary('fixture-json-agent', true, false),
      summary('cursor', false),
    ])
    const store = useOnboardingStore()

    await store.detectInstalledAgents()

    expect(store.connectors.map(item => item.descriptor.id)).toEqual([
      'claude-code', 'fixture-json-agent', 'cursor',
    ])
    expect(store.selectedAgents).toEqual(['claude-code', 'fixture-json-agent'])
    expect(store.isAgentInstalled('cursor')).toBe(false)
  })

  it('does not select an undetected connector', async () => {
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue([summary('cursor', false)])
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    store.toggleAgentSelection('cursor')

    expect(store.selectedAgents).toEqual([])
  })

  it('uses working MCP as the only completion truth', () => {
    expect(hasWorkingMcp(outcome('ok', 'partial', 'installed'))).toBe(true)
    expect(hasWorkingMcp(outcome('bad', 'failed', 'failed'))).toBe(false)
    expect(hasWorkingMcp(outcome('existing', 'unchanged', 'already_present'))).toBe(true)
  })

  it('installs selected connectors with canonical outcomes and refreshes state', async () => {
    const summaries = [summary('fixture-json-agent', true, false)]
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue(summaries)
    vi.spyOn(api, 'installAgentConnector').mockResolvedValue(outcome('fixture-json-agent', 'success', 'installed'))
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    await store.installSelectedMcp()

    expect(api.installAgentConnector).toHaveBeenCalledWith('fixture-json-agent', undefined)
    expect(store.installOutcomes[0]?.connector_id).toBe('fixture-json-agent')
    expect(store.installError).toBe('')
    expect(api.listAgentConnectors).toHaveBeenCalledTimes(2)
  })

  it('keeps successful outcomes and retries only partial work with its previous outcome', async () => {
    const summaries = [summary('first', true), summary('second', true)]
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue(summaries)
    const firstSuccess = outcome('first', 'success', 'installed')
    const secondPartial = outcome('second', 'partial', 'installed')
    const secondSuccess = outcome('second', 'success', 'already_present')
    vi.spyOn(api, 'installAgentConnector')
      .mockResolvedValueOnce(firstSuccess)
      .mockResolvedValueOnce(secondPartial)
      .mockResolvedValueOnce(secondSuccess)
    const store = useOnboardingStore()
    await store.detectInstalledAgents()
    await store.installSelectedMcp()

    await store.installSelectedMcp()

    expect(api.installAgentConnector).toHaveBeenCalledTimes(3)
    expect(api.installAgentConnector).toHaveBeenLastCalledWith('second', secondPartial)
    expect(store.installOutcomes.map(item => [item.connector_id, item.result])).toEqual([
      ['first', 'success'], ['second', 'success'],
    ])
  })

  it('returns manual instructions when an operation cannot form an outcome', async () => {
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue([summary('fixture-json-agent', true, false)])
    vi.spyOn(api, 'installAgentConnector').mockRejectedValue(new Error('config damaged'))
    vi.spyOn(api, 'getAgentConnectorManualInstructions').mockResolvedValue({
      summary: 'Configure manually',
      steps: ['Open the official Agent settings'],
      manual_config: '{"mcpServers":{}}',
    })
    const store = useOnboardingStore()
    await store.detectInstalledAgents()

    await store.installSelectedMcp()

    expect(store.installError).toContain('Fixture JSON Agent: config damaged')
    expect(store.installHint?.manual_config).toContain('mcpServers')
  })

  it('isolates list failure and removes stale selection', async () => {
    vi.spyOn(api, 'listAgentConnectors').mockRejectedValue(new Error('registry unavailable'))
    const store = useOnboardingStore()
    store.selectedAgents = ['stale']

    await store.detectInstalledAgents()

    expect(store.connectors).toEqual([])
    expect(store.selectedAgents).toEqual([])
    expect(store.detectionError).toBe('registry unavailable')
  })

  it('emits structured identifiers without config paths or manual material', async () => {
    vi.spyOn(api, 'listAgentConnectors').mockResolvedValue([summary('fixture-json-agent', true, false)])
    vi.spyOn(api, 'installAgentConnector').mockResolvedValue(outcome('fixture-json-agent', 'partial', 'installed'))
    const store = useOnboardingStore()
    await store.detectInstalledAgents()
    await store.installSelectedMcp()

    const serialized = JSON.stringify(diagnosticMock.mock.calls)
    expect(serialized).toContain('fixture-json-agent')
    expect(serialized).not.toContain('/bin/')
    expect(serialized).not.toContain('mcpServers')
  })
})
