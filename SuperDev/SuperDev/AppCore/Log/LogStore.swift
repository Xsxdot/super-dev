// LogStore 负责将 LogEntry 持久化到本地 SQLite 数据库。
//
// 职责：
//   - 管理 logs.db 数据库文件（~/Library/Application Support/SuperDev/logs.db）
//   - 提供日志写入（append）、多条件查询（fetch）和旧 run 清理（deleteOldRuns）
//
// 边界：
//   - 不解析日志行，解析由 LogEngine 负责
//   - 不持有日志内存列表，仅负责持久化读写
//   - 所有数据库操作均为同步阻塞（DatabaseQueue），调用方需在后台线程调用
import Foundation
import GRDB

// SQLite log persistence. Database at ~/Library/Application Support/SuperDev/logs.db
final class LogStore {
    private var db: DatabaseQueue?

    init() {
        setupDatabase()
    }

    nonisolated deinit {}

    // append 将单条 LogEntry 写入数据库。
    //
    // 参数：
    //   - entry: 待持久化的日志条目
    func append(_ entry: LogEntry) {
        try? db?.write { db in
            try entry.insert(db)
        }
    }

    // fetch 按条件查询日志条目，返回最新的 limit 条（按时间倒序）。
    //
    // 参数：
    //   - serviceId: 可选，按服务 ID 过滤
    //   - runId:     可选，按运行 ID 过滤
    //   - levels:    可选，按日志级别集合过滤
    //   - keyword:   可选，按消息关键字（LIKE）过滤
    //   - limit:     最多返回条数，默认 1000
    //
    // 返回：符合条件的 LogEntry 数组（时间倒序）
    func fetch(
        serviceId: UUID? = nil,
        runId: UUID? = nil,
        levels: Set<LogLevel>? = nil,
        keyword: String? = nil,
        limit: Int = 1000
    ) -> [LogEntry] {
        (try? db?.read { db in
            var conditions: [String] = []
            var args: [DatabaseValue] = []

            if let sid = serviceId {
                conditions.append("service_id = ?")
                args.append(sid.uuidString.databaseValue)
            }
            if let rid = runId {
                conditions.append("run_id = ?")
                args.append(rid.uuidString.databaseValue)
            }
            if let levels = levels, !levels.isEmpty {
                let placeholders = levels.map { _ in "?" }.joined(separator: ",")
                conditions.append("level IN (\(placeholders))")
                levels.forEach { args.append($0.rawValue.databaseValue) }
            }
            if let kw = keyword, !kw.isEmpty {
                conditions.append("message LIKE ?")
                args.append("%\(kw)%".databaseValue)
            }

            let where_ = conditions.isEmpty ? "" : "WHERE " + conditions.joined(separator: " AND ")
            let sql = "SELECT * FROM log_entries \(where_) ORDER BY timestamp DESC LIMIT \(limit)"
            return try LogEntry.fetchAll(db, sql: sql, arguments: StatementArguments(args))
        }) ?? []
    }

    // deleteOldRuns 删除旧的运行日志，仅保留最近 count 个 run 的日志。
    //
    // 参数：
    //   - count: 保留最近 run 的数量，默认 10
    func deleteOldRuns(keepLast count: Int = 10) {
        try? db?.write { db in
            let sql = """
                DELETE FROM log_entries WHERE run_id NOT IN (
                    SELECT DISTINCT run_id FROM log_entries
                    ORDER BY MIN(timestamp) DESC LIMIT ?
                )
            """
            try db.execute(sql: sql, arguments: [count])
        }
    }

    // lastErrorLog 返回指定服务最后一条 error 或 unknown 级别的日志。
    //
    // 参数：
    //   - serviceId: 服务 ID
    //
    // 返回：最后一条错误日志，若无则为 nil
    func lastErrorLog(for serviceId: UUID) -> LogEntry? {
        fetch(serviceId: serviceId, levels: [.error, .unknown], limit: 1).first
    }

    // MARK: - Private

    private func setupDatabase() {
        guard let appSupport = FileManager.default.urls(
            for: .applicationSupportDirectory, in: .userDomainMask
        ).first else { return }

        let dbDir = appSupport.appendingPathComponent("SuperDev")
        try? FileManager.default.createDirectory(at: dbDir, withIntermediateDirectories: true)
        let dbPath = dbDir.appendingPathComponent("logs.db").path

        db = try? DatabaseQueue(path: dbPath)
        createTableIfNeeded()
    }

    private func createTableIfNeeded() {
        try? db?.write { db in
            try db.create(table: "log_entries", ifNotExists: true) { t in
                t.column("id", .text).primaryKey()
                t.column("timestamp", .datetime).notNull().indexed()
                t.column("service_id", .text).notNull().indexed()
                t.column("service_name", .text).notNull()
                t.column("level", .text).notNull().indexed()
                t.column("message", .text).notNull()
                t.column("normalized_message", .text).notNull()
                t.column("run_id", .text).notNull().indexed()
                t.column("repeat_count", .integer).notNull().defaults(to: 1)
            }
        }
    }
}
