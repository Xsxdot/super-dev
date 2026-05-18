import Foundation
import GRDB

struct LogEntry: Identifiable, Codable, FetchableRecord, PersistableRecord {
    let id: UUID
    var timestamp: Date
    var serviceId: UUID
    var serviceName: String
    var level: LogLevel
    var message: String
    var normalizedMessage: String
    var runId: UUID
    var repeatCount: Int

    init(
        id: UUID = UUID(),
        timestamp: Date = Date(),
        serviceId: UUID,
        serviceName: String,
        level: LogLevel,
        message: String,
        normalizedMessage: String,
        runId: UUID,
        repeatCount: Int = 1
    ) {
        self.id = id
        self.timestamp = timestamp
        self.serviceId = serviceId
        self.serviceName = serviceName
        self.level = level
        self.message = message
        self.normalizedMessage = normalizedMessage
        self.runId = runId
        self.repeatCount = repeatCount
    }

    static let databaseTableName = "log_entries"
}

enum LogLevel: String, Codable, CaseIterable {
    case error = "ERROR"
    case warn  = "WARN"
    case info  = "INFO"
    case debug = "DEBUG"
    case unknown = "UNKNOWN"

    var priority: Int {
        switch self {
        case .error: return 4
        case .warn:  return 3
        case .info:  return 2
        case .debug: return 1
        case .unknown: return 0
        }
    }
}

extension LogEntry {
    enum Columns {
        static let id = Column("id")
        static let timestamp = Column("timestamp")
        static let serviceId = Column("service_id")
        static let serviceName = Column("service_name")
        static let level = Column("level")
        static let message = Column("message")
        static let normalizedMessage = Column("normalized_message")
        static let runId = Column("run_id")
        static let repeatCount = Column("repeat_count")
    }

    init(row: Row) throws {
        id = UUID(uuidString: row[Columns.id]) ?? UUID()
        timestamp = row[Columns.timestamp]
        serviceId = UUID(uuidString: row[Columns.serviceId]) ?? UUID()
        serviceName = row[Columns.serviceName]
        level = LogLevel(rawValue: row[Columns.level]) ?? .unknown
        message = row[Columns.message]
        normalizedMessage = row[Columns.normalizedMessage]
        runId = UUID(uuidString: row[Columns.runId]) ?? UUID()
        repeatCount = row[Columns.repeatCount]
    }

    func encode(to container: inout PersistenceContainer) throws {
        container[Columns.id] = id.uuidString
        container[Columns.timestamp] = timestamp
        container[Columns.serviceId] = serviceId.uuidString
        container[Columns.serviceName] = serviceName
        container[Columns.level] = level.rawValue
        container[Columns.message] = message
        container[Columns.normalizedMessage] = normalizedMessage
        container[Columns.runId] = runId.uuidString
        container[Columns.repeatCount] = repeatCount
    }
}
