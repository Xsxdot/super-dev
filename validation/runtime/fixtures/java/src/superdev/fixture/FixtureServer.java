/**
 * runtime-validation Java fixture 的无依赖 HTTP 运行与 JVM DAP 断点合同。
 *
 * 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
 * 边界：不使用构建框架、不访问 SuperDev API，也不持久化 campaign secret。
 */
package superdev.fixture;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

public final class FixtureServer {
    private FixtureServer() {}

    public static void main(String[] args) throws IOException {
        int port = Integer.parseInt(System.getenv("FIXTURE_PORT"));
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", port), 0);
        server.createContext("/healthz", exchange -> write(exchange, 200, "{\"ready\":true,\"provider\":\"java\"}"));
        server.createContext("/api/probe", FixtureServer::probe);
        server.start();
    }

    private static void probe(HttpExchange exchange) throws IOException {
        String fixtureMarker = "breakpoint-visible";
        int fixtureCount = 42;
        String fixtureProvider = "java";
        fixtureMarker.length(); // SUPERDEV_FIXTURE_BREAKPOINT
        boolean controlledError = "mode=error".equals(exchange.getRequestURI().getQuery());
        int status = controlledError ? 500 : 200;
        write(exchange, status, "{\"ok\":" + (!controlledError) + ",\"provider\":\"" + fixtureProvider + "\",\"count\":" + fixtureCount + "}");
    }

    private static void write(HttpExchange exchange, int status, String body) throws IOException {
        byte[] raw = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("content-type", "application/json");
        exchange.sendResponseHeaders(status, raw.length);
        exchange.getResponseBody().write(raw);
        exchange.close();
    }
}
