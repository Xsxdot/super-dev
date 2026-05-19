import XCTest
@testable import SuperDev

@MainActor
final class PopoverSelectionTests: XCTestCase {

    private var defaults: UserDefaults!
    private var suiteName: String!

    override func setUp() {
        super.setUp()
        suiteName = "superdev.test.\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)!
        defaults.removePersistentDomain(forName: suiteName)
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suiteName)
        defaults = nil
        suiteName = nil
        super.tearDown()
    }

    func test_saveAndLoad_roundTrip() {
        let map: [String: Set<String>] = ["/tmp/proj": ["api", "web"]]

        AppCore.savePopoverSelectedServiceNames(map, to: defaults)
        let loaded = AppCore.loadPopoverSelectedServiceNames(from: defaults)

        XCTAssertEqual(loaded["/tmp/proj"], ["api", "web"])
    }

    func test_normalize_includesRequired_andExcludesInvalid() {
        let required = Service(name: "api", command: "echo", workingDir: ".", required: true)
        let optional = Service(name: "web", command: "echo", workingDir: ".", required: false)
        let project = Project(
            name: "Test",
            rootPath: "/tmp",
            services: [required, optional]
        )

        let normalized = AppCore.normalizeSelectedServiceNames(
            ["web", "stale"],
            project: project,
            hiddenServiceIds: []
        )

        XCTAssertTrue(normalized.contains("api"))
        XCTAssertTrue(normalized.contains("web"))
        XCTAssertFalse(normalized.contains("stale"))
        XCTAssertEqual(normalized.count, 2)
    }

    func test_normalize_excludesHiddenServices() {
        let visible = Service(name: "api", command: "echo", workingDir: ".", required: false)
        let hiddenSvc = Service(name: "secret", command: "echo", workingDir: ".", required: false)
        let project = Project(
            name: "Test",
            rootPath: "/tmp",
            services: [visible, hiddenSvc]
        )

        let normalized = AppCore.normalizeSelectedServiceNames(
            ["api", "secret"],
            project: project,
            hiddenServiceIds: [hiddenSvc.id]
        )

        XCTAssertEqual(normalized, ["api"])
    }

    func test_persistsAcrossServiceIdRegeneration() {
        let rootPath = "/tmp/persist-test"
        let required = Service(name: "api", command: "echo", workingDir: ".", required: true)
        let optional = Service(name: "web", command: "echo", workingDir: ".", required: false)
        let projectBefore = Project(name: "Test", rootPath: rootPath, services: [required, optional])

        AppCore.savePopoverSelectedServiceNames(
            [rootPath: ["api", "web"]],
            to: defaults
        )

        let reloadedRequired = Service(name: "api", command: "echo", workingDir: ".", required: true)
        let reloadedOptional = Service(name: "web", command: "echo", workingDir: ".", required: false)
        let projectAfter = Project(
            id: UUID(),
            name: "Test",
            rootPath: rootPath,
            services: [reloadedRequired, reloadedOptional]
        )

        let loaded = AppCore.loadPopoverSelectedServiceNames(from: defaults)
        let normalized = AppCore.normalizeSelectedServiceNames(
            loaded[rootPath] ?? [],
            project: projectAfter,
            hiddenServiceIds: []
        )
        let selected = AppCore.serviceIds(for: normalized, in: projectAfter)
        XCTAssertTrue(selected.contains(reloadedOptional.id))
        XCTAssertTrue(selected.contains(reloadedRequired.id))
        XCTAssertFalse(selected.contains(optional.id))
    }

    func test_setSelectedServiceIds_updatesResolvedSelection() {
        let required = Service(name: "api", command: "echo", workingDir: ".", required: true)
        let optional = Service(name: "web", command: "echo", workingDir: ".", required: false)
        let project = Project(name: "Test", rootPath: "/tmp/save-test", services: [required, optional])

        let core = AppCore()
        core.projects = [project]
        core.setSelectedServiceIds([optional.id], for: project)

        let selected = core.defaultSelectedServiceIds(for: project)
        XCTAssertTrue(selected.contains(optional.id))
        XCTAssertTrue(selected.contains(required.id))
    }
}
