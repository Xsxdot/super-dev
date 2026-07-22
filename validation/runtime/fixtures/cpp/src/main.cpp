// runtime-validation C++ fixture 提供无第三方依赖的 HTTP 运行与 lldb-dap 断点合同。
//
// 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
// 边界：不访问 SuperDev API，不持久化数据，也不拥有 MCP coverage。

#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <string>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
using socket_handle = SOCKET;
#else
#include <csignal>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
using socket_handle = int;
#define INVALID_SOCKET (-1)
#define SOCKET_ERROR (-1)
#endif

namespace {
void close_socket(socket_handle socket) {
#ifdef _WIN32
    closesocket(socket);
#else
    close(socket);
#endif
}

void respond(socket_handle client, int status, const std::string& body) {
    const char* reason = status == 200 ? "OK" : status == 500 ? "Internal Server Error" : "Not Found";
    std::string response = "HTTP/1.1 " + std::to_string(status) + " " + reason +
        "\r\nContent-Type: application/json\r\nContent-Length: " + std::to_string(body.size()) +
        "\r\nConnection: close\r\n\r\n" + body;
    send(client, response.data(), static_cast<int>(response.size()), 0);
}

void handle(socket_handle client) {
    char buffer[2048]{};
    const int size = recv(client, buffer, sizeof(buffer) - 1, 0);
    const std::string request(buffer, size > 0 ? size : 0);
    if (request.rfind("GET /healthz ", 0) == 0) {
        respond(client, 200, "{\"ready\":true,\"provider\":\"cpp\"}");
        return;
    }
    if (request.rfind("POST /api/probe", 0) == 0) {
        const std::string fixture_marker = "breakpoint-visible";
        const int fixture_count = 42;
        const std::string fixture_provider = "cpp";
        volatile auto marker_length = fixture_marker.size(); // SUPERDEV_FIXTURE_BREAKPOINT
        (void)marker_length;
        const bool controlled_error = request.rfind("POST /api/probe?mode=error ", 0) == 0;
        respond(client, controlled_error ? 500 : 200,
            "{\"ok\":" + std::string(controlled_error ? "false" : "true") +
            ",\"provider\":\"" + fixture_provider + "\",\"count\":" + std::to_string(fixture_count) + "}");
        return;
    }
    respond(client, 404, "{\"ok\":false}");
}
}  // namespace

int main() {
#ifdef _WIN32
    WSADATA data{};
    if (WSAStartup(MAKEWORD(2, 2), &data) != 0) return 1;
#else
    // 调试 capture 取消某个在途 HTTP probe 时，对端可能先关闭 socket。
    // 忽略 SIGPIPE 让 send 以错误返回，而不是让整个 fixture 被系统信号终止。
    std::signal(SIGPIPE, SIG_IGN);
#endif
    const char* raw_port = std::getenv("FIXTURE_PORT");
    if (raw_port == nullptr) return 2;
    const int port = std::stoi(raw_port);
    const socket_handle server = socket(AF_INET, SOCK_STREAM, 0);
    if (server == INVALID_SOCKET) return 3;
    // managed runtime 会在同一动态端口上执行 stop→start；允许立即重绑，避免
    // 上一连接的 TIME_WAIT 把 debug 阶段误判为服务未就绪。
    const int reuse_address = 1;
#ifdef _WIN32
    if (setsockopt(server, SOL_SOCKET, SO_REUSEADDR,
                   reinterpret_cast<const char*>(&reuse_address), sizeof(reuse_address)) == SOCKET_ERROR) return 6;
#else
    if (setsockopt(server, SOL_SOCKET, SO_REUSEADDR, &reuse_address, sizeof(reuse_address)) == SOCKET_ERROR) return 6;
#endif
    sockaddr_in address{};
    address.sin_family = AF_INET;
    address.sin_port = htons(static_cast<unsigned short>(port));
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(server, reinterpret_cast<sockaddr*>(&address), sizeof(address)) == SOCKET_ERROR) return 4;
    if (listen(server, 8) == SOCKET_ERROR) return 5;
    while (true) {
        const socket_handle client = accept(server, nullptr, nullptr);
        if (client == INVALID_SOCKET) {
#ifdef _WIN32
            // debugger 暂停可能中断阻塞中的 accept；这不是服务退出条件。
            if (WSAGetLastError() == WSAEINTR) continue;
#else
            if (errno == EINTR) continue;
#endif
            break;
        }
        handle(client);
        close_socket(client);
    }
    close_socket(server);
#ifdef _WIN32
    WSACleanup();
#endif
    return 0;
}
