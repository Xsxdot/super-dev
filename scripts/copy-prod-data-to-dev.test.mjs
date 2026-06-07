/**
 * copy-prod-data-to-dev 迁移测试。
 *
 * 职责：
 *   - 验证生产到开发数据复制时拆分 Host 和 Agent 记录
 *   - 覆盖 token、TLS CA、SSH 私钥内容等关键字段落到正确边界
 *
 * 边界：
 *   - 不访问真实生产或开发数据文件
 *   - 不验证脚本的命令行解析流程
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import { migrateHostForDevCopy } from './copy-prod-data-to-dev.mjs';

test('copy-prod migration splits host agent and preserves security', () => {
  const migrated = migrateHostForDevCopy({
    id: 'h1',
    name: 'ali',
    tags: [],
    ssh_host: '1.2.3.4',
    ssh_user: 'root',
    ssh_private_key: 'KEY',
    agent: {
      token: 'long-token',
      transport: {
        chain: [
          { type: 'direct', direct: { address: '100.64.0.8:57017', tls: true, ca_cert: 'PEM' } },
        ],
      },
    },
  });

  assert.equal(migrated.host.ssh_host, '1.2.3.4');
  assert.equal(migrated.host.ssh_private_key, 'KEY');
  assert.equal(Object.hasOwn(migrated.host, 'agent'), false);
  assert.equal(migrated.agent.secret.token, 'long-token');
  assert.equal(migrated.agent.security.tls.mode, 'manual');
  assert.equal(migrated.agent.security.tls.ca_cert, 'PEM');
  assert.equal(migrated.agent.transport.chain[0].direct.address, '100.64.0.8:57017');
  assert.equal(Object.hasOwn(migrated.agent.transport.chain[0].direct, 'ca_cert'), false);
});
