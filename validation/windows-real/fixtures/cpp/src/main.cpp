/*
 * C++ Windows validation fixture.
 *
 * 职责：用真实 Windows C++ 进程提供 readiness、campaign Bearer probe、受控错误、结构化日志和稳定断点变量。
 * 边界：仅依赖 WinSock/标准库；不调用 SuperDev/MCP，也不记录凭据或 Authorization。
 */

#ifdef _WIN32

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <windows.h>
#include <ws2tcpip.h>

#include <atomic>
#include <cctype>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <map>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <thread>
#include <utility>
#include <vector>

namespace {

constexpr const char* kProvider = "cpp";
constexpr const char* kContractVersion = "v1";
constexpr unsigned short kDefaultPort = 18176;
constexpr std::size_t kMaxRequestBytes = 64 * 1024;
std::atomic<bool> running{true};

/** ProbeResult 保存断点可见的非秘密 C++ 局部变量。 */
struct ProbeResult {
    std::string fixture_marker;
    long long fixture_count;
    std::string fixture_provider;
};

/** JSON 转义普通诊断文本。 */
std::string json_escape(const std::string& value) {
    std::string escaped;
    escaped.reserve(value.size());
    for (char character : value) {
        switch (character) {
            case '\\': escaped += "\\\\"; break;
            case '"': escaped += "\\\""; break;
            case '\r': escaped += "\\r"; break;
            case '\n': escaped += "\\n"; break;
            default: escaped += character; break;
        }
    }
    return escaped;
}

/** 读取环境值；空值不覆盖 fixture-only 默认值。 */
std::string env_or_default(const char* name, const char* fallback) {
    const char* value = std::getenv(name);
    return value != nullptr && value[0] != '\0' ? value : fallback;
}

/** 写入结构化 JSON line；字段调用方必须排除凭据与 Authorization。 */
void write_log(const std::string& level, const std::string& event, const std::vector<std::pair<std::string, std::string>>& fields = {}) {
    std::ostringstream line;
    line << "{\"level\":\"" << json_escape(level) << "\",\"event\":\"" << json_escape(event)
         << "\",\"provider\":\"cpp\",\"campaign_id\":\"" << json_escape(env_or_default("FIXTURE_CAMPAIGN_ID", "standalone")) << '"';
    for (const auto& [key, value] : fields) {
        line << ",\"" << json_escape(key) << "\":\"" << json_escape(value) << '"';
    }
    line << "}\n";
    std::ostream& stream = level == "error" ? std::cerr : std::cout;
    const std::string output = line.str();
    stream.write(output.data(), static_cast<std::streamsize>(output.size()));
    stream.flush();
}

/** Windows 控制台事件只置停止位，主循环负责正常释放 WinSock。 */
BOOL WINAPI console_handler(DWORD control) {
    if (control == CTRL_C_EVENT || control == CTRL_BREAK_EVENT || control == CTRL_CLOSE_EVENT) {
        running.store(false);
        return TRUE;
    }
    return FALSE;
}

/** 定长比较认证字段，避免普通字符串提前退出。 */
bool constant_time_equal(const std::string& left, const std::string& right) {
    if (left.size() != right.size()) return false;
    unsigned char difference = 0;
    for (std::size_t index = 0; index < left.size(); ++index) {
        difference |= static_cast<unsigned char>(left[index] ^ right[index]);
    }
    return difference == 0;
}

/** 从非秘密 campaign ID 推导 Bearer 值并定长比较，不记录完整 header。 */
bool is_authorized(const std::optional<std::string>& header) {
    if (!header.has_value()) return false;
    const std::string campaign_id = env_or_default("FIXTURE_CAMPAIGN_ID", "");
    if (campaign_id.empty()) return false;
    const std::string expected = "Bearer superdev-validation-" + campaign_id;
    return constant_time_equal(*header, expected);
}

/** 从简单 fixture JSON 输入中读取字符串字段。 */
std::optional<std::string> json_string(const std::string& input, const std::string& key) {
    const std::string needle = "\"" + key + "\"";
    const std::size_t key_position = input.find(needle);
    if (key_position == std::string::npos) return std::nullopt;
    const std::size_t colon = input.find(':', key_position + needle.size());
    const std::size_t start = input.find('"', colon == std::string::npos ? colon : colon + 1);
    if (start == std::string::npos) return std::nullopt;
    const std::size_t end = input.find('"', start + 1);
    if (end == std::string::npos) return std::nullopt;
    return input.substr(start + 1, end - start - 1);
}

/** 从简单 fixture JSON 输入中读取整数。 */
long long json_integer(const std::string& input, const std::string& key, long long fallback) {
    const std::string needle = "\"" + key + "\"";
    const std::size_t key_position = input.find(needle);
    if (key_position == std::string::npos) return fallback;
    const std::size_t colon = input.find(':', key_position + needle.size());
    if (colon == std::string::npos) return fallback;
    try {
        return std::stoll(input.substr(colon + 1));
    } catch (...) {
        return fallback;
    }
}

/** 创建稳定断点现场；所有变量都是非秘密 fixture 常量或派生数字。 */
__declspec(noinline) ProbeResult fixture_probe(long long value) {
    std::string fixture_marker = "breakpoint-visible";
    long long fixture_count = value + 1;
    std::string fixture_provider = kProvider;
    // SUPERDEV_FIXTURE_BREAKPOINT：变量已赋值且函数尚未返回，适合 lldb-dap 检查 PDB。
    return ProbeResult{fixture_marker, fixture_count, fixture_provider};
}

/** 发送固定 HTTP JSON 响应并关闭连接。 */
bool write_response(SOCKET client, int status, const std::string& body) {
    const char* reason = status == 200 ? "OK" : status == 400 ? "Bad Request" : status == 401 ? "Unauthorized" : status == 404 ? "Not Found" : "Internal Server Error";
    std::ostringstream response;
    response << "HTTP/1.1 " << status << ' ' << reason << "\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: "
             << body.size() << "\r\nConnection: close\r\n\r\n" << body;
    const std::string bytes = response.str();
    std::size_t written = 0;
    while (written < bytes.size()) {
        const int count = send(client, bytes.data() + written, static_cast<int>(bytes.size() - written), 0);
        if (count == SOCKET_ERROR) return false;
        written += static_cast<std::size_t>(count);
    }
    return true;
}

/** 读取有上限的 HTTP 请求；验证请求之外的复杂协议不在夹具边界内。 */
bool read_request(SOCKET client, std::string& method, std::string& path, std::map<std::string, std::string>& headers, std::string& body, std::string& error) {
    std::string bytes;
    char buffer[4096];
    std::size_t expected_size = 0;
    while (bytes.size() <= kMaxRequestBytes) {
        const int count = recv(client, buffer, sizeof(buffer), 0);
        if (count <= 0) {
            error = count == 0 ? "connection closed before request complete" : "recv failed: " + std::to_string(WSAGetLastError());
            return false;
        }
        bytes.append(buffer, static_cast<std::size_t>(count));
        const std::size_t header_end = bytes.find("\r\n\r\n");
        if (header_end != std::string::npos) {
            if (expected_size == 0) {
                const std::string head = bytes.substr(0, header_end);
                const std::string marker = "Content-Length:";
                std::size_t position = head.find(marker);
                if (position == std::string::npos) position = head.find("content-length:");
                std::size_t content_length = 0;
                if (position != std::string::npos) {
                    try {
                        content_length = static_cast<std::size_t>(std::stoul(head.substr(position + marker.size())));
                    } catch (const std::exception& exception) {
                        error = "invalid content-length: " + std::string(exception.what());
                        return false;
                    }
                }
                expected_size = header_end + 4 + content_length;
            }
            if (bytes.size() >= expected_size) break;
        }
    }
    if (bytes.size() > kMaxRequestBytes) {
        error = "request exceeds 64 KiB";
        return false;
    }
    const std::size_t header_end = bytes.find("\r\n\r\n");
    if (header_end == std::string::npos) {
        error = "malformed HTTP headers";
        return false;
    }
    std::istringstream head(bytes.substr(0, header_end));
    std::string request_line;
    std::getline(head, request_line);
    if (!request_line.empty() && request_line.back() == '\r') request_line.pop_back();
    std::istringstream request(request_line);
    request >> method >> path;
    std::string line;
    while (std::getline(head, line)) {
        if (!line.empty() && line.back() == '\r') line.pop_back();
        const std::size_t colon = line.find(':');
        if (colon == std::string::npos) continue;
        std::string name = line.substr(0, colon);
        for (char& character : name) character = static_cast<char>(std::tolower(static_cast<unsigned char>(character)));
        std::string value = line.substr(colon + 1);
        const std::size_t first = value.find_first_not_of(" \t");
        headers[name] = first == std::string::npos ? "" : value.substr(first);
    }
    body = bytes.substr(header_end + 4);
    return true;
}

/** 处理单条连接；受控业务错误不会终止 listener。 */
void handle_client(SOCKET client) {
    DWORD timeout_ms = 5000;
    setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout_ms), sizeof(timeout_ms));
    std::string method;
    std::string path;
    std::map<std::string, std::string> headers;
    std::string body;
    std::string error;
    if (!read_request(client, method, path, headers, body, error)) {
        write_log("error", "fixture_request_failed", {{"reason", "invalid_http"}, {"cause", error}});
        closesocket(client);
        return;
    }
    if (method == "GET" && path == "/healthz") {
        write_response(client, 200, "{\"ready\":true,\"contract_version\":\"v1\",\"provider\":\"cpp\"}");
        write_log("info", "fixture_readiness_succeeded", {{"status", "200"}});
        closesocket(client);
        return;
    }
    if (method != "POST" || path != "/api/probe") {
        write_response(client, 404, "{\"ok\":false,\"code\":\"fixture_not_found\",\"provider\":\"cpp\"}");
        write_log("error", "fixture_request_rejected", {{"reason", "route_not_found"}, {"status", "404"}});
        closesocket(client);
        return;
    }
    const auto auth = headers.find("authorization");
    if (!is_authorized(auth == headers.end() ? std::nullopt : std::optional<std::string>(auth->second))) {
        write_response(client, 401, "{\"ok\":false,\"code\":\"fixture_unauthorized\",\"provider\":\"cpp\"}");
        write_log("error", "fixture_request_rejected", {{"reason", "unauthorized"}, {"status", "401"}});
        closesocket(client);
        return;
    }
    const std::string trace_id = json_string(body, "trace_id").value_or("");
    const std::string request_id = json_string(body, "request_id").value_or("");
    if (trace_id.empty() || request_id.empty()) {
        write_response(client, 400, "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"cpp\"}");
        write_log("error", "fixture_request_failed", {{"reason", "correlation_id_required"}, {"status", "400"}});
        closesocket(client);
        return;
    }
    const std::string outcome = json_string(body, "outcome").value_or("ok") == "error" ? "error" : "ok";
    write_log("info", "fixture_request_started", {{"trace_id", trace_id}, {"request_id", request_id}, {"outcome", outcome}});
    const ProbeResult probe = fixture_probe(json_integer(body, "value", 41));
    const int status = outcome == "error" ? 500 : 200;
    const std::string code = outcome == "error" ? "fixture_controlled_error" : "fixture_ok";
    std::ostringstream response;
    response << "{\"ok\":" << (status == 200 ? "true" : "false") << ",\"code\":\"" << code << "\",\"provider\":\"cpp\",\"trace_id\":\""
             << json_escape(trace_id) << "\",\"request_id\":\"" << json_escape(request_id) << "\",\"result\":" << probe.fixture_count << '}';
    write_response(client, status, response.str());
    write_log(status == 500 ? "error" : "info", "fixture_request_completed", {{"trace_id", trace_id}, {"request_id", request_id}, {"outcome", outcome}, {"status", std::to_string(status)}});
    closesocket(client);
}

}  // namespace

/** 程序入口：初始化 WinSock、注册控制台停止处理器并运行非阻塞 accept 循环。 */
int main() {
    if (env_or_default("FIXTURE_STARTUP_MODE", "") == "fail") {
        write_log("error", "fixture_startup_failed", {{"reason", "controlled_startup_failure"}});
        return 23;
    }
    if (env_or_default("FIXTURE_CAMPAIGN_ID", "").empty()) {
        write_log("error", "fixture_startup_failed", {{"stage", "configuration"}, {"cause", "FIXTURE_CAMPAIGN_ID is required"}});
        return 24;
    }
    WSADATA data{};
    if (WSAStartup(MAKEWORD(2, 2), &data) != 0) {
        write_log("error", "fixture_startup_failed", {{"stage", "winsock_startup"}, {"cause", std::to_string(WSAGetLastError())}});
        return 24;
    }
    if (!SetConsoleCtrlHandler(console_handler, TRUE)) {
        write_log("error", "fixture_startup_failed", {{"stage", "register_console_handler"}, {"cause", std::to_string(GetLastError())}});
        WSACleanup();
        return 24;
    }
    unsigned long parsed_port = kDefaultPort;
    try {
        parsed_port = std::stoul(env_or_default("FIXTURE_PORT", "18176"));
        if (parsed_port == 0 || parsed_port > 65535) throw std::out_of_range("port must be between 1 and 65535");
    } catch (const std::exception& error) {
        write_log("error", "fixture_startup_failed", {{"stage", "parse_port"}, {"cause", error.what()}});
        WSACleanup();
        return 24;
    }
    SOCKET listener = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (listener == INVALID_SOCKET) {
        write_log("error", "fixture_startup_failed", {{"stage", "socket"}, {"cause", std::to_string(WSAGetLastError())}});
        WSACleanup();
        return 24;
    }
    sockaddr_in address{};
    address.sin_family = AF_INET;
    address.sin_port = htons(static_cast<unsigned short>(parsed_port));
    inet_pton(AF_INET, "127.0.0.1", &address.sin_addr);
    if (bind(listener, reinterpret_cast<sockaddr*>(&address), sizeof(address)) == SOCKET_ERROR || listen(listener, SOMAXCONN) == SOCKET_ERROR) {
        write_log("error", "fixture_startup_failed", {{"stage", "listen"}, {"port", std::to_string(parsed_port)}, {"cause", std::to_string(WSAGetLastError())}});
        closesocket(listener);
        WSACleanup();
        return 24;
    }
    unsigned long nonblocking = 1;
    ioctlsocket(listener, FIONBIO, &nonblocking);
    write_log("info", "fixture_started", {{"host", "127.0.0.1"}, {"port", std::to_string(parsed_port)}, {"contract_version", kContractVersion}});
    while (running.load()) {
        SOCKET client = accept(listener, nullptr, nullptr);
        if (client == INVALID_SOCKET) {
            const int code = WSAGetLastError();
            if (code != WSAEWOULDBLOCK) {
                write_log("error", "fixture_run_failed", {{"stage", "accept"}, {"cause", std::to_string(code)}});
                closesocket(listener);
                WSACleanup();
                return 25;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(50));
            continue;
        }
        // 验证流量是串行的；同步处理可保证 cleanup 前没有脱离管理的 client 线程。
        handle_client(client);
    }
    write_log("info", "fixture_stopping", {{"signal", "windows_console"}});
    closesocket(listener);
    WSACleanup();
    write_log("info", "fixture_stopped", {{"signal", "windows_console"}});
    return 0;
}

#else

#include <cstdio>

/** 写入非 Windows 静态检查哨兵日志；该分支绝不作为 Windows verdict。 */
void write_platform_guard_log() {
    constexpr char line[] = "{\"level\":\"error\",\"event\":\"fixture_platform_unsupported\",\"provider\":\"cpp\",\"required\":\"windows-x64\"}\n";
    std::fwrite(line, 1, sizeof(line) - 1, stderr);
    std::fflush(stderr);
}

/** 非 Windows 主机只提供静态编译哨兵，不执行或声称 Windows 行为。 */
int main() {
    write_platform_guard_log();
    return 64;
}

#endif
