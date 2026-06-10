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
const certificateTabPath = `${process.cwd()}/src/components/Settings/CertificateTab.vue`
const overviewTabsPath = `${process.cwd()}/src/components/Overview/OverviewTabs.vue`
const pipelinesTabPath = `${process.cwd()}/src/components/Overview/PipelinesTab.vue`
const pipelineRowPath = `${process.cwd()}/src/components/Overview/PipelineRow.vue`
const projectPipelineEditorPath = `${process.cwd()}/src/components/Settings/ProjectPipelineEditor.vue`
const pipelineWizardPath = `${process.cwd()}/src/components/Settings/PipelineTemplateWizard.vue`
const singlePipelineFormPath = `${process.cwd()}/src/components/Settings/SingleProjectPipelineForm.vue`

function css() {
  return readFileSync(stylePath, 'utf8') as string
}

function certificateTabSource() {
  return readFileSync(certificateTabPath, 'utf8') as string
}

function overviewTabsSource() {
  return readFileSync(overviewTabsPath, 'utf8') as string
}

function pipelinesTabSource() {
  return readFileSync(pipelinesTabPath, 'utf8') as string
}

function pipelineRowSource() {
  return readFileSync(pipelineRowPath, 'utf8') as string
}

function projectPipelineEditorSource() {
  return readFileSync(projectPipelineEditorPath, 'utf8') as string
}

function pipelineWizardSource() {
  return readFileSync(pipelineWizardPath, 'utf8') as string
}

function singlePipelineFormSource() {
  return readFileSync(singlePipelineFormPath, 'utf8') as string
}

function expectRule(source: string, selector: string, declarations: string[]) {
  for (const declaration of declarations) {
    expect(source).toMatch(new RegExp(`${selector}\\s*\\{[^}]*${declaration}`, 's'))
  }
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

  it('centers the responsive settings pane inside the main content area', () => {
    const source = css()

    expect(source).toMatch(/\.settings-pane\s*\{[^}]*width:\s*min\(1200px,\s*calc\(100%\s*-\s*48px\)\);/s)
    expect(source).toMatch(/\.settings-pane\s*\{[^}]*margin:\s*0 auto;/s)
  })

  it('keeps the ACME account save action on the first form row', () => {
    const source = certificateTabSource()

    expect(source).toMatch(/\.account-grid\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*1fr\)\s+auto;/s)
    expect(source).toMatch(/\.save-account\s*\{[^}]*grid-area:\s*save;/s)
  })

  it('keeps the pipeline wizard step bar above an adaptive-left editor body', () => {
    const source = pipelineWizardSource()

    expect(source).toMatch(/\.pipeline-wizard\s*\{[^}]*grid-template-columns:\s*minmax\(360px,\s*1fr\)\s+minmax\(560px,\s*2fr\);/s)
    expect(source).toMatch(/\.phase-tabs\s*\{[^}]*overflow-x:\s*auto;/s)
    expect(source).toMatch(/\.wizard-detail-panel\s*\{[^}]*grid-row:\s*2;/s)
    expect(source).toMatch(/\.block-row\s*\{[^}]*grid-template-columns:\s*18px\s+24px\s+auto\s+minmax\(0,\s*1fr\)\s+auto\s+auto;/s)
    expect(source).toMatch(/\.block-order\s*\{[^}]*grid-row:\s*1;/s)
    expect(source).toMatch(/\.block-row\s+\.text-btn\s*\{[^}]*grid-row:\s*1;/s)
    expect(source).toMatch(/\.block-row\s+\.danger-btn\s*\{[^}]*grid-row:\s*1;/s)
    expect(source).toMatch(/@media\s*\(max-width:\s*960px\)/s)
  })

  it('keeps pipeline base accordion above the phase wizard', () => {
    const source = singlePipelineFormSource()

    expect(source).toMatch(/\.single-pipeline-form\s*\{[^}]*grid-template-rows:\s*auto\s+auto\s+minmax\(0,\s*1fr\);/s)
    expect(source).toContain('single-pipeline-config-stack')
    expect(source).toContain('pipeline-config-toggle-build')
    expect(source).toContain('pipeline-config-toggle-variables')
    expect(source).toContain('pipeline-config-toggle-deploy')
    expect(source).not.toContain('<template #base>')
  })

  it('keeps pipeline table typography on the shared compact scale without the removed overview card', () => {
    const tab = pipelinesTabSource()
    const row = pipelineRowSource()

    expectRule(tab, '\\.pipeline-console-title', ['font-size:\\s*20px;', 'font-weight:\\s*700;'])
    expectRule(tab, '\\.pipeline-console-subtitle', ['font-size:\\s*12px;', 'font-weight:\\s*500;'])
    expectRule(tab, '\\.pipeline-table-head', ['font-size:\\s*12px;', 'font-weight:\\s*500;'])
    expect(tab).not.toContain('pipeline-overview-card')
    expectRule(row, '\\.pipeline-name', ['font-size:\\s*13px;', 'font-weight:\\s*650;'])
    expectRule(row, '\\.service-tag', ['font-size:\\s*12px;'])
    expectRule(row, '\\.pipeline-status', ['font-size:\\s*12px;', 'font-weight:\\s*600;'])
  })

  it('keeps project overview tabs compact in the page header', () => {
    const source = overviewTabsSource()

    expectRule(source, '\\.overview-tabs', ['width:\\s*min\\(330px,\\s*100%\\);', 'height:\\s*44px;', 'padding:\\s*3px;'])
    expectRule(source, '\\.overview-tabs button', ['height:\\s*36px;', 'padding:\\s*0 12px;', 'font-size:\\s*13px;', 'font-weight:\\s*650;'])
  })

  it('keeps pipeline editor form typography aligned with settings controls', () => {
    const editor = projectPipelineEditorSource()
    const form = singlePipelineFormSource()
    const wizard = pipelineWizardSource()

    expectRule(editor, '\\.pipeline-editor-heading \\.settings-modal-title', ['font-size:\\s*14px;', 'font-weight:\\s*650;'])
    expectRule(editor, '\\.pipeline-editor-project-badge', ['font-size:\\s*12px;', 'font-weight:\\s*650;'])
    expectRule(editor, '\\.pipeline-editor-footer-status', ['font-size:\\s*12px;', 'font-weight:\\s*500;'])
    expectRule(form, '\\.field-row', ['font-size:\\s*11px;', 'font-weight:\\s*500;'])
    expectRule(form, '\\.service-item', ['font-size:\\s*12px;', 'font-weight:\\s*500;'])
    expectRule(form, '\\.artifact-segment button', ['font-size:\\s*12px;', 'font-weight:\\s*600;'])
    expectRule(wizard, '\\.phase-tabs button', ['font-size:\\s*12px;', 'font-weight:\\s*600;'])
    expectRule(wizard, '\\.detail-title', ['font-size:\\s*14px;', 'font-weight:\\s*650;'])
    expectRule(wizard, '\\.field-label', ['font-size:\\s*11px;', 'font-weight:\\s*500;'])
    expectRule(wizard, '\\.form-block h4', ['font-size:\\s*12px;', 'font-weight:\\s*600;'])
  })
})
