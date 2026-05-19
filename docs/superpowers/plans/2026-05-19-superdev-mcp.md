# SuperDev MCP Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed a stdio MCP server (`superdev-mcp`) into SuperDev.app so Claude Code can control services and query logs directly.

**Architecture:** SuperDev.app gains a `ControlSocketServer` that listens on a Unix Domain Socket for control commands (list/restart/stop). A separate Swift CLI target `superdev-mcp`, bundled inside the app, acts as the MCP stdio server — it reads logs directly from the shared SQLite DB and sends control commands over the UDS socket.

**Tech Stack:** Swift 5.9+, Xcode, GRDB (already a project dependency via local SPM), DispatchSource for UDS server, Foundation JSON encoding/decoding.

---

## File Map

### New Files
- `SuperDev/SuperDev/AppCore/MCP/ControlSocketServer.swift` — UDS server in main app
- `SuperDev/superdev-mcp/main.swift` — CLI entry point, MCP stdio loop
- `SuperDev/superdev-mcp/UDSClient.swift` — connects to control.sock, sends/receives JSON
- `SuperDev/superdev-mcp/SQLiteReader.swift` — opens logs.db with GRDB, runs query_logs
- `SuperDev/SuperDevTests/ControlSocketServerTests.swift` — UDS round-trip tests

### Modified Files
- `SuperDev/SuperDev/AppCore/AppCore.swift` — add `controlServer` property, start/stop it
- `SuperDev/SuperDev/UI/Settings/SettingsView.swift` — add MCP info section at bottom

---

## Task 1: ControlSocketServer — UDS listener in SuperDev

**Files:**
- Create: `SuperDev/SuperDev/AppCore/MCP/ControlSocketServer.swift`

### Background

`ControlSocketServer` listens on `~/Library/Application Support/SuperDev/control.sock`. For each incoming connection it reads one line of JSON, dispatches to main thread to call `AppCore`, writes one line of JSON response, then closes the connection.

UDS protocol (request → response):
```
{ "action": "list_services" }
→ { "ok": true, "data": [{ "project": "myapp", "service": "api", "status": "running" }] }

{ "action": "restart_service", "target": "myapp/api" }
→ { "ok": true, "message": "restarted" }
→ { "ok": false, "error": "service not found: myapp/api" }

{ "action": "stop_service", "target": "myapp/api" }
→ { "ok": true, "message": "stopped" }
```

- [ ] **Step 1: Create the file with skeleton**

Create `SuperDev/SuperDev/AppCore/MCP/ControlSocketServer.swift`:

```swift
// ControlSocketServer 监听 Unix Domain Socket，接收来自 superdev-mcp 的控制指令。
//
// 职责：
//   - 在 ~/Library/Application Support/SuperDev/control.sock 监听连接
//   - 每个连接读取一行 JSON 请求，回写一行 JSON 响应后关闭连接
//   - 将控制指令（restart/stop/list）调度到主线程执行
//
// 边界：
//   - 不持有业务状态，通过 weak AppCore 引用执行操作
//   - 每次连接独立处理，不维持长连接
import Foundation

@MainActor
final class ControlSocketServer {
    private weak var core: AppCore?
    private var serverFD: Int32 = -1
    private var dispatchSource: DispatchSourceRead?

    static var socketPath: String {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        return (support?.appendingPathComponent("SuperDev/control.sock").path) ?? "/tmp/superdev-control.sock"
    }

    init(core: AppCore) {
        self.core = core
    }

    func start() {
        // implemented in next step
    }

    func stop() {
        dispatchSource?.cancel()
        if serverFD >= 0 { close(serverFD); serverFD = -1 }
        unlink(ControlSocketServer.socketPath)
    }
}
```

- [ ] **Step 2: Implement `start()` — bind and listen on UDS**

Replace the `start()` stub in `ControlSocketServer.swift`:

```swift
func start() {
    let path = ControlSocketServer.socketPath
    // Remove stale socket from previous run
    unlink(path)

    serverFD = socket(AF_UNIX, SOCK_STREAM, 0)
    guard serverFD >= 0 else { return }

    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    withUnsafeMutableBytes(of: &addr.sun_path) { ptr in
        path.withCString { src in
            _ = strlcpy(ptr.baseAddress!.assumingMemoryBound(to: CChar.self), src, ptr.count)
        }
    }

    let bindResult = withUnsafePointer(to: &addr) { ptr in
        ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            bind(serverFD, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard bindResult == 0 else { close(serverFD); serverFD = -1; return }
    guard listen(serverFD, 5) == 0 else { close(serverFD); serverFD = -1; return }

    let source = DispatchSource.makeReadSource(fileDescriptor: serverFD, queue: .global(qos: .utility))
    source.setEventHandler { [weak self] in self?.acceptConnection() }
    source.resume()
    dispatchSource = source
}
```

- [ ] **Step 3: Implement `acceptConnection()` and `handleConnection(_:)`**

Add these methods to `ControlSocketServer`:

```swift
private func acceptConnection() {
    let clientFD = accept(serverFD, nil, nil)
    guard clientFD >= 0 else { return }
    DispatchQueue.global(qos: .utility).async { [weak self] in
        self?.handleConnection(clientFD)
    }
}

private func handleConnection(_ fd: Int32) {
    defer { close(fd) }

    // Read until newline (max 4KB)
    var buffer = [UInt8](repeating: 0, count: 4096)
    var received = ""
    while received.last != "\n" {
        let n = read(fd, &buffer, buffer.count)
        guard n > 0 else { break }
        received += String(bytes: buffer.prefix(n), encoding: .utf8) ?? ""
    }
    let line = received.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !line.isEmpty,
          let data = line.data(using: .utf8),
          let request = try? JSONDecoder().decode(ControlRequest.self, from: data) else {
        writeResponse(fd, ControlResponse(ok: false, error: "invalid request"))
        return
    }

    // Dispatch to main thread to touch AppCore state
    let semaphore = DispatchSemaphore(value: 0)
    var response = ControlResponse(ok: false, error: "unknown error")
    DispatchQueue.main.async { [weak self] in
        response = self?.handle(request) ?? ControlResponse(ok: false, error: "server not ready")
        semaphore.signal()
    }
    semaphore.wait()
    writeResponse(fd, response)
}

private func writeResponse(_ fd: Int32, _ response: ControlResponse) {
    guard let data = try? JSONEncoder().encode(response),
          let line = String(data: data, encoding: .utf8) else { return }
    let out = line + "\n"
    out.withCString { ptr in
        _ = Foundation.write(fd, ptr, strlen(ptr))
    }
}
```

- [ ] **Step 4: Define request/response types and `handle(_:)` dispatcher**

Add to `ControlSocketServer.swift`:

```swift
// MARK: - Protocol types

struct ControlRequest: Decodable {
    let action: String
    let target: String?  // "项目名/服务名" for restart/stop
}

struct ServiceInfo: Encodable {
    let project: String
    let service: String
    let status: String
}

struct ControlResponse: Encodable {
    let ok: Bool
    var message: String?
    var error: String?
    var data: [ServiceInfo]?
}

// MARK: - Action dispatch (runs on MainActor)

@MainActor
private func handle(_ request: ControlRequest) -> ControlResponse {
    switch request.action {
    case "list_services":
        return handleListServices()
    case "restart_service":
        return handleServiceAction(target: request.target, action: .restart)
    case "stop_service":
        return handleServiceAction(target: request.target, action: .stop)
    default:
        return ControlResponse(ok: false, error: "unknown action: \(request.action)")
    }
}

@MainActor
private func handleListServices() -> ControlResponse {
    guard let core else { return ControlResponse(ok: false, error: "core unavailable") }
    let infos = core.projects.flatMap { project in
        project.services.map { service in
            ServiceInfo(
                project: project.name,
                service: service.name,
                status: service.status.rawStringValue
            )
        }
    }
    return ControlResponse(ok: true, data: infos)
}

private enum ServiceAction { case restart, stop }

@MainActor
private func handleServiceAction(target: String?, action: ServiceAction) -> ControlResponse {
    guard let core else { return ControlResponse(ok: false, error: "core unavailable") }
    guard let target else { return ControlResponse(ok: false, error: "target required") }

    let parts = target.split(separator: "/", maxSplits: 1).map(String.init)
    guard parts.count == 2 else {
        return ControlResponse(ok: false, error: "target must be 'project/service', got: \(target)")
    }
    let projectName = parts[0]
    let serviceName = parts[1]

    guard let project = core.projects.first(where: { $0.name == projectName }),
          let service = project.services.first(where: { $0.name == serviceName }) else {
        return ControlResponse(ok: false, error: "service not found: \(target)")
    }

    switch action {
    case .restart:
        core.restart(service, in: project)
        return ControlResponse(ok: true, message: "restarted \(target)")
    case .stop:
        core.stop(service, in: project)
        return ControlResponse(ok: true, message: "stopped \(target)")
    }
}
```

- [ ] **Step 5: Add `rawStringValue` to `ServiceStatus`**

In `SuperDev/SuperDev/AppCore/Models/Project.swift`, add an extension after the `ServiceStatus` enum:

```swift
extension ServiceStatus {
    var rawStringValue: String {
        switch self {
        case .stopped:  return "stopped"
        case .starting: return "starting"
        case .running:  return "running"
        case .failed:   return "failed"
        }
    }
}
```

- [ ] **Step 6: Wire ControlSocketServer into AppCore**

In `SuperDev/SuperDev/AppCore/AppCore.swift`:

Add property after `private var processManagers`:
```swift
private var controlServer: ControlSocketServer?
```

At the end of `init()`, after `performStartupLogMaintenance()`:
```swift
controlServer = ControlSocketServer(core: self)
controlServer?.start()
```

- [ ] **Step 7: Build and manually test with nc**

Build the SuperDev target in Xcode (Cmd+B). Then launch the app and test:

```bash
# List services
echo '{"action":"list_services"}' | nc -U ~/Library/Application\ Support/SuperDev/control.sock

# Expected (with no projects loaded):
# {"ok":true,"data":[]}

# With a project running:
# {"ok":true,"data":[{"project":"myapp","service":"api","status":"running"}]}
```

- [ ] **Step 8: Commit**

```bash
git add SuperDev/SuperDev/AppCore/MCP/ControlSocketServer.swift \
        SuperDev/SuperDev/AppCore/AppCore.swift \
        SuperDev/SuperDev/AppCore/Models/Project.swift
git commit -m "feat: add ControlSocketServer UDS listener to SuperDev"
```

---

## Task 2: superdev-mcp CLI target — Xcode setup + MCP stdio skeleton

**Files:**
- Create: `SuperDev/superdev-mcp/main.swift`

### Background

`superdev-mcp` is a new **Command Line Tool** target added to the existing Xcode project. It implements the MCP stdio protocol: reads JSON-RPC from stdin line by line, writes JSON-RPC responses to stdout. This task covers the Xcode target creation and the MCP handshake (`initialize` + `tools/list`).

MCP JSON-RPC message format (each message is one line of JSON):
```json
// Claude Code → superdev-mcp
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}

// superdev-mcp → Claude Code
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"superdev","version":"1.0"}}}
// (no response for notifications/initialized)
{"jsonrpc":"2.0","id":2,"result":{"tools":[...]}}
```

- [ ] **Step 1: Add a Command Line Tool target in Xcode**

In Xcode:
1. File → New → Target → macOS → Command Line Tool
2. Product Name: `superdev-mcp`
3. Language: Swift
4. Do NOT add to any test targets

This creates `SuperDev/superdev-mcp/main.swift`.

- [ ] **Step 2: Add GRDB dependency to the new target**

In Xcode, select the `superdev-mcp` target → General → Frameworks and Libraries → `+` → add `GRDB` (the same local package already used by the main app).

- [ ] **Step 3: Write MCP protocol types**

Replace the contents of `SuperDev/superdev-mcp/main.swift`:

```swift
import Foundation

// MARK: - JSON-RPC types

struct JSONRPCRequest: Decodable {
    let jsonrpc: String
    let id: JSONRPCId?
    let method: String
    let params: JSONRPCParams?
}

enum JSONRPCId: Codable {
    case int(Int)
    case string(String)

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let i = try? c.decode(Int.self) { self = .int(i); return }
        if let s = try? c.decode(String.self) { self = .string(s); return }
        throw DecodingError.typeMismatch(JSONRPCId.self, .init(codingPath: [], debugDescription: "expected int or string"))
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self { case .int(let i): try c.encode(i); case .string(let s): try c.encode(s) }
    }
}

struct JSONRPCParams: Decodable {
    let protocolVersion: String?
    // tools/call params
    let name: String?
    let arguments: [String: JSONValue]?
}

enum JSONValue: Codable {
    case string(String), int(Int), bool(Bool), null

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let s = try? c.decode(String.self) { self = .string(s); return }
        if let i = try? c.decode(Int.self) { self = .int(i); return }
        if let b = try? c.decode(Bool.self) { self = .bool(b); return }
        self = .null
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .int(let i): try c.encode(i)
        case .bool(let b): try c.encode(b)
        case .null: try c.encodeNil()
        }
    }

    var stringValue: String? { if case .string(let s) = self { return s }; return nil }
    var intValue: Int? { if case .int(let i) = self { return i }; return nil }
}

struct JSONRPCResponse: Encodable {
    let jsonrpc = "2.0"
    let id: JSONRPCId?
    let result: JSONRPCResult?
    let error: JSONRPCError?
}

enum JSONRPCResult: Encodable {
    case initialize(InitializeResult)
    case toolsList(ToolsListResult)
    case toolCall(ToolCallResult)

    func encode(to encoder: Encoder) throws {
        switch self {
        case .initialize(let r): try r.encode(to: encoder)
        case .toolsList(let r): try r.encode(to: encoder)
        case .toolCall(let r): try r.encode(to: encoder)
        }
    }
}

struct JSONRPCError: Encodable {
    let code: Int
    let message: String
}

struct InitializeResult: Encodable {
    let protocolVersion = "2024-11-05"
    let capabilities = InitCapabilities()
    let serverInfo = ServerInfo()
    struct InitCapabilities: Encodable { let tools = ToolsCap(); struct ToolsCap: Encodable {} }
    struct ServerInfo: Encodable { let name = "superdev"; let version = "1.0" }
}

struct ToolsListResult: Encodable {
    let tools: [MCPTool]
}

struct MCPTool: Encodable {
    let name: String
    let description: String
    let inputSchema: InputSchema

    struct InputSchema: Encodable {
        let type = "object"
        let properties: [String: PropertyDef]
        let required: [String]?
    }
    struct PropertyDef: Encodable {
        let type: String
        let description: String
    }
}

struct ToolCallResult: Encodable {
    let content: [ContentBlock]
    struct ContentBlock: Encodable {
        let type = "text"
        let text: String
    }
}
```

- [ ] **Step 4: Write the tool definitions and MCP main loop**

Append to `main.swift`:

```swift
// MARK: - Tool definitions

let allTools: [MCPTool] = [
    MCPTool(
        name: "list_services",
        description: "List all services managed by SuperDev with their current status.",
        inputSchema: MCPTool.InputSchema(properties: [:], required: nil)
    ),
    MCPTool(
        name: "restart_service",
        description: "Restart a service. target format: 'project/service' e.g. 'myapp/api'",
        inputSchema: MCPTool.InputSchema(
            properties: ["target": .init(type: "string", description: "project/service")],
            required: ["target"]
        )
    ),
    MCPTool(
        name: "stop_service",
        description: "Stop a running service. target format: 'project/service'",
        inputSchema: MCPTool.InputSchema(
            properties: ["target": .init(type: "string", description: "project/service")],
            required: ["target"]
        )
    ),
    MCPTool(
        name: "query_logs",
        description: "Query logs from SuperDev's SQLite database. Returns up to 'limit' entries.",
        inputSchema: MCPTool.InputSchema(
            properties: [
                "service":  .init(type: "string", description: "project/service filter (optional)"),
                "level":    .init(type: "string", description: "ERROR|WARN|INFO|DEBUG (optional)"),
                "keyword":  .init(type: "string", description: "substring match on message (optional)"),
                "limit":    .init(type: "string", description: "max results, default 100, max 500"),
                "since":    .init(type: "string", description: "ISO8601 timestamp, return entries after this time (optional)")
            ],
            required: nil
        )
    )
]

// MARK: - MCP stdio main loop

let encoder = JSONEncoder()
encoder.outputFormatting = []

func sendResponse(_ response: JSONRPCResponse) {
    guard let data = try? encoder.encode(response),
          let line = String(data: data, encoding: .utf8) else { return }
    print(line)
    fflush(stdout)
}

func sendError(id: JSONRPCId?, code: Int, message: String) {
    sendResponse(JSONRPCResponse(id: id, result: nil, error: JSONRPCError(code: code, message: message)))
}

while let line = readLine() {
    let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty,
          let data = trimmed.data(using: .utf8),
          let request = try? JSONDecoder().decode(JSONRPCRequest.self, from: data) else {
        continue
    }

    switch request.method {
    case "initialize":
        sendResponse(JSONRPCResponse(id: request.id, result: .initialize(InitializeResult()), error: nil))

    case "notifications/initialized":
        break  // no response for notifications

    case "tools/list":
        sendResponse(JSONRPCResponse(id: request.id, result: .toolsList(ToolsListResult(tools: allTools)), error: nil))

    case "tools/call":
        guard let toolName = request.params?.name else {
            sendError(id: request.id, code: -32602, message: "missing tool name")
            continue
        }
        let args = request.params?.arguments ?? [:]
        let result = handleToolCall(name: toolName, args: args)
        sendResponse(JSONRPCResponse(id: request.id, result: .toolCall(result), error: nil))

    default:
        sendError(id: request.id, code: -32601, message: "method not found: \(request.method)")
    }
}
```

- [ ] **Step 5: Add `handleToolCall` stub (control tools only)**

Append to `main.swift`:

```swift
// MARK: - Tool dispatch

func handleToolCall(name: String, args: [String: JSONValue]) -> ToolCallResult {
    switch name {
    case "list_services":
        return udsCall(request: ["action": "list_services"])
    case "restart_service":
        guard let target = args["target"]?.stringValue else {
            return ToolCallResult(content: [.init(text: "error: target is required")])
        }
        return udsCall(request: ["action": "restart_service", "target": target])
    case "stop_service":
        guard let target = args["target"]?.stringValue else {
            return ToolCallResult(content: [.init(text: "error: target is required")])
        }
        return udsCall(request: ["action": "stop_service", "target": target])
    case "query_logs":
        return queryLogs(args: args)
    default:
        return ToolCallResult(content: [.init(text: "error: unknown tool \(name)")])
    }
}
```

- [ ] **Step 6: Implement `udsCall(_:)` — send request to ControlSocketServer**

Append to `main.swift`:

```swift
// MARK: - UDS client

func socketPath() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return "\(home)/Library/Application Support/SuperDev/control.sock"
}

func udsCall(request: [String: String]) -> ToolCallResult {
    let path = socketPath()

    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else {
        return ToolCallResult(content: [.init(text: "error: could not create socket")])
    }
    defer { close(fd) }

    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    withUnsafeMutableBytes(of: &addr.sun_path) { ptr in
        path.withCString { src in _ = strlcpy(ptr.baseAddress!.assumingMemoryBound(to: CChar.self), src, ptr.count) }
    }

    let connected = withUnsafePointer(to: &addr) { ptr in
        ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard connected == 0 else {
        return ToolCallResult(content: [.init(text: "error: SuperDev is not running (could not connect to control socket)")])
    }

    guard let payload = try? JSONEncoder().encode(request),
          let line = String(data: payload, encoding: .utf8) else {
        return ToolCallResult(content: [.init(text: "error: could not encode request")])
    }
    let toSend = line + "\n"
    toSend.withCString { ptr in _ = Foundation.write(fd, ptr, strlen(ptr)) }

    // Read response
    var buffer = [UInt8](repeating: 0, count: 65536)
    var response = ""
    while response.last != "\n" {
        let n = read(fd, &buffer, buffer.count)
        guard n > 0 else { break }
        response += String(bytes: buffer.prefix(n), encoding: .utf8) ?? ""
    }

    return ToolCallResult(content: [.init(text: response.trimmingCharacters(in: .whitespacesAndNewlines))])
}
```

- [ ] **Step 7: Build and smoke test**

Build `superdev-mcp` target in Xcode. Then with SuperDev running, test from terminal:

```bash
# Path after build (Debug):
BINARY=~/Library/Developer/Xcode/DerivedData/SuperDev-*/Build/Products/Debug/superdev-mcp

# Test initialize handshake
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_services","arguments":{}}}' | $BINARY
```

Expected output (4 lines, last is empty because notifications/initialized has no response):
```
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05",...}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[...]}}
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"{\"ok\":true,\"data\":[...]}"}]}}
```

- [ ] **Step 8: Commit**

```bash
git add SuperDev/superdev-mcp/
git commit -m "feat: add superdev-mcp CLI target with MCP stdio + control tools"
```

---

## Task 3: query_logs — SQLite reader in superdev-mcp

**Files:**
- Modify: `SuperDev/superdev-mcp/main.swift` — replace `queryLogs` stub with real implementation

### Background

`query_logs` opens the shared SQLite DB at `~/Library/Application Support/SuperDev/logs.db` using GRDB. It queries `log_entries` with optional filters. The `log_entries` table schema (from `LogStore.swift`):

```
id TEXT PRIMARY KEY
timestamp DATETIME NOT NULL
service_id TEXT NOT NULL
service_name TEXT NOT NULL
level TEXT NOT NULL          -- "ERROR"|"WARN"|"INFO"|"DEBUG"|"UNKNOWN"
message TEXT NOT NULL
normalized_message TEXT NOT NULL
run_id TEXT NOT NULL
repeat_count INTEGER NOT NULL DEFAULT 1
```

Note: `log_entries` does NOT have a `project_name` column. To map service → project we need a second source. Since `superdev-mcp` can't access `AppCore` state, we resolve the project name via UDS `list_services` at query time (call list_services, build a `serviceName → projectName` map, apply when formatting results).

- [ ] **Step 1: Add GRDB import and DB path helper**

At the top of `main.swift`, add after the existing imports:

```swift
import GRDB
```

Add this helper function before `handleToolCall`:

```swift
func dbPath() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return "\(home)/Library/Application Support/SuperDev/logs.db"
}
```

- [ ] **Step 2: Implement `queryLogs(args:)`**

Replace the `queryLogs` stub (the `case "query_logs":` line in `handleToolCall` calls `queryLogs(args:)`) — append this function to `main.swift`:

```swift
func queryLogs(args: [String: JSONValue]) -> ToolCallResult {
    // Parse args
    let serviceFilter = args["service"]?.stringValue   // "project/service" or "service"
    let levelFilter   = args["level"]?.stringValue?.uppercased()
    let keyword       = args["keyword"]?.stringValue
    let limit         = min(args["limit"]?.intValue ?? 100, 500)
    let sinceStr      = args["since"]?.stringValue

    // Parse since date
    var sinceDate: Date? = nil
    if let s = sinceStr {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        sinceDate = fmt.date(from: s) ?? ISO8601DateFormatter().date(from: s)
    }

    // Resolve service name filter (strip project prefix if present)
    let serviceNameFilter: String?
    if let sf = serviceFilter {
        let parts = sf.split(separator: "/", maxSplits: 1).map(String.init)
        serviceNameFilter = parts.count == 2 ? parts[1] : parts[0]
    } else {
        serviceNameFilter = nil
    }

    // Fetch project mapping from SuperDev (best-effort, empty map if SuperDev not running)
    var serviceToProject: [String: String] = [:]
    let listResult = udsCall(request: ["action": "list_services"])
    if let text = listResult.content.first?.text,
       let data = text.data(using: .utf8),
       let parsed = try? JSONDecoder().decode(ListServicesResponse.self, from: data),
       parsed.ok {
        for item in parsed.data ?? [] {
            serviceToProject[item.service] = item.project
        }
    }

    // Open DB
    guard let db = try? DatabaseQueue(path: dbPath()) else {
        return ToolCallResult(content: [.init(text: "error: could not open logs.db")])
    }

    // Build query
    var conditions: [String] = []
    var arguments: [DatabaseValue] = []

    if let sn = serviceNameFilter {
        conditions.append("service_name = ?")
        arguments.append(sn.databaseValue)
    }
    if let lv = levelFilter, !lv.isEmpty {
        conditions.append("level = ?")
        arguments.append(lv.databaseValue)
    }
    if let kw = keyword, !kw.isEmpty {
        conditions.append("message LIKE ?")
        arguments.append("%\(kw)%".databaseValue)
    }
    if let since = sinceDate {
        conditions.append("timestamp >= ?")
        arguments.append(since.databaseValue)
    }

    let whereClause = conditions.isEmpty ? "" : "WHERE " + conditions.joined(separator: " AND ")
    let sql = """
        SELECT timestamp, service_name, level, message
        FROM log_entries
        \(whereClause)
        ORDER BY timestamp DESC
        LIMIT \(limit)
        """

    struct Row: FetchableRecord {
        let timestamp: Date
        let serviceName: String
        let level: String
        let message: String

        init(row: GRDB.Row) throws {
            timestamp   = row["timestamp"]
            serviceName = row["service_name"]
            level       = row["level"]
            message     = row["message"]
        }
    }

    let rows = (try? db.read { db in
        try Row.fetchAll(db, sql: sql, arguments: StatementArguments(arguments))
    }) ?? []

    if rows.isEmpty {
        return ToolCallResult(content: [.init(text: "No logs found matching the given filters.")])
    }

    // Format output
    let isoFormatter = ISO8601DateFormatter()
    isoFormatter.formatOptions = [.withInternetDateTime]

    let lines = rows.reversed().map { row -> String in
        let ts = isoFormatter.string(from: row.timestamp)
        let proj = serviceToProject[row.serviceName] ?? "?"
        return "[\(ts)] [\(proj)/\(row.serviceName)] [\(row.level)] \(row.message)"
    }
    return ToolCallResult(content: [.init(text: lines.joined(separator: "\n"))])
}

// Helper decodable for list_services UDS response
private struct ListServicesResponse: Decodable {
    let ok: Bool
    let data: [ServiceEntry]?
    struct ServiceEntry: Decodable {
        let project: String
        let service: String
        let status: String
    }
}
```

- [ ] **Step 3: Build and test query_logs**

With SuperDev running and some logs in the DB:

```bash
BINARY=~/Library/Developer/Xcode/DerivedData/SuperDev-*/Build/Products/Debug/superdev-mcp

echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_logs","arguments":{"limit":10}}}' | $BINARY
```

Expected: last message contains up to 10 log lines formatted as `[timestamp] [project/service] [LEVEL] message`.

Test with level filter:
```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_logs","arguments":{"level":"ERROR","limit":5}}}' | $BINARY
```

- [ ] **Step 4: Commit**

```bash
git add SuperDev/superdev-mcp/main.swift
git commit -m "feat: implement query_logs with SQLite reader in superdev-mcp"
```

---

## Task 4: SettingsView MCP info section

**Files:**
- Modify: `SuperDev/SuperDev/UI/Settings/SettingsView.swift`

### Background

Add a「MCP 集成」section at the bottom of SettingsView showing:
1. The socket path (read-only text, user can see it)
2. A "复制 Claude Code 配置" button that writes the correct JSON to the clipboard

The `superdev-mcp` binary path in production is `/Applications/SuperDev.app/Contents/MacOS/superdev-mcp`. During development, users can see the socket path to verify the server is running.

- [ ] **Step 1: Add MCP section to SettingsView body**

In `SettingsView.swift`, add `mcpSection` to the body after `addButton`:

```swift
var body: some View {
    VStack(alignment: .leading, spacing: 0) {
        logRetentionSection
        Rectangle().fill(Theme.borderPrimary).frame(height: 1)
        projectList
        Rectangle().fill(Theme.borderPrimary).frame(height: 1)
        addButton
        Rectangle().fill(Theme.borderPrimary).frame(height: 1)  // add this
        mcpSection                                                // add this
    }
    // ... rest unchanged
}
```

- [ ] **Step 2: Implement `mcpSection`**

Add this computed property to `SettingsView`:

```swift
private var mcpSection: some View {
    VStack(alignment: .leading, spacing: 10) {
        Text("MCP 集成")
            .font(.system(size: 12, weight: .semibold))
            .foregroundColor(Theme.textPrimary)

        VStack(alignment: .leading, spacing: 4) {
            Text("Control socket")
                .font(.caption)
                .foregroundColor(Theme.textSecondary)
            Text(ControlSocketServer.socketPath)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(Theme.textTertiary)
                .lineLimit(1)
                .truncationMode(.middle)
        }

        Button {
            copyMCPConfig()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "doc.on.clipboard")
                Text("复制 Claude Code 配置")
            }
            .font(.system(size: 11, weight: .medium))
            .foregroundColor(Theme.accent)
        }
        .buttonStyle(.plain)
        .help("复制后粘贴到 .claude/settings.json 的 mcpServers 字段")
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 12)
    .background(Theme.bgElevated)
}

private func copyMCPConfig() {
    let binaryPath: String
    if let bundlePath = Bundle.main.executableURL?
        .deletingLastPathComponent()
        .appendingPathComponent("superdev-mcp").path,
       FileManager.default.fileExists(atPath: bundlePath) {
        binaryPath = bundlePath
    } else {
        binaryPath = "/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"
    }

    let config = """
    {
      "mcpServers": {
        "superdev": {
          "command": "\(binaryPath)"
        }
      }
    }
    """
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(config, forType: .string)
}
```

- [ ] **Step 3: Build and visually verify**

Build SuperDev and open Settings (via menubar). Verify:
- The MCP section appears at the bottom
- Socket path is shown
- Clicking "复制 Claude Code 配置" puts the JSON into clipboard (paste into a text editor to verify)

- [ ] **Step 4: Commit**

```bash
git add SuperDev/SuperDev/UI/Settings/SettingsView.swift
git commit -m "feat: add MCP info section to SettingsView with copy-config button"
```

---

## Task 5: End-to-end verification with Claude Code

**Goal:** Configure Claude Code to use `superdev-mcp` and verify all four tools work in a real session.

- [ ] **Step 1: Build a release-like binary**

Build both targets in Xcode with the `superdev-mcp` scheme. Note the binary path from DerivedData or copy to a known location:

```bash
# Find the built binary
find ~/Library/Developer/Xcode/DerivedData -name "superdev-mcp" -type f 2>/dev/null | head -3
```

- [ ] **Step 2: Add MCP config to Claude Code settings**

Open SuperDev → Settings → click "复制 Claude Code 配置". Then add to `.claude/settings.json` in your project (or `~/.claude/settings.json` for global):

```json
{
  "mcpServers": {
    "superdev": {
      "command": "/path/to/superdev-mcp"
    }
  }
}
```

Restart Claude Code (or run `/mcp` to reload).

- [ ] **Step 3: Verify all four tools in Claude Code**

In a Claude Code session, run:
```
Use the superdev MCP to list all services
```
Expected: Claude calls `list_services` and shows current service statuses.

```
Use superdev MCP to query the last 5 ERROR logs
```
Expected: Claude calls `query_logs` with `level: "ERROR", limit: 5` and shows results.

```
Use superdev MCP to restart the service myapp/api
```
Expected: Claude calls `restart_service` with `target: "myapp/api"`, SuperDev restarts the service.

- [ ] **Step 4: Final commit**

```bash
git add .
git commit -m "feat: SuperDev MCP integration complete — list_services, restart_service, stop_service, query_logs"
```
