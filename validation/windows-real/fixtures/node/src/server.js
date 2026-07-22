/**
 * Node.js Windows validation fixture.
 *
 * 职责：提供 Fixture Protocol v1 的 readiness、鉴权 probe、受控错误、结构化日志和稳定断点现场。
 * 边界：仅使用 Node.js 标准库；不调用 SuperDev、MCP、外部服务，也不记录鉴权材料。
 */
'use strict';

const crypto = require('node:crypto');
const http = require('node:http');

const PROVIDER = 'node';
const CONTRACT_VERSION = 'v1';
const DEFAULT_PORT = 18171;

/** 写入一条可由 Runtime Log 检索的 JSON line，字段值中不得包含凭据。 */
function writeLog(level, event, fields = {}) {
  const line = JSON.stringify({
    timestamp: new Date().toISOString(),
    level,
    event,
    provider: PROVIDER,
    campaign_id: process.env.FIXTURE_CAMPAIGN_ID || 'standalone',
    ...fields,
  });
  const stream = level === 'error' ? process.stderr : process.stdout;
  stream.write(`${line}\n`);
}

/** 从非秘密 campaign ID 推导 Bearer 值并定长比较，不记录完整 Authorization。 */
function isAuthorized(header) {
  const campaignId = (process.env.FIXTURE_CAMPAIGN_ID || '').trim();
  if (!campaignId) return false;
  const expected = `Bearer superdev-validation-${campaignId}`;
  const suppliedBytes = Buffer.from(typeof header === 'string' ? header.trim() : '');
  const expectedBytes = Buffer.from(expected);
  return suppliedBytes.length === expectedBytes.length && crypto.timingSafeEqual(suppliedBytes, expectedBytes);
}

/**
 * 执行可调试的业务计算。
 *
 * @param {number} value 非秘密输入值。
 * @returns {{fixtureMarker: string, fixtureCount: number, fixtureProvider: string}} 稳定断点变量与计算结果。
 */
function fixtureProbe(value) {
  const fixtureMarker = 'breakpoint-visible';
  const fixtureCount = value + 1;
  const fixtureProvider = PROVIDER;
  // SUPERDEV_FIXTURE_BREAKPOINT：变量已赋值且响应尚未发送，便于跨版本稳定检查。
  return { fixtureMarker, fixtureCount, fixtureProvider };
}

/** 发送固定 JSON 响应，响应只包含业务标识与非秘密字段。 */
function sendJson(response, status, payload) {
  const body = Buffer.from(JSON.stringify(payload));
  response.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': body.length,
    connection: 'close',
  });
  response.end(body);
}

/** 收集有上限的请求体，防止验证输入异常时无限占用内存。 */
function readBody(request) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > 64 * 1024) {
        reject(new Error('request body exceeds 64 KiB'));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    request.on('error', reject);
  });
}

/** 处理公开 HTTP 合同；未知路由不泄露进程或文件系统信息。 */
async function handleRequest(request, response) {
  if (request.method === 'GET' && request.url === '/healthz') {
    sendJson(response, 200, { ready: true, contract_version: CONTRACT_VERSION, provider: PROVIDER });
    writeLog('info', 'fixture_readiness_succeeded', { status: 200 });
    return;
  }
  if (request.method !== 'POST' || request.url !== '/api/probe') {
    sendJson(response, 404, { ok: false, code: 'fixture_not_found', provider: PROVIDER });
    writeLog('error', 'fixture_request_rejected', { reason: 'route_not_found', status: 404 });
    return;
  }
  if (!isAuthorized(request.headers.authorization)) {
    sendJson(response, 401, { ok: false, code: 'fixture_unauthorized', provider: PROVIDER });
    writeLog('error', 'fixture_request_rejected', { reason: 'unauthorized', status: 401 });
    return;
  }

  let input;
  try {
    input = JSON.parse(await readBody(request));
  } catch (error) {
    sendJson(response, 400, { ok: false, code: 'fixture_invalid_request', provider: PROVIDER });
    writeLog('error', 'fixture_request_failed', { reason: 'invalid_json', status: 400, cause: error.message });
    return;
  }
  const traceId = typeof input.trace_id === 'string' ? input.trace_id : '';
  const requestId = typeof input.request_id === 'string' ? input.request_id : '';
  const outcome = input.outcome === 'error' ? 'error' : 'ok';
  const value = Number.isSafeInteger(input.value) ? input.value : 41;
  if (!traceId || !requestId) {
    sendJson(response, 400, { ok: false, code: 'fixture_invalid_request', provider: PROVIDER });
    writeLog('error', 'fixture_request_failed', { reason: 'correlation_id_required', status: 400 });
    return;
  }

  writeLog('info', 'fixture_request_started', { trace_id: traceId, request_id: requestId, outcome });
  const probe = fixtureProbe(value);
  if (outcome === 'error') {
    sendJson(response, 500, {
      ok: false,
      code: 'fixture_controlled_error',
      provider: PROVIDER,
      trace_id: traceId,
      request_id: requestId,
      result: probe.fixtureCount,
    });
    writeLog('error', 'fixture_request_completed', { trace_id: traceId, request_id: requestId, outcome, status: 500 });
    return;
  }
  sendJson(response, 200, {
    ok: true,
    code: 'fixture_ok',
    provider: PROVIDER,
    trace_id: traceId,
    request_id: requestId,
    result: probe.fixtureCount,
  });
  writeLog('info', 'fixture_request_completed', { trace_id: traceId, request_id: requestId, outcome, status: 200 });
}

if (process.env.FIXTURE_STARTUP_MODE === 'fail') {
  writeLog('error', 'fixture_startup_failed', { reason: 'controlled_startup_failure' });
  process.exitCode = 23;
} else {
  const campaignId = (process.env.FIXTURE_CAMPAIGN_ID || '').trim();
  if (!campaignId) {
    writeLog('error', 'fixture_startup_failed', { stage: 'configuration', cause: 'FIXTURE_CAMPAIGN_ID is required' });
    process.exit(24);
  }
  const port = Number.parseInt(process.env.FIXTURE_PORT || `${DEFAULT_PORT}`, 10);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    writeLog('error', 'fixture_startup_failed', { stage: 'parse_port', cause: 'port must be between 1 and 65535' });
    process.exit(24);
  }
  const server = http.createServer((request, response) => {
    handleRequest(request, response).catch((error) => {
      sendJson(response, 500, { ok: false, code: 'fixture_internal_error', provider: PROVIDER });
      writeLog('error', 'fixture_request_failed', { reason: 'unexpected_error', status: 500, cause: error.message });
    });
  });

  server.on('error', (error) => {
    writeLog('error', 'fixture_startup_failed', { stage: 'listen', port, cause: error.message });
    process.exitCode = 24;
  });
  server.listen(port, '127.0.0.1', () => {
    writeLog('info', 'fixture_started', { host: '127.0.0.1', port, contract_version: CONTRACT_VERSION });
  });

  let stopping = false;
  const stop = (signal) => {
    if (stopping) return;
    stopping = true;
    writeLog('info', 'fixture_stopping', { signal });
    server.close((error) => {
      if (error) {
        writeLog('error', 'fixture_stop_failed', { signal, cause: error.message });
        process.exitCode = 25;
        return;
      }
      writeLog('info', 'fixture_stopped', { signal });
    });
  };
  process.on('SIGINT', () => stop('SIGINT'));
  process.on('SIGTERM', () => stop('SIGTERM'));
}
