// runtime-validation C++ fixture 提供无第三方依赖的 HTTP 运行与 lldb-dap 断点合同。
//
// 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
// 边界：不访问 SuperDev API，不持久化数据，也不拥有 MCP coverage。

#include <cstdlib>
#include <cstring>
#include <iostream>
#include <string>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
using socket_handle = SOCKET;
#else
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
#endif
    const char* raw_port = std::getenv("FIXTURE_PORT");
    if (raw_port == nullptr) return 2;
    const int port = std::stoi(raw_port);
    const socket_handle server = socket(AF_INET, SOCK_STREAM, 0);
    if (server == INVALID_SOCKET) return 3;
    sockaddr_in address{};
    address.sin_family = AF_INET;
    address.sin_port = htons(static_cast<unsigned short>(port));
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(server, reinterpret_cast<sockaddr*>(&address), sizeof(address)) == SOCKET_ERROR) return 4;
    if (listen(server, 8) == SOCKET_ERROR) return 5;
    while (true) {
        const socket_handle client = accept(server, nullptr, nullptr);
        if (client == INVALID_SOCKET) break;
        handle(client);
        close_socket(client);
    }
    close_socket(server);
#ifdef _WIN32
    WSACleanup();
#endif
    return 0;
}
