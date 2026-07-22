/*
 * Kotlin Windows validation fixture.
 *
 * 职责：以 Kotlin/JVM 进程实现 readiness、campaign Bearer probe、受控错误、结构化日志和稳定断点变量。
 * 边界：仅依赖 Kotlin/JDK 标准能力；不调用 SuperDev/MCP，也不记录 Authorization 或环境凭据。
 */
package superdev.fixture

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors

private const val PROVIDER = "kotlin"
private const val CONTRACT_VERSION = "v1"
private const val DEFAULT_PORT = 18174
private const val MAX_BODY_BYTES = 64 * 1024

/** ProbeResult 保存断点可见的非秘密 Kotlin 局部变量。 */
data class ProbeResult(val fixtureMarker: String, val fixtureCount: Int, val fixtureProvider: String)

/** 启动 Kotlin/JVM loopback 服务；shutdown hook 保证服务和线程池干净退出。 */
fun main() {
    if (System.getenv("FIXTURE_STARTUP_MODE") == "fail") {
        writeLog("error", "fixture_startup_failed", "reason" to "controlled_startup_failure")
        kotlin.system.exitProcess(23)
    }
    if (System.getenv("FIXTURE_CAMPAIGN_ID").isNullOrBlank()) {
        writeLog("error", "fixture_startup_failed", "stage" to "configuration", "cause" to "FIXTURE_CAMPAIGN_ID is required")
        kotlin.system.exitProcess(24)
    }
    val port = envOrDefault("FIXTURE_PORT", DEFAULT_PORT.toString()).toIntOrNull()
    if (port == null) {
        writeLog("error", "fixture_startup_failed", "stage" to "parse_port")
        kotlin.system.exitProcess(24)
    }
    val server = try {
        HttpServer.create(InetSocketAddress("127.0.0.1", port), 0)
    } catch (error: Exception) {
        writeLog("error", "fixture_startup_failed", "stage" to "listen", "port" to port.toString(), "cause" to (error.message ?: error.javaClass.simpleName))
        kotlin.system.exitProcess(24)
    }
    val executor = Executors.newCachedThreadPool()
    server.executor = executor
    server.createContext("/healthz") { exchange -> guarded(exchange, ::handleHealth) }
    server.createContext("/api/probe") { exchange -> guarded(exchange, ::handleProbe) }
    server.createContext("/") { exchange -> guarded(exchange, ::handleNotFound) }
    Runtime.getRuntime().addShutdownHook(Thread({
        writeLog("info", "fixture_stopping", "signal" to "jvm_shutdown")
        server.stop(0)
        executor.shutdownNow()
        writeLog("info", "fixture_stopped", "signal" to "jvm_shutdown")
    }, "fixture-shutdown"))
    server.start()
    writeLog("info", "fixture_started", "host" to "127.0.0.1", "port" to port.toString(), "contract_version" to CONTRACT_VERSION)
    try {
        CountDownLatch(1).await()
    } catch (error: InterruptedException) {
        writeLog("error", "fixture_run_failed", "stage" to "main_wait", "cause" to (error.message ?: "interrupted"))
        Thread.currentThread().interrupt()
        server.stop(0)
        executor.shutdownNow()
        kotlin.system.exitProcess(25)
    }
}

/** 捕获请求边界异常，记录安全根因并保持 Kotlin fixture 进程存活。 */
private fun guarded(exchange: HttpExchange, handler: (HttpExchange) -> Unit) {
    try {
        handler(exchange)
    } catch (error: Exception) {
        writeLog("error", "fixture_request_failed", "reason" to "unexpected_error", "cause" to "${error.javaClass.simpleName}:${error.message ?: ""}")
        if (exchange.responseCode < 0) sendJson(exchange, 500, response(false, "fixture_internal_error", "", "", 0))
    } finally {
        exchange.close()
    }
}

/** 处理严格的 GET /healthz readiness 合同。 */
private fun handleHealth(exchange: HttpExchange) {
    if (exchange.requestMethod != "GET" || exchange.requestURI.path != "/healthz") {
        handleNotFound(exchange)
        return
    }
    sendJson(exchange, 200, "{\"ready\":true,\"contract_version\":\"v1\",\"provider\":\"kotlin\"}")
    writeLog("info", "fixture_readiness_succeeded", "status" to "200")
}

/** 处理鉴权 probe；outcome=error 只产生可恢复的稳定 HTTP 500。 */
private fun handleProbe(exchange: HttpExchange) {
    if (exchange.requestMethod != "POST" || exchange.requestURI.path != "/api/probe") {
        handleNotFound(exchange)
        return
    }
    if (!isAuthorized(exchange.requestHeaders.getFirst("Authorization"))) {
        sendJson(exchange, 401, "{\"ok\":false,\"code\":\"fixture_unauthorized\",\"provider\":\"kotlin\"}")
        writeLog("error", "fixture_request_rejected", "reason" to "unauthorized", "status" to "401")
        return
    }
    val body = exchange.requestBody.readNBytes(MAX_BODY_BYTES + 1)
    if (body.size > MAX_BODY_BYTES) {
        sendJson(exchange, 400, "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"kotlin\"}")
        writeLog("error", "fixture_request_failed", "reason" to "body_too_large", "status" to "400")
        return
    }
    val json = body.toString(StandardCharsets.UTF_8)
    val traceId = jsonString(json, "trace_id")
    val requestId = jsonString(json, "request_id")
    if (traceId.isBlank() || requestId.isBlank()) {
        sendJson(exchange, 400, "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"kotlin\"}")
        writeLog("error", "fixture_request_failed", "reason" to "correlation_id_required", "status" to "400")
        return
    }
    val outcome = if (jsonString(json, "outcome") == "error") "error" else "ok"
    val value = jsonInteger(json, "value", 41)
    writeLog("info", "fixture_request_started", "trace_id" to traceId, "request_id" to requestId, "outcome" to outcome)
    val probe = fixtureProbe(value)
    val status = if (outcome == "error") 500 else 200
    val code = if (outcome == "error") "fixture_controlled_error" else "fixture_ok"
    sendJson(exchange, status, response(status == 200, code, traceId, requestId, probe.fixtureCount))
    writeLog(if (status == 500) "error" else "info", "fixture_request_completed", "trace_id" to traceId, "request_id" to requestId, "outcome" to outcome, "status" to status.toString())
}

/** 创建稳定的 Kotlin 断点现场，变量均为非秘密测试值。 */
private fun fixtureProbe(value: Int): ProbeResult {
    val fixtureMarker = "breakpoint-visible"
    val fixtureCount = value + 1
    val fixtureProvider = PROVIDER
    // SUPERDEV_FIXTURE_BREAKPOINT：在生成响应前固定三个可观察的局部变量。
    return ProbeResult(fixtureMarker, fixtureCount, fixtureProvider)
}

/** 未知路径返回固定 404，避免泄露运行环境。 */
private fun handleNotFound(exchange: HttpExchange) {
    sendJson(exchange, 404, "{\"ok\":false,\"code\":\"fixture_not_found\",\"provider\":\"kotlin\"}")
    writeLog("error", "fixture_request_rejected", "reason" to "route_not_found", "status" to "404")
}

/** 从非秘密 campaign ID 推导 Bearer 值并定长比较，不记录完整 header。 */
private fun isAuthorized(header: String?): Boolean {
    val campaignID = System.getenv("FIXTURE_CAMPAIGN_ID")?.trim().orEmpty()
    if (campaignID.isEmpty()) return false
    val supplied = (header ?: "").trim().toByteArray(StandardCharsets.UTF_8)
    val expected = "Bearer superdev-validation-$campaignID".toByteArray(StandardCharsets.UTF_8)
    return MessageDigest.isEqual(supplied, expected)
}

/** 发送固定 JSON 响应。 */
private fun sendJson(exchange: HttpExchange, status: Int, json: String) {
    val bytes = json.toByteArray(StandardCharsets.UTF_8)
    exchange.responseHeaders.set("Content-Type", "application/json; charset=utf-8")
    exchange.sendResponseHeaders(status, bytes.size.toLong())
    exchange.responseBody.write(bytes)
}

/** 从受控 fixture 输入提取简单字符串字段，不充当通用 JSON parser。 */
private fun jsonString(json: String, key: String): String =
    Regex("\\\"${Regex.escape(key)}\\\"\\s*:\\s*\\\"([^\\\"]*)\\\"").find(json)?.groupValues?.get(1) ?: ""

/** 从受控 fixture 输入提取整数，缺失或溢出时回落为固定默认值。 */
private fun jsonInteger(json: String, key: String, fallback: Int): Int =
    Regex("\\\"${Regex.escape(key)}\\\"\\s*:\\s*(-?\\d+)").find(json)?.groupValues?.get(1)?.toIntOrNull() ?: fallback

/** 生成 probe JSON 响应。 */
private fun response(ok: Boolean, code: String, traceId: String, requestId: String, result: Int): String =
    "{\"ok\":$ok,\"code\":\"${jsonEscape(code)}\",\"provider\":\"kotlin\",\"trace_id\":\"${jsonEscape(traceId)}\",\"request_id\":\"${jsonEscape(requestId)}\",\"result\":$result}"

/** 写入一条结构化 JSON line；调用方禁止传入凭据或 Authorization。 */
@Synchronized
private fun writeLog(level: String, event: String, vararg fields: Pair<String, String>) {
    val line = buildString {
        append("{\"level\":\"").append(jsonEscape(level))
        append("\",\"event\":\"").append(jsonEscape(event))
        append("\",\"provider\":\"kotlin\",\"campaign_id\":\"")
        append(jsonEscape(envOrDefault("FIXTURE_CAMPAIGN_ID", "standalone"))).append('"')
        fields.forEach { (key, value) -> append(",\"").append(jsonEscape(key)).append("\":\"").append(jsonEscape(value)).append('"') }
        append("}\n")
    }.toByteArray(StandardCharsets.UTF_8)
    if (level == "error") System.err.write(line) else System.out.write(line)
}

/** JSON 转义普通诊断文本。 */
private fun jsonEscape(value: String): String = value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\r", "\\r").replace("\n", "\\n")

/** 读取环境值；空值不覆盖 fixture-only 默认值。 */
private fun envOrDefault(name: String, fallback: String): String = System.getenv(name)?.takeIf { it.isNotBlank() } ?: fallback
