/**
 * Desktop client build workflow contract tests.
 *
 * Responsibilities:
 *   - Verify the manual GitHub Actions workflow can build Windows and Linux clients.
 *   - Keep runner setup, Tauri packaging, and artifact upload steps visible to reviewers.
 *
 * Boundaries:
 *   - Does not execute GitHub Actions locally.
 *   - Does not validate signed release publishing; this workflow only uploads temporary artifacts.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';

const workflow = readFileSync('.github/workflows/build-desktop-clients.yml', 'utf8');

test('manual desktop client workflow exposes gh-friendly inputs', () => {
  assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /description:\s+Branch, tag, or SHA to build/);
  assert.match(workflow, /type:\s+choice/);
  assert.match(workflow, /windows-linux/);
  assert.match(workflow, /windows/);
  assert.match(workflow, /linux/);
});

test('manual desktop client workflow builds Linux and Windows packages', () => {
  assert.match(workflow, /Desktop Linux client/);
  assert.match(workflow, /runs-on:\s+ubuntu-22\.04/);
  assert.match(workflow, /libwebkit2gtk-4\.1-dev/);
  assert.match(workflow, /Desktop Windows client/);
  assert.match(workflow, /runs-on:\s+windows-latest/);
  assert.match(workflow, /BUILD_REMOTE_INSTALL=1 bash desktop\/scripts\/build-agent\.sh/);
  assert.match(workflow, /pnpm tauri build/);
});

test('manual desktop client workflow uploads downloadable artifacts', () => {
  assert.match(workflow, /actions\/upload-artifact@v4/);
  assert.match(workflow, /superdev-desktop-linux/);
  assert.match(workflow, /superdev-desktop-windows/);
  assert.match(workflow, /\.AppImage/);
  assert.match(workflow, /\.deb/);
  assert.match(workflow, /\.msi/);
  assert.match(workflow, /\.exe/);
});
