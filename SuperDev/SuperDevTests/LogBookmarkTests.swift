import XCTest
@testable import SuperDev

final class LogBookmarkTests: XCTestCase {

    private func makeEntry(serviceId: UUID = UUID(), message: String = "msg") -> LogEntry {
        LogEntry(
            id: UUID(), timestamp: Date(), serviceId: serviceId,
            serviceName: "svc", level: .info, message: message,
            normalizedMessage: message, runId: UUID(), repeatCount: 1
        )
    }

    func test_bookmark_initialState_noTimestamps() {
        let bm = LogBookmark(panelId: UUID(), serviceId: nil)
        XCTAssertNil(bm.startTime)
        XCTAssertNil(bm.endTime)
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    func test_isActive_trueWhenStartedNoEnd() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        XCTAssertTrue(bm.isActive)
    }

    func test_isActive_falseWhenCompleted() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        bm.endTime = Date()
        XCTAssertFalse(bm.isActive)
    }

    func test_isActive_falseWhenNoStart() {
        let bm = LogBookmark(panelId: UUID(), serviceId: nil)
        XCTAssertFalse(bm.isActive)
    }

    func test_isCompleted_trueWhenBothTimestampsSet() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        bm.endTime = Date()
        XCTAssertTrue(bm.isCompleted)
    }

    func test_appendLog_whenActive_addsToLockedLogs() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        let entry = makeEntry()
        bm.appendLog(entry)
        XCTAssertEqual(bm.lockedLogs.count, 1)
        XCTAssertEqual(bm.lockedLogs[0].id, entry.id)
    }

    func test_appendLog_whenCompleted_doesNotAdd() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        bm.endTime = Date()
        bm.appendLog(makeEntry())
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    func test_appendLog_whenNoStart_doesNotAdd() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.appendLog(makeEntry())
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    func test_formattedText_formatsCorrectly() {
        var bm = LogBookmark(panelId: UUID(), serviceId: nil)
        bm.startTime = Date()
        let ts = Date(timeIntervalSince1970: 0)
        let entry = LogEntry(
            id: UUID(), timestamp: ts, serviceId: UUID(),
            serviceName: "api", level: .error, message: "boom",
            normalizedMessage: "boom", runId: UUID(), repeatCount: 1
        )
        bm.appendLog(entry)
        let text = bm.formattedText
        XCTAssertTrue(text.contains("[api]"))
        XCTAssertTrue(text.contains("ERROR"))
        XCTAssertTrue(text.contains("boom"))
    }
}
