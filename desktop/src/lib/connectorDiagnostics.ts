/**
 * Connector diagnostics emits one safe event shape for onboarding and settings operations.
 *
 * Responsibility: make list/install/update/verify/uninstall/manual operations observable.
 * Boundary: callers may pass identifiers and enum results only; never paths, material, or errors.
 */
export type ConnectorDiagnosticLevel = 'debug' | 'info' | 'warn' | 'error'

/** emitConnectorDiagnostic dispatches a structured, locally buffered Connector event. */
export function emitConnectorDiagnostic(
  event: string,
  level: ConnectorDiagnosticLevel,
  context: Record<string, unknown> = {},
) {
  window.dispatchEvent(new CustomEvent('superdev:connector', {
    detail: {
      ...context,
      scope: 'connector',
      level,
      event,
      at: new Date().toISOString(),
    },
  }))
}
