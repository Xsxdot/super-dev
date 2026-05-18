import Foundation
import GRDB

struct LogEntry: Identifiable, Codable, FetchableRecord, PersistableRecord {
    var id: UUID
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
