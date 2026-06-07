/**
 * copy-prod-data-to-dev 迁移测试。
 *
 * 职责：
 *   - 验证生产到开发数据复制时保留 agent 嵌套凭据
 *   - 覆盖 direct transport chain 中 CA 等关键字段不被清空
 *
 * 边界：
 *   - 不访问真实生产或开发数据文件
 *   - 不验证脚本的命令行解析流程
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import { migrateHostForDevCopy } from './copy-prod-data-to-dev.mjs';

test('copy-prod migration preserves nested agent token', () => {
  const migrated = migrateHostForDevCopy({
    id: 'h1',
    name: 'ali',
    tags: [],
    agent: {
      token: 'long-token',
      transport: {
        chain: [
          { type: 'direct', direct: { address: '100.64.0.8:57017', tls: true, ca_cert: 'PEM' } },
        ],
      },
    },
  });

  assert.equal(migrated.agent.token, 'long-token');
  assert.equal(migrated.agent.transport.chain[0].direct.ca_cert, 'PEM');
});
