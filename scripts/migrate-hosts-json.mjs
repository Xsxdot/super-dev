#!/usr/bin/env node
/**
 * Formal hosts.json migration entrypoint.
 *
 * Responsibilities:
 *   - Split legacy hosts.json records into Host records plus agents.json records.
 *   - Support dry-run and apply modes for production ~/.superdev data.
 *   - Back up target files before applying changes.
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
const DEFAULT_AGENT_LISTEN_PORT = 57017;

export function migrateHostsFile({
  source,
  target = source,
  agentsSource = '',
  agentsTarget = '',
  apply = false,
  now = timestamp,
}) {
  const sourcePath = resolve(expandHome(source));
  const targetPath = resolve(expandHome(target));
  const sourceAgentsPath = agentsSource
    ? resolve(expandHome(agentsSource))
    : join(dirname(sourcePath), 'agents.json');
  const targetAgentsPath = agentsTarget
    ? resolve(expandHome(agentsTarget))
    : join(dirname(targetPath), 'agents.json');

  const hosts = readHosts(sourcePath);
  const existingAgents = readAgentsIfPresent(sourceAgentsPath);
  const summary = summarizeHosts(hosts, existingAgents);
  if (!apply) {
    return {
      ...summary,
      source: sourcePath,
      target: targetPath,
      agentsSource: sourceAgentsPath,
      agentsTarget: targetAgentsPath,
      backup: '',
      agentsBackup: '',
    };
  }

  mkdirSync(dirname(targetPath), { recursive: true });
  mkdirSync(dirname(targetAgentsPath), { recursive: true });
  const backup = backupFileIfPresent(targetPath, now);
  const agentsBackup = backupFileIfPresent(targetAgentsPath, now);
  const migrated = splitHostsAndAgents(hosts, { existingAgents });
  writeJSON(targetPath, migrated.hosts, 0o600);
  writeJSON(targetAgentsPath, migrated.agents, 0o600);
  return {
    ...summary,
    source: sourcePath,
    target: targetPath,
    agentsSource: sourceAgentsPath,
    agentsTarget: targetAgentsPath,
    backup,
    agentsBackup,
  };
}

export function splitHostsAndAgents(hosts, { existingAgents = [] } = {}) {
  const migratedHosts = [];
  const migratedAgents = [];
  const existingByHostID = new Map();
  for (const agent of existingAgents) {
    const normalized = normalizeExistingAgent(agent);
    if (normalized.host_id) {
      existingByHostID.set(normalized.host_id, normalized);
    }
  }

  for (const host of hosts) {
    const hostID = stringValue(host?.id);
    const hostRecord = normalizeHost(host);
    migratedHosts.push(hostRecord);

    if (isNestedHost(host)) {
      migratedAgents.push(normalizeLegacyAgent(host, hostID));
      continue;
    }

    const existingAgent = existingByHostID.get(hostID);
    if (existingAgent) {
      migratedAgents.push(existingAgent);
      continue;
    }

    migratedAgents.push(normalizeLegacyAgent(host, hostID));
  }

  return { hosts: migratedHosts, agents: migratedAgents };
}

export function splitHostAndAgent(host) {
  const split = splitHostsAndAgents([host]);
  return { host: split.hosts[0], agent: split.agents[0] };
}

export function summarizeHosts(hosts, existingAgents = []) {
  let legacy = 0;
  let nested = 0;
  for (const host of hosts) {
    if (isNestedHost(host)) {
      nested += 1;
      continue;
    }
    if (existingAgents.length === 0) {
      legacy += 1;
    }
  }
  return { total: hosts.length, legacy, nested };
}

export function isNestedHost(host) {
  return Boolean(host?.agent?.transport?.type || host?.agent?.transport?.chain);
}

function readHosts(hostsPath) {
  const hosts = JSON.parse(readFileSync(hostsPath, 'utf8'));
  if (!Array.isArray(hosts)) {
    throw new Error('hosts.json must be a JSON array');
  }
  return hosts;
}

function readAgentsIfPresent(agentsPath) {
  if (!existsSync(agentsPath)) {
    return [];
  }
  const agents = JSON.parse(readFileSync(agentsPath, 'utf8'));
  if (!Array.isArray(agents)) {
    throw new Error(`agents.json must be a JSON array: ${agentsPath}`);
  }
  return agents;
}

function backupFileIfPresent(path, now) {
  if (!existsSync(path)) {
    return '';
  }
  const backup = `${path}.bak-${now()}`;
  copyFileSync(path, backup);
  return backup;
}

function normalizeHost(host) {
  const tunnel = firstTunnelSource(host);
  const out = {
    id: stringValue(host?.id),
    name: stringValue(host?.name),
    tags: Array.isArray(host?.tags) ? host.tags : [],
  };
  setIfNonEmpty(out, 'public_ip', host?.public_ip);
  setIfNonEmpty(out, 'private_ip', host?.private_ip);
  setIfNonEmpty(out, 'ssh_host', firstNonEmpty(host?.ssh_host, tunnel?.ssh_host));
  setIfNonEmpty(out, 'ssh_user', firstNonEmpty(host?.ssh_user, tunnel?.ssh_user));
  setIfNonEmpty(out, 'ssh_password', firstNonEmpty(host?.ssh_password, tunnel?.ssh_password));

  const port = firstPositiveInteger(host?.ssh_port, tunnel?.ssh_port);
  const hasSSHIdentity = out.ssh_host || out.ssh_user || out.ssh_password || host?.ssh_private_key || tunnel?.ssh_private_key;
  if (port > 0) {
    out.ssh_port = port;
  } else if (hasSSHIdentity) {
    out.ssh_port = DEFAULT_SSH_PORT;
  }

  const keyContent = firstNonEmpty(
    host?.ssh_private_key,
    tunnel?.ssh_private_key,
    readPrivateKeyContent(firstNonEmpty(host?.ssh_key_path, tunnel?.ssh_key_path)),
  );
  setIfNonEmpty(out, 'ssh_private_key', keyContent);
  return out;
}

function normalizeLegacyAgent(host, hostID) {
  const rawEntries = legacyTransportEntries(host);
  const chain = rawEntries.map(normalizeTransportEntry).filter(Boolean);
  const normalizedChain = chain.length > 0 ? chain : [normalizeTransportEntry({ type: 'tunnel', tunnel: host })];
  const token = firstNonEmpty(host?.agent?.secret?.token, host?.agent?.token);
  const agent = {
    host_id: hostID,
    transport: { chain: normalizedChain },
    config: normalizeAgentConfig(host?.agent?.config, normalizedChain),
    security: normalizeAgentSecurity(host?.agent, rawEntries, token),
  };
  if (token) {
    agent.secret = { token };
  }
  return agent;
}

function normalizeExistingAgent(agent) {
  const rawEntries = agentTransportEntries(agent);
  const chain = rawEntries.map(normalizeTransportEntry).filter(Boolean);
  const normalizedChain = chain.length > 0 ? chain : [{ type: 'tunnel', tunnel: { remote_agent_port: DEFAULT_REMOTE_AGENT_PORT } }];
  const token = firstNonEmpty(agent?.secret?.token, agent?.token);
  const out = {
    host_id: stringValue(agent?.host_id),
    transport: { chain: normalizedChain },
    config: normalizeAgentConfig(agent?.config, normalizedChain),
    security: normalizeAgentSecurity(agent, rawEntries, token),
  };
  if (token) {
    out.secret = { token };
  }
  return out;
}

function normalizeAgentConfig(config, chain) {
  const out = {};
  setIfNonEmpty(out, 'listen_address', config?.listen_address);
  const listenPort = firstPositiveInteger(config?.listen_port, firstTunnelPort(chain), DEFAULT_AGENT_LISTEN_PORT);
  if (listenPort > 0) {
    out.listen_port = listenPort;
  }
  return out;
}

function normalizeAgentSecurity(agent, rawEntries, token) {
  const existingState = stringValue(agent?.security?.provision_state);
  const out = {
    provision_state: existingState || (token ? 'provisioned' : 'pending-bootstrap'),
    token_configured: Boolean(agent?.security?.token_configured || token),
    tls: normalizeTLSSpec(agent?.security?.tls, rawEntries),
  };
  return out;
}

function normalizeTLSSpec(existingTLS, rawEntries) {
  if (isTLSMode(existingTLS?.mode)) {
    const out = { mode: existingTLS.mode };
    setIfNonEmpty(out, 'ca_cert', existingTLS?.ca_cert);
    setIfNonEmpty(out, 'server_name', existingTLS?.server_name);
    return out;
  }

  const direct = firstDirectSource(rawEntries);
  const out = { mode: 'auto' };
  if (!direct) {
    return out;
  }
  if (direct.tls === false) {
    out.mode = 'off';
  } else if (stringValue(direct.ca_cert)) {
    out.mode = 'manual';
    out.ca_cert = stringValue(direct.ca_cert);
  } else if (direct.tls === true) {
    out.mode = 'auto';
  }
  setIfNonEmpty(out, 'server_name', direct.server_name);
  return out;
}

function normalizeTransportEntry(entry) {
  const type = stringValue(entry?.type || 'tunnel');
  if (type === 'direct') {
    const direct = entry?.direct ?? entry ?? {};
    return { type, direct: normalizeDirect(direct) };
  }
  if (type === 'tunnel') {
    const tunnel = entry?.tunnel ?? entry ?? {};
    return { type, tunnel: normalizeTunnel(tunnel) };
  }
  return { type };
}

function normalizeTunnel(tunnel) {
  return {
    remote_agent_port: firstPositiveInteger(tunnel?.remote_agent_port, DEFAULT_REMOTE_AGENT_PORT),
  };
}

function normalizeDirect(direct) {
  const out = {};
  setIfNonEmpty(out, 'address', direct?.address);
  return out;
}

function legacyTransportEntries(host) {
  if (!isNestedHost(host)) {
    return [{ type: 'tunnel', tunnel: host }];
  }
  return agentTransportEntries(host?.agent);
}

function agentTransportEntries(agent) {
  const transport = agent?.transport ?? {};
  if (Array.isArray(transport.chain) && transport.chain.length > 0) {
    return transport.chain;
  }
  return [{
    type: stringValue(transport.type || 'tunnel'),
    tunnel: transport.tunnel,
    direct: transport.direct,
  }];
}

function firstTunnelSource(host) {
  if (!isNestedHost(host)) {
    return host;
  }
  for (const entry of legacyTransportEntries(host)) {
    const type = stringValue(entry?.type || 'tunnel');
    if (type === 'tunnel') {
      return entry?.tunnel ?? entry ?? {};
    }
  }
  return {};
}

function firstDirectSource(rawEntries) {
  for (const entry of rawEntries) {
    const type = stringValue(entry?.type || '');
    if (type === 'direct') {
      return entry?.direct ?? entry ?? {};
    }
  }
  return null;
}

function firstTunnelPort(chain) {
  for (const entry of chain) {
    if (entry?.type === 'tunnel' && Number.isInteger(entry?.tunnel?.remote_agent_port)) {
      return entry.tunnel.remote_agent_port;
    }
  }
  return 0;
}

function readPrivateKeyContent(keyPath) {
  const normalized = stringValue(keyPath);
  if (!normalized) {
    return '';
  }
  const path = resolve(expandHome(normalized));
  if (!existsSync(path)) {
    return '';
  }
  return readFileSync(path, 'utf8');
}

function setIfNonEmpty(target, key, value) {
  const normalized = stringValue(value);
  if (normalized !== '') {
    target[key] = normalized;
  }
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const normalized = stringValue(value);
    if (normalized !== '') {
      return normalized;
    }
  }
  return '';
}

function firstPositiveInteger(...values) {
  for (const value of values) {
    if (Number.isInteger(value) && value > 0) {
      return value;
    }
  }
  return 0;
}

function stringValue(value) {
  return typeof value === 'string' ? value : '';
}

function isTLSMode(value) {
  return value === 'off' || value === 'auto' || value === 'manual';
}

function writeJSON(path, value, mode) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { mode });
  chmodSync(path, mode);
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
    agentsTarget: '',
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
      case '--agents-target':
        opts.agentsTarget = requireValue(argv, ++i, arg);
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
  node scripts/migrate-hosts-json.mjs [--apply] [--source <hosts.json>] [--target <hosts.json>] [--agents-target <agents.json>]

Defaults:
  --source ${DEFAULT_HOSTS_PATH}
  --target <same as --source>
  --agents-target <target directory>/agents.json

By default this is a dry-run. Add --apply to back up and rewrite hosts.json plus agents.json.`);
}

function main(argv = process.argv.slice(2)) {
  const opts = parseArgs(argv);
  const result = migrateHostsFile(opts);
  console.log(`Source:       ${result.source}`);
  console.log(`Target hosts: ${result.target}`);
  console.log(`Target agents:${result.agentsTarget}`);
  console.log(`Mode:         ${opts.apply ? 'apply' : 'dry-run'}`);
  console.log(
    `hosts.json: ${result.total} host(s), ${result.legacy} legacy flat, ${result.nested} nested agent`,
  );
  if (opts.apply) {
    if (result.backup) {
      console.log(`Hosts backup:  ${result.backup}`);
    }
    if (result.agentsBackup) {
      console.log(`Agents backup: ${result.agentsBackup}`);
    }
    console.log('Migration applied.');
  } else {
    console.log('Dry-run only. Re-run with --apply to rewrite hosts.json and agents.json.');
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
