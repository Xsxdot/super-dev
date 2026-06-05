/**
 * Settings style contract tests the shared settings UI CSS surface.
 *
 * Responsibilities:
 *   - Verify settings tokens used by components exist
 *   - Verify shared settings classes are defined globally
 *
 * Boundaries:
 *   - Does not perform visual rendering checks
 *   - Does not assert exact color values except alias presence
 */
import { describe, expect, it } from 'vitest'
// @ts-expect-error Vitest runs this contract in Node, while app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs'

declare const process: { cwd: () => string }

const stylePath = `${process.cwd()}/src/style.css`

function css() {
  return readFileSync(stylePath, 'utf8') as string
}

describe('settings style contract', () => {
  it('defines tokens consumed by settings components', () => {
    const source = css()

    expect(source).toContain('--bg-secondary:')
    expect(source).toContain('--border-muted:')
    expect(source).toContain('--danger:')
    expect(source).toContain('--warning:')
    expect(source).toContain('--success:')
  })

  it('defines shared settings workbench classes globally', () => {
    const source = css()
    const requiredClasses = [
      '.settings-shell',
      '.settings-sidebar',
      '.settings-main',
      '.settings-pane',
      '.settings-pane-header',
      '.settings-toolbar',
      '.settings-surface',
      '.settings-section',
      '.settings-table',
      '.settings-card-list',
      '.settings-card',
      '.settings-empty',
      '.settings-alert',
      '.settings-field',
      '.settings-modal-backdrop',
      '.settings-modal',
      '.settings-modal-footer',
      '.settings-btn',
      '.settings-btn-primary',
      '.settings-btn-danger',
      '.settings-btn-text',
      '.settings-badge',
    ]

    for (const className of requiredClasses) {
      expect(source).toContain(className)
    }
  })
})
