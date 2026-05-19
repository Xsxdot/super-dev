import XCTest
@testable import SuperDev

final class LogFilterTests: XCTestCase {

    private func entry(message: String) -> LogEntry {
        entry(message: message, serviceId: UUID())
    }

    func test_passes_noRules_returnsTrue() {
        XCTAssertTrue(LogFilter.passes(entry(message: "hello"), rules: []))
    }

    func test_exclude_rule_hidesMatchingEntry() {
        let rule = LogRule(name: "heartbeat", type: .exclude, keywords: ["heartbeat"], logic: .or)
        XCTAssertFalse(LogFilter.passes(entry(message: "heartbeat ok"), rules: [rule]))
        XCTAssertTrue(LogFilter.passes(entry(message: "server started"), rules: [rule]))
    }

    func test_include_rule_requiresMatch() {
        let rule = LogRule(name: "errors only", type: .include, keywords: ["ERROR"], logic: .or)
        XCTAssertTrue(LogFilter.passes(entry(message: "ERROR disk full"), rules: [rule]))
        XCTAssertFalse(LogFilter.passes(entry(message: "server started"), rules: [rule]))
    }

    func test_exclude_before_include() {
        let exclude = LogRule(name: "noise", type: .exclude, keywords: ["ping"], logic: .or)
        let include = LogRule(name: "errors", type: .include, keywords: ["ERROR"], logic: .or)
        XCTAssertFalse(LogFilter.passes(entry(message: "ERROR ping"), rules: [exclude, include]))
    }

    func test_and_logic_requiresAllKeywords() {
        let rule = LogRule(name: "and", type: .include, keywords: ["foo", "bar"], logic: .and)
        XCTAssertTrue(LogFilter.passes(entry(message: "foo and bar"), rules: [rule]))
        XCTAssertFalse(LogFilter.passes(entry(message: "foo only"), rules: [rule]))
    }

    func test_keywordTokenizer_splitsOnSeparators() {
        XCTAssertEqual(KeywordTokenizer.split("a, b\nc\td;e"), ["a", "b", "c", "d", "e"])
    }

    func test_makeRulesFromChips_includeOnly() {
        let chips = [
            FilterChip(keyword: "error", type: .include),
            FilterChip(keyword: "warn", type: .include),
        ]
        let rules = LogChipRuleBuilder.makeRulesFromChips(name: "Errors", chips: chips, logic: .and)
        XCTAssertEqual(rules.count, 1)
        XCTAssertEqual(rules[0].name, "Errors")
        XCTAssertEqual(rules[0].type, .include)
        XCTAssertEqual(rules[0].keywords, ["error", "warn"])
        XCTAssertEqual(rules[0].logic, .and)
    }

    func test_makeRulesFromChips_excludeOnly() {
        let chips = [FilterChip(keyword: "heartbeat", type: .exclude)]
        let rules = LogChipRuleBuilder.makeRulesFromChips(name: "", chips: chips, logic: .or)
        XCTAssertEqual(rules.count, 1)
        XCTAssertEqual(rules[0].name, "快捷过滤")
        XCTAssertEqual(rules[0].type, .exclude)
    }

    func test_makeRulesFromChips_mixedTypes() {
        let chips = [
            FilterChip(keyword: "error", type: .include),
            FilterChip(keyword: "ping", type: .exclude),
        ]
        let rules = LogChipRuleBuilder.makeRulesFromChips(name: "Filter", chips: chips, logic: .or)
        XCTAssertEqual(rules.count, 2)
        XCTAssertEqual(rules[0].name, "Filter (包含)")
        XCTAssertEqual(rules[0].keywords, ["error"])
        XCTAssertEqual(rules[1].name, "Filter (排除)")
        XCTAssertEqual(rules[1].keywords, ["ping"])
    }

    func test_isDuplicate_matchesTypeKeywordsAndLogic() {
        let existing = LogRule(name: "x", type: .exclude, keywords: ["a", "b"], logic: .and)
        let same = LogRule(name: "y", type: .exclude, keywords: ["b", "a"], logic: .and)
        let different = LogRule(name: "z", type: .exclude, keywords: ["a", "b"], logic: .or)
        XCTAssertTrue(LogChipRuleBuilder.isDuplicate(same, in: [existing]))
        XCTAssertFalse(LogChipRuleBuilder.isDuplicate(different, in: [existing]))
    }

    func test_chip_exclude() {
        XCTAssertFalse(
            LogFilter.passes(
                entry(message: "heartbeat"),
                includeChips: [],
                excludeChips: ["heartbeat"],
                logic: .or
            )
        )
    }

    func test_filterEntries_appliesRulesPerProject() {
        let projectA = UUID()
        let projectB = UUID()
        let serviceA = UUID()
        let serviceB = UUID()
        let excludeNoise = LogRule(name: "noise", type: .exclude, keywords: ["heartbeat"], logic: .or)
        let snapshot = LogRulesSnapshot(
            serviceIdToProjectId: [serviceA: projectA, serviceB: projectB],
            serviceNameToProjectId: ["svcA": projectA, "svcB": projectB],
            rulesByProjectId: [projectA: [excludeNoise], projectB: []]
        )
        let entries = [
            entry(message: "heartbeat from A", serviceId: serviceA),
            entry(message: "heartbeat from B", serviceId: serviceB)
        ]
        let result = LogFilter.filterEntries(entries, snapshot: snapshot)
        XCTAssertEqual(result.count, 1)
        XCTAssertTrue(result[0].message.contains("from B"))
    }

    private func entry(message: String, serviceId: UUID) -> LogEntry {
        LogEntry(
            serviceId: serviceId,
            serviceName: "svc",
            level: .info,
            message: message,
            normalizedMessage: message,
            runId: UUID()
        )
    }

    func test_rulesForEntry_fallsBackToServiceNameWhenIdIsStale() {
        let projectId = UUID()
        let staleServiceId = UUID()
        let snapshot = LogRulesSnapshot(
            serviceIdToProjectId: [:],
            serviceNameToProjectId: ["api": projectId],
            rulesByProjectId: [
                projectId: [LogRule(name: "noise", type: .exclude, keywords: ["heartbeat"], logic: .or)]
            ]
        )
        let entry = LogEntry(
            serviceId: staleServiceId,
            serviceName: "api",
            level: .info,
            message: "heartbeat ok",
            normalizedMessage: "heartbeat ok",
            runId: UUID()
        )
        XCTAssertFalse(LogFilter.passes(entry, rules: LogFilter.rulesForEntry(entry, snapshot: snapshot)))
    }

    func test_apply_filtersBatch() {
        let rule = LogRule(name: "x", type: .exclude, keywords: ["noise"], logic: .or)
        let entries = [
            entry(message: "noise line"),
            entry(message: "good line")
        ]
        let result = LogFilter.apply(rules: [rule], to: entries)
        XCTAssertEqual(result.count, 1)
        XCTAssertEqual(result[0].message, "good line")
    }
}

@MainActor
final class ConfigLoaderLogRulesTests: XCTestCase {

    var tempDir: URL!

    override func setUp() {
        super.setUp()
        tempDir = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
        try! FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: tempDir)
        super.tearDown()
    }

    func test_loadLogRules_parsesRulesSection() throws {
        let yaml = """
        name: Test
        services: []
        logRules:
          rules:
            - id: "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
              name: 心跳包
              type: exclude
              keywords: ["heartbeat", "ping"]
              logic: OR
              enabled: true
        """
        let configDir = tempDir.appendingPathComponent(".superdev")
        try FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
        try yaml.write(to: configDir.appendingPathComponent("config.yaml"), atomically: true, encoding: .utf8)

        let loader = ConfigLoader(rootPath: tempDir.path)
        let config = try loader.loadLogRules()

        XCTAssertEqual(config.rules.count, 1)
        XCTAssertEqual(config.rules[0].name, "心跳包")
        XCTAssertEqual(config.rules[0].keywords, ["heartbeat", "ping"])
    }

    func test_saveLogRules_roundTrips() throws {
        let loader = ConfigLoader(rootPath: tempDir.path)
        let rule = LogRule(
            id: UUID(uuidString: "A1B2C3D4-E5F6-7890-ABCD-EF1234567890")!,
            name: "test",
            type: .exclude,
            keywords: ["foo"],
            logic: .or
        )
        try loader.saveLogRules(LogRulesConfig(rules: [rule]))
        let loaded = try loader.loadLogRules()
        XCTAssertEqual(loaded.rules.count, 1)
        XCTAssertEqual(loaded.rules[0].name, "test")
    }

    func test_saveLogRules_emptyRulesAfterDelete() throws {
        let loader = ConfigLoader(rootPath: tempDir.path)
        let rule = LogRule(
            id: UUID(uuidString: "A1B2C3D4-E5F6-7890-ABCD-EF1234567890")!,
            name: "to-remove",
            type: .exclude,
            keywords: ["noise"],
            logic: .or
        )
        try loader.saveLogRules(LogRulesConfig(rules: [rule]))
        try loader.saveLogRules(LogRulesConfig(rules: []))
        let loaded = try loader.loadLogRules()
        XCTAssertTrue(loaded.rules.isEmpty)
    }

    func test_removeOneOfTwoRules_roundTrips() throws {
        let loader = ConfigLoader(rootPath: tempDir.path)
        let keepId = UUID(uuidString: "11111111-1111-1111-1111-111111111111")!
        let removeId = UUID(uuidString: "22222222-2222-2222-2222-222222222222")!
        let keep = LogRule(id: keepId, name: "keep", type: .include, keywords: ["ok"], logic: .or)
        let remove = LogRule(id: removeId, name: "remove", type: .exclude, keywords: ["noise"], logic: .or)
        try loader.saveLogRules(LogRulesConfig(rules: [keep, remove]))

        var config = try loader.loadLogRules()
        config.rules.removeAll { $0.id == removeId }
        try loader.saveLogRules(config)

        let loaded = try loader.loadLogRules()
        XCTAssertEqual(loaded.rules.count, 1)
        XCTAssertEqual(loaded.rules[0].id, keepId)
        XCTAssertEqual(loaded.rules[0].name, "keep")
    }

    func test_saveProject_preservesLogRulesWhenProjectFieldsChange() throws {
        let yaml = """
        name: Test
        services:
          - name: api
            command: echo hi
            working_dir: .
        logRules:
          retentionDays: 14
          rules:
            - id: "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
              name: keep-me
              type: exclude
              keywords: ["noise"]
              logic: OR
              enabled: true
        """
        let configDir = tempDir.appendingPathComponent(".superdev")
        try FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
        try yaml.write(to: configDir.appendingPathComponent("config.yaml"), atomically: true, encoding: .utf8)

        let loader = ConfigLoader(rootPath: tempDir.path)
        var project = try loader.load()
        project.name = "Renamed"
        try loader.save(project)

        let content = try String(contentsOf: configDir.appendingPathComponent("config.yaml"), encoding: .utf8)
        XCTAssertTrue(content.contains("keep-me"))
        XCTAssertTrue(content.contains("noise"))
        let rules = try loader.loadLogRules()
        XCTAssertEqual(rules.rules.first?.name, "keep-me")
    }
}
