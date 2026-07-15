/**
 * runtime-validation Node.js fixture 提供跨平台 HTTP 运行与 js-debug 断点合同。
 *
 * 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
 * 边界：不下载依赖、不访问 SuperDev API，也不持久化 campaign secret。
 */
const http = require('node:http');

const port = Number(process.env.FIXTURE_PORT);
if (!Number.isInteger(port) || port <= 0) {
  throw new Error('FIXTURE_PORT is required');
}

const server = http.createServer((request, response) => {
  if (request.url === '/healthz') {
    return writeJSON(response, 200, { ready: true, provider: 'node' });
  }
  if (request.url?.startsWith('/api/probe')) {
    const fixtureMarker = 'breakpoint-visible';
    const fixtureCount = 42;
    const fixtureProvider = 'node';
    void fixtureMarker; // SUPERDEV_FIXTURE_BREAKPOINT
    const controlledError = new URL(request.url, 'http://127.0.0.1').searchParams.get('mode') === 'error';
    return writeJSON(response, controlledError ? 500 : 200, {
      ok: !controlledError,
      provider: fixtureProvider,
      count: fixtureCount,
    });
  }
  return writeJSON(response, 404, { ok: false });
});

server.listen(port, '127.0.0.1');

function writeJSON(response, status, value) {
  response.writeHead(status, { 'content-type': 'application/json' });
  response.end(JSON.stringify(value));
}
