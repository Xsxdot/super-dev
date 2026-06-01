/**
 * Node HTTP pipeline example.
 *
 * Responsibilities:
 *   - Expose /health for deployment health checks.
 *   - Expose /info with language and app metadata.
 *
 * Boundaries:
 *   - Does not depend on external services.
 *   - Does not require a reverse proxy.
 */
const http = require('http')

const port = Number(process.env.PORT || 18081)

const server = http.createServer((req, res) => {
  if (req.url === '/health') {
    res.writeHead(200, { 'content-type': 'text/plain' })
    res.end('ok')
    return
  }
  if (req.url === '/info') {
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ app: 'node-http', language: 'node', version: process.env.APP_VERSION || '' }))
    return
  }
  res.writeHead(200, { 'content-type': 'text/plain' })
  res.end('node-http')
})

server.listen(port, '0.0.0.0')
