#!/usr/bin/env node
/**
 * Copy production SuperDev data into the dev data directory.
 *
 * Responsibilities:
 *   - Copy ~/.superdev into ~/.superdev-dev for local verification.
 *   - Convert legacy flat hosts.json records into Host -> Agent -> Transport.
 *   - Back up any existing dev data before writing.
 *
 * Boundaries:
 *   - Never writes to the source directory.
 *   - Does not start or stop SuperDev agents.
 *   - Does not validate SSH credentials or connect to remote hosts.
 */
import { chmodSync, cpSync, existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { splitHostAndAgent, splitHostsAndAgents, summarizeHosts } from './migrate-hosts-json.mjs';

const DEFAULT_SOURCE_DIR = join(homedir(), '.superdev');
const DEFAULT_TARGET_DIR = join(homedir(), '.superdev-dev');

function usage() {
  console.log(`Usage:
  node scripts/copy-prod-data-to-dev.mjs [--apply] [--source-dir <dir>] [--target-dir <dir>]

Defaults:
  --source-dir ${DEFAULT_SOURCE_DIR}
  --target-dir ${DEFAULT_TARGET_DIR}

By default this is a dry-run. Add --apply to copy data and rewrite target hosts.json.`);
}

function parseArgs(argv) {
  const opts = {
    apply: false,
    sourceDir: DEFAULT_SOURCE_DIR,
    targetDir: DEFAULT_TARGET_DIR,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case '--apply':
        opts.apply = true;
        break;
      case '--source-dir':
        opts.sourceDir = requireValue(argv, ++i, arg);
        break;
      case '--target-dir':
        opts.targetDir = requireValue(argv, ++i, arg);
        break;
      case '--help':
      case '-h':
        usage();
        process.exit(0);
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  opts.sourceDir = resolve(expandHome(opts.sourceDir));
  opts.targetDir = resolve(expandHome(opts.targetDir));
  return opts;
}

function requireValue(argv, index, flag) {
  const value = argv[index];
  if (!value || value.startsWith('--')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function expandHome(path) {
  if (path === '~') {
    return homedir();
  }
  if (path.startsWith('~/')) {
    return join(homedir(), path.slice(2));
  }
  return path;
}

export function main() {
  const opts = parseArgs(process.argv.slice(2));
  validateDirs(opts);

  const sourceHostsPath = join(opts.sourceDir, 'hosts.json');
  const sourceAgentsPath = join(opts.sourceDir, 'agents.json');
  const sourceHosts = readHosts(sourceHostsPath);
  const sourceAgents = readAgents(sourceAgentsPath);
  const summary = sourceHosts
    ? { ...summarizeHosts(sourceHosts, sourceAgents), missing: false }
    : { total: 0, legacy: 0, nested: 0, missing: true };

  printPlan(opts, summary);
  if (!opts.apply) {
    console.log('\nDry-run only. Re-run with --apply to write ~/.superdev-dev.');
    return;
  }

  const backupPath = backupTarget(opts.targetDir);
  copyDataDir(opts.sourceDir, opts.targetDir);
  const targetHostsPath = join(opts.targetDir, 'hosts.json');
  if (sourceHosts) {
    const migrated = splitHostsAndAgents(sourceHosts, { existingAgents: sourceAgents });
    writeJSON(targetHostsPath, migrated.hosts, 0o600);
    writeJSON(join(opts.targetDir, 'agents.json'), migrated.agents, 0o600);
    console.log(`Converted hosts.json: ${migrated.hosts.length} host(s)`);
    console.log(`Converted agents.json: ${migrated.agents.length} agent(s)`);
  } else {
    console.log('No hosts.json found in source; copied data without host conversion.');
  }
  if (backupPath) {
    console.log(`Previous dev data backed up at: ${backupPath}`);
  }
  console.log(`Dev data is ready at: ${opts.targetDir}`);
}

function validateDirs(opts) {
  if (opts.sourceDir === opts.targetDir) {
    throw new Error('source-dir and target-dir must be different');
  }
  if (!existsSync(opts.sourceDir)) {
    throw new Error(`source directory does not exist: ${opts.sourceDir}`);
  }
  if (!statSync(opts.sourceDir).isDirectory()) {
    throw new Error(`source is not a directory: ${opts.sourceDir}`);
  }
}

function readHosts(hostsPath) {
  if (!existsSync(hostsPath)) {
    return null;
  }
  const parsed = JSON.parse(readFileSync(hostsPath, 'utf8'));
  if (!Array.isArray(parsed)) {
    throw new Error(`hosts.json must be a JSON array: ${hostsPath}`);
  }
  return parsed;
}

function readAgents(agentsPath) {
  if (!existsSync(agentsPath)) {
    return [];
  }
  const parsed = JSON.parse(readFileSync(agentsPath, 'utf8'));
  if (!Array.isArray(parsed)) {
    throw new Error(`agents.json must be a JSON array: ${agentsPath}`);
  }
  return parsed;
}

function printPlan(opts, summary) {
  console.log(`Source: ${opts.sourceDir}`);
  console.log(`Target: ${opts.targetDir}`);
  console.log(`Mode:   ${opts.apply ? 'apply' : 'dry-run'}`);
  if (summary.missing) {
    console.log('hosts.json: not found in source');
  } else {
    console.log(
      `hosts.json: ${summary.total} host(s), ${summary.legacy} legacy flat, ${summary.nested} already nested`,
    );
  }
  if (existsSync(opts.targetDir)) {
    console.log('Target exists: it will be moved to a timestamped backup before copy.');
  }
}

function backupTarget(targetDir) {
  if (!existsSync(targetDir)) {
    return '';
  }
  const backupPath = `${targetDir}.bak-${timestamp()}`;
  renameSync(targetDir, backupPath);
  return backupPath;
}

function copyDataDir(sourceDir, targetDir) {
  mkdirSync(dirname(targetDir), { recursive: true });
  cpSync(sourceDir, targetDir, {
    recursive: true,
    preserveTimestamps: true,
  });
}

export function migrateHostForDevCopy(host) {
  return splitHostAndAgent(host);
}

function writeJSON(path, value, mode) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { mode });
  chmodSync(path, mode);
}

function timestamp() {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return [
    now.getFullYear(),
    pad(now.getMonth() + 1),
    pad(now.getDate()),
    '-',
    pad(now.getHours()),
    pad(now.getMinutes()),
    pad(now.getSeconds()),
  ].join('');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
