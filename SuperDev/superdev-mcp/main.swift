import Foundation
import GRDB

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
        switch self {
        case .int(let i): try c.encode(i)
        case .string(let s): try c.encode(s)
        }
    }
}

struct JSONRPCParams: Decodable {
    let protocolVersion: String?
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

// MARK: - Tool definitions

let allTools: [MCPTool] = [
    MCPTool(
        name: "list_services",
        description: "List all services managed by SuperDev with their current status (stopped/starting/running/failed).",
        inputSchema: MCPTool.InputSchema(properties: [:], required: nil)
    ),
    MCPTool(
        name: "restart_service",
        description: "Restart a service managed by SuperDev. Use list_services first to get valid target names.",
        inputSchema: MCPTool.InputSchema(
            properties: ["target": .init(type: "string", description: "Format: 'project/service', e.g. 'myapp/api'")],
            required: ["target"]
        )
    ),
    MCPTool(
        name: "stop_service",
        description: "Stop a running service managed by SuperDev.",
        inputSchema: MCPTool.InputSchema(
            properties: ["target": .init(type: "string", description: "Format: 'project/service', e.g. 'myapp/api'")],
            required: ["target"]
        )
    ),
    MCPTool(
        name: "query_logs",
        description: "Query logs from SuperDev's SQLite database. Searches across all historical runs. Returns entries in chronological order.",
        inputSchema: MCPTool.InputSchema(
            properties: [
                "service": .init(type: "string", description: "Filter by 'project/service' or just 'service' name (optional)"),
                "level":   .init(type: "string", description: "Filter by log level: ERROR, WARN, INFO, or DEBUG (optional)"),
                "keyword": .init(type: "string", description: "Case-insensitive substring match on log message (optional)"),
                "limit":   .init(type: "integer", description: "Max results to return, default 100, max 500 (optional)"),
                "since":   .init(type: "string", description: "ISO8601 timestamp — only return entries after this time (optional)")
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
        break

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

// MARK: - Tool dispatch

func handleToolCall(name: String, args: [String: JSONValue]) -> ToolCallResult {
    switch name {
    case "list_services":
        return udsCall(request: ["action": "list_services"])
    case "restart_service":
        guard let target = args["target"]?.stringValue else {
            return ToolCallResult(content: [.init(text: "error: target is required (format: 'project/service')")])
        }
        return udsCall(request: ["action": "restart_service", "target": target])
    case "stop_service":
        guard let target = args["target"]?.stringValue else {
            return ToolCallResult(content: [.init(text: "error: target is required (format: 'project/service')")])
        }
        return udsCall(request: ["action": "stop_service", "target": target])
    case "query_logs":
        return queryLogs(args: args)
    default:
        return ToolCallResult(content: [.init(text: "error: unknown tool '\(name)'")])
    }
}

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
        path.withCString { src in
            _ = strlcpy(ptr.baseAddress!.assumingMemoryBound(to: CChar.self), src, ptr.count)
        }
    }

    let connected = withUnsafePointer(to: &addr) { ptr in
        ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard connected == 0 else {
        return ToolCallResult(content: [.init(text: "error: SuperDev is not running — could not connect to control socket at \(path)")])
    }

    guard let payload = try? JSONEncoder().encode(request),
          let line = String(data: payload, encoding: .utf8) else {
        return ToolCallResult(content: [.init(text: "error: could not encode request")])
    }
    let toSend = line + "\n"
    toSend.withCString { ptr in _ = Foundation.write(fd, ptr, strlen(ptr)) }

    var buffer = [UInt8](repeating: 0, count: 65536)
    var response = ""
    while response.last != "\n" {
        let n = read(fd, &buffer, buffer.count)
        guard n > 0 else { break }
        response += String(bytes: buffer.prefix(n), encoding: .utf8) ?? ""
    }

    return ToolCallResult(content: [.init(text: response.trimmingCharacters(in: .whitespacesAndNewlines))])
}

// MARK: - query_logs

func dbPath() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return "\(home)/Library/Application Support/SuperDev/logs.db"
}

func queryLogs(args: [String: JSONValue]) -> ToolCallResult {
    let serviceFilter = args["service"]?.stringValue
    let levelFilter   = args["level"]?.stringValue?.uppercased()
    let keyword       = args["keyword"]?.stringValue
    let limit         = min(args["limit"]?.intValue ?? 100, 500)
    let sinceStr      = args["since"]?.stringValue

    var sinceDate: Date? = nil
    if let s = sinceStr {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        sinceDate = fmt.date(from: s) ?? ISO8601DateFormatter().date(from: s)
    }

    let serviceNameFilter: String?
    if let sf = serviceFilter {
        let parts = sf.split(separator: "/", maxSplits: 1).map(String.init)
        serviceNameFilter = parts.count == 2 ? parts[1] : parts[0]
    } else {
        serviceNameFilter = nil
    }

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

    guard let db = try? DatabaseQueue(path: dbPath()) else {
        return ToolCallResult(content: [.init(text: "error: could not open logs.db at \(dbPath())")])
    }

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

    struct LogRow: FetchableRecord {
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
        try LogRow.fetchAll(db, sql: sql, arguments: StatementArguments(arguments))
    }) ?? []

    if rows.isEmpty {
        return ToolCallResult(content: [.init(text: "No logs found matching the given filters.")])
    }

    let isoFormatter = ISO8601DateFormatter()
    isoFormatter.formatOptions = [.withInternetDateTime]

    let lines = rows.reversed().map { row -> String in
        let ts = isoFormatter.string(from: row.timestamp)
        let proj = serviceToProject[row.serviceName] ?? "?"
        return "[\(ts)] [\(proj)/\(row.serviceName)] [\(row.level)] \(row.message)"
    }
    return ToolCallResult(content: [.init(text: lines.joined(separator: "\n"))])
}

private struct ListServicesResponse: Decodable {
    let ok: Bool
    let data: [ServiceEntry]?
    struct ServiceEntry: Decodable {
        let project: String
        let service: String
        let status: String
    }
}
