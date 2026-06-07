/**
 * hosts.json migration tests.
 *
 * Responsibilities:
 *   - Verify legacy flat Host records are converted into Host -> Agent -> Transport.
 *   - Verify dry-run mode reports work without rewriting production data.
 *
 * Boundaries:
 *   - Does not touch the user's real ~/.superdev directory.
 *   - Does not validate remote SSH connectivity or agent reachability.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { migrateHostsFile } from './migrate-hosts-json.mjs';

test('migrates legacy flat host into split hosts and agents files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'superdev-hosts-'));
  const source = join(dir, 'hosts.json');
  const agents = join(dir, 'agents.json');
  writeFileSync(
    source,
    JSON.stringify([
      {
        id: 'h1',
        name: 'ali',
        ssh_host: '1.2.3.4',
        ssh_user: 'root',
        tags: ['prod'],
        remote_agent_port: 57017,
      },
    ]),
  );

  const result = migrateHostsFile({
    source,
    target: source,
    agentsTarget: agents,
    apply: true,
    now: () => '20260606-120000',
  });

  assert.equal(result.total, 1);
  assert.equal(result.legacy, 1);
  assert.equal(result.nested, 0);
  assert.equal(result.backup, `${source}.bak-20260606-120000`);
  assert.equal(existsSync(result.backup), true);

  const migrated = JSON.parse(readFileSync(source, 'utf8'));
  assert.equal(migrated[0].ssh_host, '1.2.3.4');
  assert.equal(migrated[0].ssh_user, 'root');
  assert.equal(Object.hasOwn(migrated[0], 'agent'), false);
  assert.equal(statSync(source).mode & 0o777, 0o600);

  const migratedAgents = JSON.parse(readFileSync(agents, 'utf8'));
  assert.equal(migratedAgents[0].host_id, 'h1');
  assert.equal(migratedAgents[0].transport.chain[0].type, 'tunnel');
  assert.equal(migratedAgents[0].transport.chain[0].tunnel.remote_agent_port, 57017);
  assert.equal(Object.hasOwn(migratedAgents[0].transport.chain[0].tunnel, 'ssh_host'), false);
  assert.equal(statSync(agents).mode & 0o777, 0o600);
});

test('migrates nested single direct transport into agent security and secret', () => {
  const dir = mkdtempSync(join(tmpdir(), 'superdev-hosts-'));
  const source = join(dir, 'hosts.json');
  const agents = join(dir, 'agents.json');
  writeFileSync(
    source,
    JSON.stringify([
      {
        id: 'h1',
        name: 'direct',
        tags: [],
        agent: {
          token: 'tok',
          transport: {
            type: 'direct',
            direct: { address: '100.64.0.8:57017', tls: true, ca_cert: 'PEM' },
          },
        },
      },
    ]),
  );

  migrateHostsFile({ source, target: source, agentsTarget: agents, apply: true, now: () => '20260607-120000' });

  const migrated = JSON.parse(readFileSync(agents, 'utf8'));
  assert.equal(migrated[0].secret.token, 'tok');
  assert.equal(migrated[0].security.tls.mode, 'manual');
  assert.equal(migrated[0].security.tls.ca_cert, 'PEM');
  assert.equal(migrated[0].transport.chain[0].type, 'direct');
  assert.equal(migrated[0].transport.chain[0].direct.address, '100.64.0.8:57017');
  assert.equal(Object.hasOwn(migrated[0].transport.chain[0].direct, 'ca_cert'), false);
});

test('dry run does not rewrite the file', () => {
  const dir = mkdtempSync(join(tmpdir(), 'superdev-hosts-'));
  const source = join(dir, 'hosts.json');
  const original = '[{"id":"h1","name":"ali","ssh_host":"1.2.3.4","ssh_user":"root"}]';
  writeFileSync(source, original);

  const result = migrateHostsFile({
    source,
    target: source,
    apply: false,
    now: () => '20260606-120000',
  });

  assert.equal(result.total, 1);
  assert.equal(result.legacy, 1);
  assert.equal(result.backup, '');
  assert.equal(readFileSync(source, 'utf8'), original);
});
