/** Connector diagnostics contract tests; no real bridge or backend is used. */
import { describe, expect, it, vi } from 'vitest'
import { emitConnectorDiagnostic } from '../connectorDiagnostics'

describe('connectorDiagnostics', () => {
  it('emits a stable connector scope without adding material fields', () => {
    const listener = vi.fn()
    window.addEventListener('superdev:connector', listener)

    emitConnectorDiagnostic('install.completed', 'info', {
      connectorId: 'fixture-json-agent', result: 'partial',
      capabilityResults: ['mcp=installed', 'skill=failed'],
    })

    const detail = (listener.mock.calls[0]?.[0] as CustomEvent).detail
    expect(detail).toMatchObject({
      scope: 'connector', level: 'info', event: 'install.completed',
      connectorId: 'fixture-json-agent', result: 'partial',
    })
    expect(JSON.stringify(detail)).not.toContain('mcpServers')
    window.removeEventListener('superdev:connector', listener)
  })
})
