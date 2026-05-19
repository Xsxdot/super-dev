import XCTest
import GRDB
@testable import SuperDev

final class LogStoreTests: XCTestCase {

    func test_parseTimestamp_acceptsSQLiteText() {
        let value = "2026-05-19 06:16:50.014".databaseValue
        XCTAssertNotNil(LogStore.parseTimestamp(value))
    }

    func test_fetchRuns_doesNotDropExistingDatabaseRuns() {
        let store = LogStore()
        let runs = store.fetchRuns()
        let dbURL = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)
            .first!
            .appendingPathComponent("SuperDev/logs.db")
        guard FileManager.default.fileExists(atPath: dbURL.path),
              let queue = try? DatabaseQueue(path: dbURL.path) else { return }
        let distinctCount = try? queue.read { db in
            try Int.fetchOne(db, sql: "SELECT COUNT(DISTINCT run_id) FROM log_entries")
        }
        guard let distinctCount, distinctCount > 0 else { return }
        XCTAssertFalse(runs.isEmpty, "fetchRuns should parse runs present in logs.db")
        XCTAssertGreaterThan(runs[0].logCount, 0)
    }
}
