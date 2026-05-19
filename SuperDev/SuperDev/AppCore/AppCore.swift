import Foundation
import Combine
import SwiftUI

// Central state manager for the entire app. UI layers share this via @StateObject/@EnvironmentObject.
//
// Responsibilities:
//   - Owns projects list and in-memory log buffer
//   - Coordinates ProcessManager, LogEngine, LogStore
//   - Exposes process control and log query APIs to UI
//
// Boundaries:
//   - @MainActor: all state mutations happen on the main actor
//   - Does not own UI state (selected service, filter state) — that belongs in views
@MainActor
final class AppCore: ObservableObject {
    @Published var projects: [Project] = []
    @Published var logs: [LogEntry] = []
    @Published var currentRunId: UUID
    @Published var availableRuns: [RunSummary] = []
    @Published var historyLogs: [LogEntry] = []
    @Published var viewingRunId: UUID? = nil
    /// Per-project log filter rules (keyed by Project.id).
    @Published private(set) var logRulesByProjectId: [UUID: [LogRule]] = [:]
    /// 用户选择在 Popover 中隐藏的服务 ID 集合（持久化到 UserDefaults）
    @Published var hiddenServiceIds: Set<UUID> = []

    static let logRetentionDaysKey = "superdev.log_retention_days"
    static let defaultRetentionDays = 7

    private let projectStore = ProjectStore()
    private let logStore = LogStore()
    private let logEngine: LogEngine
    private var processManagers: [UUID: ProcessManager] = [:]  // projectId → manager
    private var controlServer: ControlSocketServer?

    private let hiddenServiceIdsKey = "superdev.hidden_service_ids"

    init() {
        let runId = UUID()
        currentRunId = runId
        logEngine = LogEngine(runId: runId)
        hiddenServiceIds = Self.loadHiddenServiceIds()
        killOrphanProcesses()
        loadProjects()
        reloadLogRules()
        performStartupLogMaintenance()
        controlServer = ControlSocketServer(core: self)
        controlServer?.start()
    }

    // MARK: - Log retention & history

    var logRetentionDays: Int {
        get {
            let stored = UserDefaults.standard.integer(forKey: Self.logRetentionDaysKey)
            return stored > 0 ? stored : Self.defaultRetentionDays
        }
        set {
            UserDefaults.standard.set(newValue, forKey: Self.logRetentionDaysKey)
            performStartupLogMaintenance()
        }
    }

    func returnToLiveLogs() {
        viewingRunId = nil
    }

    func loadHistoryRun(_ runId: UUID) {
        viewingRunId = runId
        historyLogs = []
        let store = logStore
        Task.detached {
            let raw = store.fetchLogs(for: runId)
            await MainActor.run { [weak self] in
                guard self?.viewingRunId == runId else { return }
                self?.historyLogs = raw
            }
        }
    }

    func refreshAvailableRuns() {
        let store = logStore
        let currentRun = currentRunId
        Task.detached {
            let runs = store.fetchRuns().filter { $0.runId != currentRun }
            await MainActor.run { [weak self] in
                self?.availableRuns = runs
            }
        }
    }

    private func performStartupLogMaintenance() {
        let days = logRetentionDays
        let store = logStore
        let currentRun = currentRunId
        Task.detached {
            store.deleteOldEntries(olderThan: days)
            let runs = store.fetchRuns().filter { $0.runId != currentRun }
            var previousLogs: [LogEntry] = []
            if let lastRun = runs.first {
                previousLogs = store.fetchLogs(for: lastRun.runId)
            }
            await MainActor.run { [weak self] in
                self?.availableRuns = runs
                self?.historyLogs = previousLogs
            }
        }
    }

    // MARK: - Log rules (per-project config.yaml)

    func reloadLogRules() {
        var byProject: [UUID: [LogRule]] = [:]
        for project in projects {
            let loader = ConfigLoader(rootPath: project.rootPath)
            let config = (try? loader.loadLogRules()) ?? LogRulesConfig()
            byProject[project.id] = config.rules
        }
        logRulesByProjectId = byProject
        if let runId = viewingRunId {
            loadHistoryRun(runId)
        }
    }

    func logRules(for projectId: UUID) -> [LogRule] {
        logRulesByProjectId[projectId] ?? []
    }

    func logRules(for project: Project) -> LogRulesConfig {
        let loader = ConfigLoader(rootPath: project.rootPath)
        return (try? loader.loadLogRules()) ?? LogRulesConfig()
    }

    func saveLogRules(_ config: LogRulesConfig, for project: Project) throws {
        let loader = ConfigLoader(rootPath: project.rootPath)
        try loader.saveLogRules(config)
        reloadLogRules()
    }

    func addLogRule(_ rule: LogRule, to project: Project) throws {
        var config = logRules(for: project)
        config.rules.append(rule)
        try saveLogRules(config, for: project)
    }

    func project(forServiceId serviceId: UUID?) -> Project? {
        guard let serviceId else { return projects.first }
        return projects.first { $0.services.contains { $0.id == serviceId } }
    }

    // MARK: - Hidden Services

    func toggleHidden(_ service: Service) {
        if hiddenServiceIds.contains(service.id) {
            hiddenServiceIds.remove(service.id)
        } else {
            hiddenServiceIds.insert(service.id)
        }
        saveHiddenServiceIds()
    }

    func isHidden(_ service: Service) -> Bool {
        hiddenServiceIds.contains(service.id)
    }

    private func saveHiddenServiceIds() {
        let strings = hiddenServiceIds.map { $0.uuidString }
        UserDefaults.standard.set(strings, forKey: hiddenServiceIdsKey)
    }

    private static func loadHiddenServiceIds() -> Set<UUID> {
        let strings = UserDefaults.standard.stringArray(forKey: "superdev.hidden_service_ids") ?? []
        return Set(strings.compactMap { UUID(uuidString: $0) })
    }

    // MARK: - Project Management

    func addProject(rootPath: String) throws {
        let loader = ConfigLoader(rootPath: rootPath)
        var project = try loader.load()
        project = Project(id: project.id, name: project.name, rootPath: rootPath, services: project.services)
        projects.append(project)
        projectStore.addPath(rootPath)
        reloadLogRules()
    }

    func removeProject(_ project: Project) {
        stopAll(project: project)
        projects.removeAll { $0.id == project.id }
        projectStore.removePath(project.rootPath)
        reloadLogRules()
    }

    func reloadConfig(for project: Project) throws {
        let loader = ConfigLoader(rootPath: project.rootPath)
        let updated = try loader.load()
        if let idx = projects.firstIndex(where: { $0.id == project.id }) {
            projects[idx].name = updated.name
            projects[idx].services = updated.services
        }
        reloadLogRules()
    }

    func importFromLaunchJson(rootPath: String) throws {
        let importer = LaunchJsonImporter(rootPath: rootPath)
        let services = try importer.importServices()
        let project = Project(name: URL(fileURLWithPath: rootPath).lastPathComponent,
                              rootPath: rootPath, services: services)
        let loader = ConfigLoader(rootPath: rootPath)
        try loader.save(project)
        try addProject(rootPath: rootPath)
    }

    // MARK: - Process Control

    func start(_ service: Service, in project: Project) {
        let manager = getOrCreateManager(for: project)
        manager.start(service, projectRootPath: project.rootPath)
    }

    func stop(_ service: Service, in project: Project) {
        processManagers[project.id]?.stop(service.id)
    }

    func startSelected(services: [Service], in project: Project) {
        services.forEach { start($0, in: project) }
    }

    func restart(_ service: Service, in project: Project) {
        let manager = getOrCreateManager(for: project)
        manager.restart(service, projectRootPath: project.rootPath)
    }

    func stopAll(project: Project) {
        processManagers[project.id]?.stopAll()
        if let idx = projects.firstIndex(where: { $0.id == project.id }) {
            for i in projects[idx].services.indices {
                projects[idx].services[i].status = .stopped
            }
        }
    }

    // MARK: - Log Queries

    private var activeLogSource: [LogEntry] {
        viewingRunId != nil ? historyLogs : logs
    }

    func filteredLogs(
        serviceId: UUID? = nil,
        levels: Set<LogLevel>? = nil,
        includeChips: [String] = [],
        excludeChips: [String] = [],
        chipLogic: LogFilter.ChipLogic = .or
    ) -> [LogEntry] {
        let snapshot = logRulesSnapshot()
        var result = activeLogSource.filter { entry in
            let rules = LogFilter.rulesForEntry(entry, snapshot: snapshot)
            return LogFilter.passes(entry, rules: rules)
        }
        if let sid = serviceId {
            if viewingRunId != nil, let name = serviceName(for: sid) {
                result = result.filter { $0.serviceName == name }
            } else {
                result = result.filter { $0.serviceId == sid }
            }
        }
        if let lvls = levels, !lvls.isEmpty { result = result.filter { lvls.contains($0.level) } }
        if !includeChips.isEmpty || !excludeChips.isEmpty {
            result = result.filter {
                LogFilter.passes($0, includeChips: includeChips, excludeChips: excludeChips, logic: chipLogic)
            }
        }
        return result
    }

    func lastErrorLog(for serviceId: UUID) -> LogEntry? {
        logStore.lastErrorLog(for: serviceId)
    }

    // MARK: - Private

    private func logRulesSnapshot() -> LogRulesSnapshot {
        var serviceIdToProjectId: [UUID: UUID] = [:]
        var serviceNameToProjectId: [String: UUID] = [:]
        for project in projects {
            for service in project.services {
                serviceIdToProjectId[service.id] = project.id
                serviceNameToProjectId[service.name] = project.id
            }
        }
        return LogRulesSnapshot(
            serviceIdToProjectId: serviceIdToProjectId,
            serviceNameToProjectId: serviceNameToProjectId,
            rulesByProjectId: logRulesByProjectId
        )
    }

    private func serviceName(for serviceId: UUID) -> String? {
        for project in projects {
            if let service = project.services.first(where: { $0.id == serviceId }) {
                return service.name
            }
        }
        return nil
    }

    private func loadProjects() {
        for path in projectStore.loadPaths() {
            try? addProject(rootPath: path)
        }
    }

    private func getOrCreateManager(for project: Project) -> ProcessManager {
        if let existing = processManagers[project.id] { return existing }
        let manager = ProcessManager(
            onLog: { [weak self] serviceId, serviceName, line in
                guard let self else { return }
                let entry = self.logEngine.parseLine(line, serviceId: serviceId, serviceName: serviceName)
                self.logEngine.ingest(entry, into: &self.logs)
                self.logStore.append(entry)
            },
            onStatusChange: { [weak self] serviceId, status in
                guard let self else { return }
                self.updateServiceStatus(serviceId, status: status, in: project.id)
            },
            onPidReady: { [weak self] serviceId, pid in
                guard let self else { return }
                if let pid {
                    self.recordPid(pid, for: serviceId)
                } else {
                    self.clearPid(for: serviceId)
                }
            }
        )
        processManagers[project.id] = manager
        return manager
    }

    private func updateServiceStatus(_ serviceId: UUID, status: ServiceStatus, in projectId: UUID) {
        guard let pi = projects.firstIndex(where: { $0.id == projectId }),
              let si = projects[pi].services.firstIndex(where: { $0.id == serviceId }) else { return }
        projects[pi].services[si].status = status
    }

    // MARK: - Orphan Process Cleanup

    private var pidFilePath: String {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        return (support?.appendingPathComponent("SuperDev/running_pids.json").path) ?? "/tmp/superdev_pids.json"
    }

    func recordPid(_ pid: Int32, for serviceId: UUID) {
        var pids = loadSavedPids()
        pids[serviceId.uuidString] = pid
        savePids(pids)
    }

    func clearPid(for serviceId: UUID) {
        var pids = loadSavedPids()
        pids.removeValue(forKey: serviceId.uuidString)
        savePids(pids)
    }

    private func killOrphanProcesses() {
        let pids = loadSavedPids()
        for (_, pid) in pids {
            kill(pid, SIGTERM)
        }
        savePids([:])
    }

    private func loadSavedPids() -> [String: Int32] {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: pidFilePath)),
              let decoded = try? JSONDecoder().decode([String: Int32].self, from: data) else {
            return [:]
        }
        return decoded
    }

    private func savePids(_ pids: [String: Int32]) {
        let dir = URL(fileURLWithPath: pidFilePath).deletingLastPathComponent()
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        if let data = try? JSONEncoder().encode(pids) {
            try? data.write(to: URL(fileURLWithPath: pidFilePath))
        }
    }
}
