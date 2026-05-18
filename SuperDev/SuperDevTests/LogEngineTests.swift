// SuperDevTests/LogEngineTests.swift
import XCTest
@testable import SuperDev

@MainActor
final class LogEngineTests: XCTestCase {

    func test_parse_detectsErrorLevel() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("2024-01-01 10:00:00 ERROR database connection failed",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .error)
    }

    func test_parse_detectsWarnLevel() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("[WARN] slow query detected",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .warn)
    }

    func test_parse_defaultsToInfo() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("server started on port 8080",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .info)
    }

    func test_dedup_foldsDuplicates() {
        let engine = LogEngine(runId: UUID())
        let sid = UUID()
        let e1 = engine.parseLine("ERROR disk full", serviceId: sid, serviceName: "api")
        let e2 = engine.parseLine("ERROR disk full", serviceId: sid, serviceName: "api")

        XCTAssertEqual(e1.normalizedMessage, e2.normalizedMessage)

        var entries: [LogEntry] = []
        engine.ingest(e1, into: &entries)
        engine.ingest(e2, into: &entries)

        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries[0].repeatCount, 2)
    }

    func test_dedup_doesNotFoldDifferentServices() {
        let engine = LogEngine(runId: UUID())
        let e1 = engine.parseLine("ERROR disk full", serviceId: UUID(), serviceName: "svc1")
        let e2 = engine.parseLine("ERROR disk full", serviceId: UUID(), serviceName: "svc2")

        var entries: [LogEntry] = []
        engine.ingest(e1, into: &entries)
        engine.ingest(e2, into: &entries)

        XCTAssertEqual(entries.count, 2)
    }

    func test_normalize_stripsTimestamps() {
        let engine = LogEngine(runId: UUID())
        let n1 = engine.normalize("10:23:01 ERROR disk full")
        let n2 = engine.normalize("10:23:59 ERROR disk full")
        XCTAssertEqual(n1, n2)
    }

    nonisolated deinit {}
}
