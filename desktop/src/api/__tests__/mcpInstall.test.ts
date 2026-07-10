/**
 * Connector API client tests.
 *
 * Responsibility: protect the canonical open-ID Tauri command surface and payloads.
 * Boundary: no real Tauri command or local Agent configuration is touched.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { invoke } from '@tauri-apps/api/core'
import {
  getAgentConnectorManualInstructions,
  getGenericMcpConnectionMaterial,
  getMcpDocs,
  installAgentConnector,
  listAgentConnectors,
  uninstallAgentConnector,
  updateAgentConnector,
  verifyAgentConnector,
  type ConnectorOperationOutcome,
} from '@/api/mcpInstall'

vi.mock('@tauri-apps/api/core', () => ({ invoke: vi.fn() }))

const outcome: ConnectorOperationOutcome = {
  connector_id: 'fixture-json-agent',
  operation: 'install',
  result: 'partial',
  integrations: [
    { capability: 'mcp', result: 'installed' },
    { capability: 'skill', result: 'unsupported' },
    { capability: 'session_hook', result: 'unsupported' },
  ],
  requires_restart: false,
}

afterEach(() => vi.clearAllMocks())

describe('mcpInstall Connector API', () => {
  it('lists summaries with an unknown open connector id', async () => {
    vi.mocked(invoke).mockResolvedValue([{
      descriptor: {
        id: 'fixture-json-agent', display_name: 'Fixture JSON Agent', built_in: false,
        platforms: ['linux'], support_level: 'standard',
        integrations: [
          { capability: 'mcp', support: 'automatic' },
          { capability: 'skill', support: 'unsupported' },
          { capability: 'session_hook', support: 'unsupported' },
        ],
        operations: [{ operation: 'install', support: 'automatic' }],
      },
      state: { detected: true, integrations: [], requires_restart: false },
    }])

    const result = await listAgentConnectors()

    expect(result[0]?.descriptor.id).toBe('fixture-json-agent')
    expect(invoke).toHaveBeenCalledWith('list_agent_connectors')
  })

  it('passes canonical install/update retry payloads', async () => {
    vi.mocked(invoke).mockResolvedValue(outcome)

    await installAgentConnector('fixture-json-agent')
    await updateAgentConnector('fixture-json-agent', outcome)

    expect(invoke).toHaveBeenNthCalledWith(1, 'install_agent_connector', {
      connectorId: 'fixture-json-agent', previousOutcome: null,
    })
    expect(invoke).toHaveBeenNthCalledWith(2, 'update_agent_connector', {
      connectorId: 'fixture-json-agent', previousOutcome: outcome,
    })
  })

  it('routes verify, uninstall, and manual instructions by open id', async () => {
    vi.mocked(invoke).mockResolvedValue(outcome)

    await verifyAgentConnector('fixture-json-agent')
    await uninstallAgentConnector('fixture-json-agent')
    await getAgentConnectorManualInstructions('fixture-json-agent')

    expect(invoke).toHaveBeenNthCalledWith(1, 'verify_agent_connector', { connectorId: 'fixture-json-agent' })
    expect(invoke).toHaveBeenNthCalledWith(2, 'uninstall_agent_connector', { connectorId: 'fixture-json-agent' })
    expect(invoke).toHaveBeenNthCalledWith(3, 'agent_connector_manual_instructions', { connectorId: 'fixture-json-agent' })
  })

  it('reads generic local material and shared docs', async () => {
    vi.mocked(invoke)
      .mockResolvedValueOnce({ transport: 'stdio', command: '/app/superdev-mcp', agent_url: 'http://127.0.0.1:57017', manual_config: '{"mcpServers":{}}' })
      .mockResolvedValueOnce({
        summary_sections: [{ id: 'logs', title: 'Logs', description: 'Read logs', tools: [] }],
        documents: [{ id: 'skill', title: 'SKILL.md', path: 'SKILL.md', content: '# SuperDev' }],
      })

    const material = await getGenericMcpConnectionMaterial()
    const docs = await getMcpDocs()

    expect(material.transport).toBe('stdio')
    expect(docs.documents[0]?.title).toBe('SKILL.md')
    expect(invoke).toHaveBeenNthCalledWith(1, 'generic_mcp_connection_material')
    expect(invoke).toHaveBeenNthCalledWith(2, 'mcp_docs')
  })
})
