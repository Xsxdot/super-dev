import XCTest
@testable import SuperDev

@MainActor
final class FilteredLogsTests: XCTestCase {

    private func makeEntry(
        serviceId: UUID,
        serviceName: String,
        message: String = "msg"
    ) -> LogEntry {
        LogEntry(
            serviceId: serviceId,
            serviceName: serviceName,
            level: .info,
            message: message,
            normalizedMessage: message,
            runId: UUID()
        )
    }

    func test_filteredLogs_projectId_live_filtersByProjectServices() {
        let core = AppCore()
        let projectA = UUID()
        let projectB = UUID()
        let svcA = UUID()
        let svcB = UUID()
        let svcOther = UUID()

        core.projects = [
            Project(id: projectA, name: "A", rootPath: "/a", services: [
                Service(id: svcA, name: "api", command: "echo", workingDir: "."),
            ]),
            Project(id: projectB, name: "B", rootPath: "/b", services: [
                Service(id: svcB, name: "web", command: "echo", workingDir: "."),
            ]),
        ]
        core.logs = [
            makeEntry(serviceId: svcA, serviceName: "api"),
            makeEntry(serviceId: svcB, serviceName: "web"),
            makeEntry(serviceId: svcOther, serviceName: "other"),
        ]

        let filtered = core.filteredLogs(projectId: projectA)
        XCTAssertEqual(filtered.count, 1)
        XCTAssertEqual(filtered[0].serviceId, svcA)
    }

    func test_filteredLogs_projectId_history_filtersByServiceName() {
        let core = AppCore()
        let projectId = UUID()
        let svcId = UUID()
        core.projects = [
            Project(id: projectId, name: "P", rootPath: "/p", services: [
                Service(id: svcId, name: "api", command: "echo", workingDir: "."),
            ]),
        ]
        core.viewingRunId = UUID()
        core.historyLogs = [
            makeEntry(serviceId: UUID(), serviceName: "api"),
            makeEntry(serviceId: UUID(), serviceName: "other"),
        ]

        let filtered = core.filteredLogs(projectId: projectId)
        XCTAssertEqual(filtered.count, 1)
        XCTAssertEqual(filtered[0].serviceName, "api")
    }

    func test_filterRunsForService_onlyIncludesMatchingService() {
        let svcA = UUID()
        let svcB = UUID()
        let project = Project(
            id: UUID(),
            name: "P",
            rootPath: "/p",
            services: [
                Service(id: svcA, name: "api", command: "echo", workingDir: "."),
                Service(id: svcB, name: "worker", command: "echo", workingDir: "."),
            ]
        )
        let runs = [
            RunSummary(runId: UUID(), startTime: Date(), logCount: 1, serviceNames: ["api"]),
            RunSummary(runId: UUID(), startTime: Date(), logCount: 1, serviceNames: ["worker"]),
            RunSummary(runId: UUID(), startTime: Date(), logCount: 2, serviceNames: ["api", "worker"]),
        ]
        let apiOnly = AppCore.filterRunsForService(
            runs,
            service: project.services[0],
            project: project,
            allProjects: [project]
        )
        XCTAssertEqual(apiOnly.count, 2)
        XCTAssertTrue(apiOnly.allSatisfy { $0.serviceNames.contains("api") })
        XCTAssertFalse(apiOnly.contains { $0.serviceNames == ["worker"] })
    }

    func test_filterRunsForProject_excludesOtherProjectsExclusiveServices() {
        let projectA = Project(
            id: UUID(),
            name: "A",
            rootPath: "/a",
            services: [
                Service(id: UUID(), name: "api", command: "echo", workingDir: "."),
                Service(id: UUID(), name: "worker", command: "echo", workingDir: "."),
            ]
        )
        let projectB = Project(
            id: UUID(),
            name: "B",
            rootPath: "/b",
            services: [
                Service(id: UUID(), name: "api", command: "echo", workingDir: "."),
                Service(id: UUID(), name: "redis", command: "echo", workingDir: "."),
            ]
        )
        let runs = [
            RunSummary(runId: UUID(), startTime: Date(), logCount: 1, serviceNames: ["api"]),
            RunSummary(runId: UUID(), startTime: Date(), logCount: 2, serviceNames: ["api", "redis"]),
            RunSummary(runId: UUID(), startTime: Date(), logCount: 1, serviceNames: ["worker"]),
        ]
        let forA = AppCore.filterRunsForProject(runs, project: projectA, allProjects: [projectA, projectB])
        XCTAssertEqual(forA.count, 2)
        XCTAssertTrue(forA.contains { $0.serviceNames == ["api"] })
        XCTAssertTrue(forA.contains { $0.serviceNames == ["worker"] })
        XCTAssertFalse(forA.contains { $0.serviceNames.contains("redis") })

        let forB = AppCore.filterRunsForProject(runs, project: projectB, allProjects: [projectA, projectB])
        XCTAssertEqual(forB.count, 2)
        XCTAssertFalse(forB.contains { $0.serviceNames == ["worker"] })
    }

    func test_filteredLogs_serviceId_takesPrecedenceOverProjectId() {
        let core = AppCore()
        let projectId = UUID()
        let svcA = UUID()
        let svcB = UUID()
        core.projects = [
            Project(id: projectId, name: "P", rootPath: "/p", services: [
                Service(id: svcA, name: "api", command: "echo", workingDir: "."),
                Service(id: svcB, name: "web", command: "echo", workingDir: "."),
            ]),
        ]
        core.logs = [
            makeEntry(serviceId: svcA, serviceName: "api"),
            makeEntry(serviceId: svcB, serviceName: "web"),
        ]

        let filtered = core.filteredLogs(serviceId: svcA, projectId: projectId)
        XCTAssertEqual(filtered.count, 1)
        XCTAssertEqual(filtered[0].serviceId, svcA)
    }
}
