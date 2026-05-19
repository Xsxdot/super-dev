import XCTest
@testable import SuperDev

@MainActor
final class SyncBookmarkTests: XCTestCase {

    func test_toggleSyncGroup_addsPanel() async {
        let core = AppCore()
        let panelId = UUID()
        core.toggleSyncGroup(panelId: panelId)
        XCTAssertTrue(core.syncGroupPanelIds.contains(panelId))
    }

    func test_toggleSyncGroup_removesPanelIfAlreadyIn() async {
        let core = AppCore()
        let panelId = UUID()
        core.toggleSyncGroup(panelId: panelId)
        core.toggleSyncGroup(panelId: panelId)
        XCTAssertFalse(core.syncGroupPanelIds.contains(panelId))
    }

    func test_startSyncBookmark_setsRecordingAndCreatesBookmarks() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])
        XCTAssertTrue(core.syncGroupIsRecording)
        XCTAssertNotNil(core.bookmarks[p1])
        XCTAssertNotNil(core.bookmarks[p2])
        XCTAssertTrue(core.bookmarks[p1]!.isActive)
        XCTAssertTrue(core.bookmarks[p2]!.isActive)
    }

    func test_endSyncBookmark_completesAllBookmarksAndClearsRecording() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])
        core.endSyncBookmark()
        XCTAssertFalse(core.syncGroupIsRecording)
        XCTAssertTrue(core.bookmarks[p1]!.isCompleted)
        XCTAssertTrue(core.bookmarks[p2]!.isCompleted)
    }

    func test_syncBookmarkFormattedText_groupsByServiceNameAlphabetically() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])

        let ts = Date(timeIntervalSince1970: 3600)
        let entry1 = LogEntry(id: UUID(), timestamp: ts, serviceId: s1,
                              serviceName: "zebra", level: .info, message: "z-msg",
                              normalizedMessage: "z-msg", runId: UUID(), repeatCount: 1)
        let entry2 = LogEntry(id: UUID(), timestamp: ts, serviceId: s2,
                              serviceName: "alpha", level: .warn, message: "a-msg",
                              normalizedMessage: "a-msg", runId: UUID(), repeatCount: 1)
        core.bookmarks[p1]?.appendLog(entry1)
        core.bookmarks[p2]?.appendLog(entry2)
        core.endSyncBookmark()

        let text = core.syncBookmarkFormattedText()
        let alphaRange = text.range(of: "=== alpha")
        let zebraRange = text.range(of: "=== zebra")
        XCTAssertNotNil(alphaRange)
        XCTAssertNotNil(zebraRange)
        XCTAssertLessThan(alphaRange!.lowerBound, zebraRange!.lowerBound)
        XCTAssertTrue(text.contains("a-msg"))
        XCTAssertTrue(text.contains("z-msg"))
    }

    func test_syncBookmarkFormattedText_skipsPanelsWithNilServiceId() async {
        let core = AppCore()
        let p1 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.startSyncBookmark(serviceIdByPanelId: [p1: nil])
        core.endSyncBookmark()
        let text = core.syncBookmarkFormattedText()
        XCTAssertTrue(text.isEmpty)
    }
}
