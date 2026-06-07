#!/usr/bin/env node
/**
 * Formal hosts.json migration entrypoint.
 *
 * Responsibilities:
 *   - Convert legacy flat Host records into Host -> Agent -> Transport shape.
 *   - Support dry-run and apply modes for production ~/.superdev/hosts.json.
 *   - Back up the target file before applying changes.
 *
 * Boundaries:
 *   - Does not copy full SuperDev data directories.
 *   - Does not validate SSH credentials or connect to remote agents.
 *   - Does not mutate files unless --apply is provided.
 */
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const DEFAULT_HOSTS_PATH = join(homedir(), '.superdev', 'hosts.json');
const DEFAULT_SSH_PORT = 22;
const DEFAULT_REMOTE_AGENT_PORT = 57017;

export function migrateHostsFile({ source, target = source, apply = false, now = timestamp }) {
  const sourcePath = resolve(expandHome(source));
  const targetPath = resolve(expandHome(target));
  const hosts = JSON.parse(readFileSync(sourcePath, 'utf8'));
  if (!Array.isArray(hosts)) {
    throw new Error('hosts.json must be a JSON array');
  }

  const summary = summarize(hosts);
  if (!apply) {
    return { ...summary, source: sourcePath, target: targetPath, backup: '' };
  }

  mkdirSync(dirname(targetPath), { recursive: true });
  const backup = existsSync(targetPath) ? `${targetPath}.bak-${now()}` : '';
  if (backup) {
    copyFileSync(targetPath, backup);
  }
  const migrated = hosts.map(migrateHost);
  writeFileSync(targetPath, `${JSON.stringify(migrated, null, 2)}\n`, { mode: 0o600 });
  chmodSync(targetPath, 0o600);
  return { ...summary, source: sourcePath, target: targetPath, backup };
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
      chain: [{
        type: 'tunnel',
        tunnel: normalizeTunnel(host),
      }],
    },
  };
  return out;
}

function isNestedHost(host) {
  return Boolean(host?.agent?.transport?.type || host?.agent?.transport?.chain);
}

function normalizeAgent(agent) {
  const transport = agent?.transport ?? {};
  const chain = Array.isArray(transport.chain) && transport.chain.length > 0
    ? transport.chain.map(normalizeTransportEntry).filter(Boolean)
    : [normalizeTransportEntry({
      type: stringValue(transport.type || 'tunnel'),
      tunnel: transport.tunnel,
      direct: transport.direct,
    })].filter(Boolean);
  const out = {
    transport: { chain },
  };
  setIfNonEmpty(out, 'token', agent?.token);
  return out;
}

function normalizeTransportEntry(entry) {
  const type = stringValue(entry?.type || 'tunnel');
  if (type === 'tunnel') {
    return { type, tunnel: normalizeTunnel(entry?.tunnel ?? entry ?? {}) };
  }
  if (type === 'direct') {
    return { type, direct: normalizeDirect(entry?.direct ?? {}) };
  }
  return { type };
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
  setIfNonEmpty(out, 'ca_cert', direct.ca_cert);
  if (typeof direct.tls === 'boolean') {
    out.tls = direct.tls;
  }
  return out;
}

function summarize(hosts) {
  let legacy = 0;
  let nested = 0;
  for (const host of hosts) {
    if (isNestedHost(host)) {
      nested += 1;
    } else {
      legacy += 1;
    }
  }
  return { total: hosts.length, legacy, nested };
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

function expandHome(path) {
  if (path === '~') {
    return homedir();
  }
  if (path.startsWith('~/')) {
    return join(homedir(), path.slice(2));
  }
  return path;
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

function parseArgs(argv) {
  const opts = {
    apply: false,
    source: DEFAULT_HOSTS_PATH,
    target: '',
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case '--apply':
        opts.apply = true;
        break;
      case '--source':
        opts.source = requireValue(argv, ++i, arg);
        break;
      case '--target':
        opts.target = requireValue(argv, ++i, arg);
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
  if (!opts.target) {
    opts.target = opts.source;
  }
  return opts;
}

function requireValue(argv, index, flag) {
  const value = argv[index];
  if (!value || value.startsWith('--')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function usage() {
  console.log(`Usage:
  node scripts/migrate-hosts-json.mjs [--apply] [--source <hosts.json>] [--target <hosts.json>]

Defaults:
  --source ${DEFAULT_HOSTS_PATH}
  --target <same as --source>

By default this is a dry-run. Add --apply to back up and rewrite hosts.json.`);
}

function main(argv = process.argv.slice(2)) {
  const opts = parseArgs(argv);
  const result = migrateHostsFile(opts);
  console.log(`Source: ${result.source}`);
  console.log(`Target: ${result.target}`);
  console.log(`Mode:   ${opts.apply ? 'apply' : 'dry-run'}`);
  console.log(
    `hosts.json: ${result.total} host(s), ${result.legacy} legacy flat, ${result.nested} already nested`,
  );
  if (opts.apply) {
    if (result.backup) {
      console.log(`Backup: ${result.backup}`);
    }
    console.log('Migration applied.');
  } else {
    console.log('Dry-run only. Re-run with --apply to rewrite hosts.json.');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
