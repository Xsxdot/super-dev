/**
 * runtime-validation Kotlin fixture 的无依赖 HTTP 运行与 JVM DAP 断点合同。
 *
 * 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
 * 边界：不使用 Gradle、不访问 SuperDev API，也不持久化 campaign secret。
 */
package superdev.fixture

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress

fun main() {
    val port = System.getenv("FIXTURE_PORT").toInt()
    val server = HttpServer.create(InetSocketAddress("127.0.0.1", port), 0)
    server.createContext("/healthz") { exchange -> write(exchange, 200, "{\"ready\":true,\"provider\":\"kotlin\"}") }
    server.createContext("/api/probe", ::probe)
    server.start()
}

private fun probe(exchange: HttpExchange) {
    val fixtureMarker = "breakpoint-visible"
    val fixtureCount = 42
    val fixtureProvider = "kotlin"
    fixtureMarker.length // SUPERDEV_FIXTURE_BREAKPOINT
    val controlledError = exchange.requestURI.query == "mode=error"
    val status = if (controlledError) 500 else 200
    write(exchange, status, "{\"ok\":${!controlledError},\"provider\":\"$fixtureProvider\",\"count\":$fixtureCount}")
}

private fun write(exchange: HttpExchange, status: Int, body: String) {
    val raw = body.toByteArray()
    exchange.responseHeaders.set("content-type", "application/json")
    exchange.sendResponseHeaders(status, raw.size.toLong())
    exchange.responseBody.use { it.write(raw) }
}
