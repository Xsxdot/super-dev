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
import {
  chmodSync,
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { homedir } from 'node:os';

const DEFAULT_SOURCE_DIR = join(homedir(), '.superdev');
const DEFAULT_TARGET_DIR = join(homedir(), '.superdev-dev');
const DEFAULT_SSH_PORT = 22;
const DEFAULT_REMOTE_AGENT_PORT = 57017;

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

function main() {
  const opts = parseArgs(process.argv.slice(2));
  validateDirs(opts);

  const sourceHostsPath = join(opts.sourceDir, 'hosts.json');
  const sourceHosts = readHosts(sourceHostsPath);
  const summary = summarizeHosts(sourceHosts);

  printPlan(opts, summary);
  if (!opts.apply) {
    console.log('\nDry-run only. Re-run with --apply to write ~/.superdev-dev.');
    return;
  }

  const backupPath = backupTarget(opts.targetDir);
  copyDataDir(opts.sourceDir, opts.targetDir);
  const targetHostsPath = join(opts.targetDir, 'hosts.json');
  if (sourceHosts) {
    const migrated = sourceHosts.map(migrateHost);
    writeJSON(targetHostsPath, migrated, 0o600);
    console.log(`Converted hosts.json: ${migrated.length} host(s)`);
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

function summarizeHosts(hosts) {
  if (!hosts) {
    return { total: 0, legacy: 0, nested: 0, missing: true };
  }
  let legacy = 0;
  let nested = 0;
  for (const host of hosts) {
    if (isNestedHost(host)) {
      nested += 1;
    } else {
      legacy += 1;
    }
  }
  return { total: hosts.length, legacy, nested, missing: false };
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

function migrateHost(host) {
  const out = {
    id: stringValue(host.id),
    name: stringValue(host.name),
    tags: Array.isArray(host.tags) ? host.tags : [],
  };
  setIfNonEmpty(out, 'public_ip', host.public_ip);
  setIfNonEmpty(out, 'private_ip', host.private_ip);

  if (isNestedHost(host)) {
    out.agent = normalizeAgent(host.agent);
    return out;
  }

  out.agent = {
    transport: {
      type: 'tunnel',
      tunnel: normalizeTunnel({
        ssh_host: host.ssh_host,
        ssh_port: host.ssh_port,
        ssh_user: host.ssh_user,
        ssh_password: host.ssh_password,
        ssh_key_path: host.ssh_key_path,
        ssh_private_key: host.ssh_private_key,
        remote_agent_port: host.remote_agent_port,
      }),
    },
  };
  return out;
}

function isNestedHost(host) {
  return Boolean(host?.agent?.transport?.type);
}

function normalizeAgent(agent) {
  const transport = agent.transport ?? {};
  const out = {
    transport: {
      type: stringValue(transport.type || 'tunnel'),
    },
  };
  if (transport.tunnel || out.transport.type === 'tunnel') {
    out.transport.tunnel = normalizeTunnel(transport.tunnel ?? {});
  }
  if (transport.direct) {
    out.transport.direct = normalizeDirect(transport.direct);
  }
  return out;
}

function normalizeTunnel(tunnel) {
  const out = {
    ssh_host: stringValue(tunnel.ssh_host),
    ssh_port: numberValue(tunnel.ssh_port, DEFAULT_SSH_PORT),
    ssh_user: stringValue(tunnel.ssh_user),
    remote_agent_port: numberValue(tunnel.remote_agent_port, DEFAULT_REMOTE_AGENT_PORT),
  };
  setIfNonEmpty(out, 'ssh_password', tunnel.ssh_password);
  setIfNonEmpty(out, 'ssh_key_path', tunnel.ssh_key_path);
  setIfNonEmpty(out, 'ssh_private_key', tunnel.ssh_private_key);
  return out;
}

function normalizeDirect(direct) {
  const out = {};
  setIfNonEmpty(out, 'address', direct.address);
  return out;
}

function setIfNonEmpty(target, key, value) {
  const normalized = stringValue(value);
  if (normalized !== '') {
    target[key] = normalized;
  }
}

function stringValue(value) {
  return typeof value === 'string' ? value : '';
}

function numberValue(value, fallback) {
  return Number.isInteger(value) && value > 0 ? value : fallback;
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

try {
  main();
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
