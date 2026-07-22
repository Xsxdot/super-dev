/*
 * Java Windows validation fixture.
 *
 * 职责：以真实 JVM 进程提供 readiness、campaign Bearer probe、受控 500、结构化日志和稳定断点变量。
 * 边界：仅依赖 JDK 自带 HTTP server；不调用 SuperDev/MCP，也不输出 Authorization 或环境凭据。
 */
package superdev.fixture;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Locale;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** FixtureServer 是 Java provider 的独立 Fixture Protocol v1 进程。 */
public final class FixtureServer {
    private static final String PROVIDER = "java";
    private static final String CONTRACT_VERSION = "v1";
    private static final int DEFAULT_PORT = 18173;
    private static final int MAX_BODY_BYTES = 64 * 1024;

    private FixtureServer() {}

    /** 启动 loopback HTTP 服务；JVM shutdown hook 负责停止 server 与工作线程。 */
    public static void main(String[] args) throws Exception {
        if ("fail".equals(System.getenv("FIXTURE_STARTUP_MODE"))) {
            writeLog("error", "fixture_startup_failed", "reason", "controlled_startup_failure");
            System.exit(23);
        }
        String campaignID = System.getenv("FIXTURE_CAMPAIGN_ID");
        if (campaignID == null || campaignID.isBlank()) {
            writeLog("error", "fixture_startup_failed", "stage", "configuration", "cause", "FIXTURE_CAMPAIGN_ID is required");
            System.exit(24);
        }
        int port;
        try {
            port = Integer.parseInt(envOrDefault("FIXTURE_PORT", Integer.toString(DEFAULT_PORT)));
        } catch (NumberFormatException error) {
            writeLog("error", "fixture_startup_failed", "stage", "parse_port", "cause", error.getMessage());
            System.exit(24);
            return;
        }

        HttpServer server;
        try {
            server = HttpServer.create(new InetSocketAddress("127.0.0.1", port), 0);
        } catch (Exception error) {
            writeLog("error", "fixture_startup_failed", "stage", "listen", "port", Integer.toString(port), "cause", error.getMessage());
            System.exit(24);
            return;
        }
        ExecutorService executor = Executors.newCachedThreadPool();
        server.setExecutor(executor);
        server.createContext("/healthz", guarded(FixtureServer::handleHealth));
        server.createContext("/api/probe", guarded(FixtureServer::handleProbe));
        server.createContext("/", guarded(FixtureServer::handleNotFound));
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            writeLog("info", "fixture_stopping", "signal", "jvm_shutdown");
            server.stop(0);
            executor.shutdownNow();
            writeLog("info", "fixture_stopped", "signal", "jvm_shutdown");
        }, "fixture-shutdown"));
        server.start();
        writeLog("info", "fixture_started", "host", "127.0.0.1", "port", Integer.toString(port), "contract_version", CONTRACT_VERSION);
        try {
            new CountDownLatch(1).await();
        } catch (InterruptedException error) {
            writeLog("error", "fixture_run_failed", "stage", "main_wait", "cause", error.getMessage());
            Thread.currentThread().interrupt();
            server.stop(0);
            executor.shutdownNow();
            System.exit(25);
        }
    }

    /** 包装 handler 的异常边界，确保意外错误有上下文且不终止服务进程。 */
    private static HttpHandler guarded(HttpHandler delegate) {
        return exchange -> {
            try {
                delegate.handle(exchange);
            } catch (Exception error) {
                writeLog("error", "fixture_request_failed", "reason", "unexpected_error", "cause", error.getClass().getSimpleName() + ":" + error.getMessage());
                if (exchange.getResponseCode() < 0) {
                    sendJson(exchange, 500, response(false, "fixture_internal_error", "", "", 0));
                }
            } finally {
                exchange.close();
            }
        };
    }

    /** 处理 readiness，严格限制为 GET /healthz。 */
    private static void handleHealth(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod()) || !"/healthz".equals(exchange.getRequestURI().getPath())) {
            handleNotFound(exchange);
            return;
        }
        sendJson(exchange, 200, "{\"ready\":true,\"contract_version\":\"v1\",\"provider\":\"java\"}");
        writeLog("info", "fixture_readiness_succeeded", "status", "200");
    }

    /** 处理鉴权 probe；受控业务错误返回 500 后仍可继续接受请求。 */
    private static void handleProbe(HttpExchange exchange) throws IOException {
        if (!"POST".equals(exchange.getRequestMethod()) || !"/api/probe".equals(exchange.getRequestURI().getPath())) {
            handleNotFound(exchange);
            return;
        }
        if (!isAuthorized(exchange.getRequestHeaders().getFirst("Authorization"))) {
            sendJson(exchange, 401, "{\"ok\":false,\"code\":\"fixture_unauthorized\",\"provider\":\"java\"}");
            writeLog("error", "fixture_request_rejected", "reason", "unauthorized", "status", "401");
            return;
        }
        byte[] body = exchange.getRequestBody().readNBytes(MAX_BODY_BYTES + 1);
        if (body.length > MAX_BODY_BYTES) {
            sendJson(exchange, 400, "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"java\"}");
            writeLog("error", "fixture_request_failed", "reason", "body_too_large", "status", "400");
            return;
        }
        String json = new String(body, StandardCharsets.UTF_8);
        String traceId = jsonString(json, "trace_id");
        String requestId = jsonString(json, "request_id");
        if (traceId.isBlank() || requestId.isBlank()) {
            sendJson(exchange, 400, "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"java\"}");
            writeLog("error", "fixture_request_failed", "reason", "correlation_id_required", "status", "400");
            return;
        }
        String outcome = "error".equals(jsonString(json, "outcome")) ? "error" : "ok";
        int value = jsonInteger(json, "value", 41);
        writeLog("info", "fixture_request_started", "trace_id", traceId, "request_id", requestId, "outcome", outcome);
        ProbeResult probe = fixtureProbe(value);
        int status = "error".equals(outcome) ? 500 : 200;
        String code = "error".equals(outcome) ? "fixture_controlled_error" : "fixture_ok";
        sendJson(exchange, status, response(status == 200, code, traceId, requestId, probe.fixtureCount()));
        writeLog(status == 500 ? "error" : "info", "fixture_request_completed", "trace_id", traceId, "request_id", requestId, "outcome", outcome, "status", Integer.toString(status));
    }

    /** 返回稳定断点变量；变量均为非秘密测试值。 */
    private static ProbeResult fixtureProbe(int value) {
        String fixtureMarker = "breakpoint-visible";
        int fixtureCount = value + 1;
        String fixtureProvider = PROVIDER;
        // SUPERDEV_FIXTURE_BREAKPOINT：在响应生成前保留已赋值的 JVM 局部变量。
        return new ProbeResult(fixtureMarker, fixtureCount, fixtureProvider);
    }

    /** 对未知路径返回固定 404，不暴露运行时细节。 */
    private static void handleNotFound(HttpExchange exchange) throws IOException {
        sendJson(exchange, 404, "{\"ok\":false,\"code\":\"fixture_not_found\",\"provider\":\"java\"}");
        writeLog("error", "fixture_request_rejected", "reason", "route_not_found", "status", "404");
    }

    /** 从非秘密 campaign ID 推导 Bearer 值，使用定长比较且不记录完整 header。 */
    private static boolean isAuthorized(String header) {
        String campaignID = System.getenv("FIXTURE_CAMPAIGN_ID");
        if (campaignID == null || campaignID.isBlank()) return false;
        byte[] supplied = (header == null ? "" : header.trim()).getBytes(StandardCharsets.UTF_8);
        byte[] expected = ("Bearer superdev-validation-" + campaignID.trim()).getBytes(StandardCharsets.UTF_8);
        return MessageDigest.isEqual(supplied, expected);
    }

    /** 发送固定 JSON 响应。 */
    private static void sendJson(HttpExchange exchange, int status, String json) throws IOException {
        byte[] bytes = json.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json; charset=utf-8");
        exchange.sendResponseHeaders(status, bytes.length);
        exchange.getResponseBody().write(bytes);
    }

    /** 从验证输入读取字符串字段；夹具只接受简单 JSON 字符串，不实现通用 parser。 */
    private static String jsonString(String json, String key) {
        Matcher matcher = Pattern.compile("\\\"" + Pattern.quote(key) + "\\\"\\s*:\\s*\\\"([^\\\"]*)\\\"").matcher(json);
        return matcher.find() ? matcher.group(1) : "";
    }

    /** 从验证输入读取整数，不存在或溢出时使用稳定默认值。 */
    private static int jsonInteger(String json, String key, int fallback) {
        Matcher matcher = Pattern.compile("\\\"" + Pattern.quote(key) + "\\\"\\s*:\\s*(-?\\d+)").matcher(json);
        if (!matcher.find()) return fallback;
        try {
            return Integer.parseInt(matcher.group(1));
        } catch (NumberFormatException error) {
            return fallback;
        }
    }

    /** 生成 probe 响应，所有动态文本先 JSON 转义。 */
    private static String response(boolean ok, String code, String traceId, String requestId, int result) {
        return String.format(Locale.ROOT,
                "{\"ok\":%s,\"code\":\"%s\",\"provider\":\"java\",\"trace_id\":\"%s\",\"request_id\":\"%s\",\"result\":%d}",
                ok, jsonEscape(code), jsonEscape(traceId), jsonEscape(requestId), result);
    }

    /** 写入结构化 JSON line；字段调用方必须排除凭据和 Authorization。 */
    private static synchronized void writeLog(String level, String event, String... fields) {
        StringBuilder line = new StringBuilder("{\"level\":\"").append(jsonEscape(level))
                .append("\",\"event\":\"").append(jsonEscape(event))
                .append("\",\"provider\":\"java\",\"campaign_id\":\"")
                .append(jsonEscape(envOrDefault("FIXTURE_CAMPAIGN_ID", "standalone"))).append('"');
        for (int index = 0; index + 1 < fields.length; index += 2) {
            line.append(",\"").append(jsonEscape(fields[index])).append("\":\"")
                    .append(jsonEscape(fields[index + 1] == null ? "" : fields[index + 1])).append('"');
        }
        line.append("}\n");
        byte[] bytes = line.toString().getBytes(StandardCharsets.UTF_8);
        if ("error".equals(level)) System.err.write(bytes, 0, bytes.length);
        else System.out.write(bytes, 0, bytes.length);
    }

    /** JSON 转义日志/响应中的普通诊断文本。 */
    private static String jsonEscape(String value) {
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\r", "\\r").replace("\n", "\\n");
    }

    /** 读取环境值；空值不覆盖安全的 fixture-only 默认值。 */
    private static String envOrDefault(String name, String fallback) {
        String value = System.getenv(name);
        return value == null || value.isBlank() ? fallback : value;
    }

    /** ProbeResult 保存断点可见的非秘密局部变量。 */
    private record ProbeResult(String fixtureMarker, int fixtureCount, String fixtureProvider) {}
}
