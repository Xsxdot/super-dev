/**
 * Release metadata version check.
 *
 * Responsibilities:
 *   - Treat VERSION as the repository version source of truth.
 *   - Verify package metadata that ships in desktop builds uses the same version.
 *
 * Boundaries:
 *   - Does not bump versions or write files.
 *   - Does not validate changelog content or Git tags.
 */
import { readFileSync } from 'node:fs';

const expected = readFileSync('VERSION', 'utf8').trim();

function fail(message) {
  console.error(message);
  process.exitCode = 1;
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

const desktopPackage = readJSON('desktop/package.json');
const tauriConfig = readJSON('desktop/src-tauri/tauri.conf.json');
const cargoToml = readFileSync('desktop/src-tauri/Cargo.toml', 'utf8');
const cargoVersion = cargoToml.match(/^\[package\][\s\S]*?^version\s*=\s*"([^"]+)"/m)?.[1];
const agentBuildInfo = readFileSync('agent/internal/buildinfo/version.go', 'utf8');
const agentVersion = agentBuildInfo.match(/^const Version = "([^"]+)"/m)?.[1];

const checks = [
  ['desktop/package.json', desktopPackage.version],
  ['desktop/src-tauri/tauri.conf.json', tauriConfig.version],
  ['desktop/src-tauri/Cargo.toml', cargoVersion],
  ['agent/internal/buildinfo/version.go', agentVersion],
];

for (const [path, actual] of checks) {
  if (actual !== expected) {
    fail(`${path} version ${actual ?? '<missing>'} does not match VERSION ${expected}`);
  }
}

if (!process.exitCode) {
  console.log(`Version metadata is consistent at ${expected}`);
}
