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
    /// Popover 待启动勾选：key 为项目 rootPath，value 为服务名（config 重载后 UUID 会变，名称稳定）
    @Published private(set) var popoverSelectedServiceNamesByRootPath: [String: Set<String>] = [:]
    /// 每个面板的日志书签（keyed by panelId）
    @Published var bookmarks: [UUID: LogBookmark] = [:]
    /// 已勾选加入同步组的 panelId 集合
    @Published var syncGroupPanelIds: Set<UUID> = []
    /// 同步组是否正在录制中
    @Published var syncGroupIsRecording: Bool = false

    static let logRetentionDaysKey = "superdev.log_retention_days"
    static let defaultRetentionDays = 7

    private let projectStore = ProjectStore()
    private let logStore = LogStore()
    private let logEngine: LogEngine
    private var processManagers: [UUID: ProcessManager] = [:]  // projectId → manager
    private var controlServer: ControlSocketServer?

    private let hiddenServiceIdsKey = "superdev.hidden_service_ids"
    private static let popoverSelectedServiceNamesKey = "superdev.popover_selected_service_names"
    private static let legacyPopoverSelectedServiceIdsKey = "superdev.popover_selected_service_ids"

    init() {
        let runId = UUID()
        currentRunId = runId
        logEngine = LogEngine(runId: runId)
        hiddenServiceIds = Self.loadHiddenServiceIds()
        popoverSelectedServiceNamesByRootPath = Self.loadPopoverSelectedServiceNames()
        killOrphanProcesses()
        loadProjects()
        prunePopoverSelections()
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

    func removeLogRule(id: UUID, from project: Project) throws {
        var config = logRules(for: project)
        config.rules.removeAll { $0.id == id }
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

    // MARK: - Popover Service Selection

    func setSelectedServiceIds(_ ids: Set<UUID>, for project: Project) {
        let names = Self.serviceNames(for: ids, in: project)
        let normalized = Self.normalizeSelectedServiceNames(
            names,
            project: project,
            hiddenServiceIds: hiddenServiceIds
        )
        var stored = popoverSelectedServiceNamesByRootPath
        stored[project.rootPath] = normalized
        popoverSelectedServiceNamesByRootPath = stored
        savePopoverSelectedServiceNames()
    }

    func defaultSelectedServiceIds(for project: Project) -> Set<UUID> {
        let persistedNames = popoverSelectedServiceNamesByRootPath[project.rootPath] ?? []
        if !persistedNames.isEmpty {
            let normalized = Self.normalizeSelectedServiceNames(
                persistedNames,
                project: project,
                hiddenServiceIds: hiddenServiceIds
            )
            return Self.serviceIds(for: normalized, in: project)
        }
        return Set(project.services.filter(\.required).map(\.id))
    }

    private func savePopoverSelectedServiceNames() {
        Self.savePopoverSelectedServiceNames(popoverSelectedServiceNamesByRootPath)
    }

    static func loadPopoverSelectedServiceNames(
        from defaults: UserDefaults = .standard
    ) -> [String: Set<String>] {
        if let data = defaults.data(forKey: popoverSelectedServiceNamesKey),
           let raw = try? JSONDecoder().decode([String: [String]].self, from: data) {
            return raw.mapValues { Set($0) }
        }
        return [:]
    }

    static func savePopoverSelectedServiceNames(
        _ map: [String: Set<String>],
        to defaults: UserDefaults = .standard
    ) {
        let raw = map.mapValues { Array($0).sorted() }
        if let data = try? JSONEncoder().encode(raw) {
            defaults.set(data, forKey: popoverSelectedServiceNamesKey)
            defaults.removeObject(forKey: legacyPopoverSelectedServiceIdsKey)
        }
    }

    static func normalizeSelectedServiceNames(
        _ names: Set<String>,
        project: Project,
        hiddenServiceIds: Set<UUID>
    ) -> Set<String> {
        let validNames = Set(project.services.map(\.name))
        let hiddenNames = Set(
            project.services.filter { hiddenServiceIds.contains($0.id) }.map(\.name)
        )
        let requiredNames = Set(project.services.filter(\.required).map(\.name))
        return names.intersection(validNames).subtracting(hiddenNames).union(requiredNames)
    }

    static func serviceNames(for ids: Set<UUID>, in project: Project) -> Set<String> {
        Set(project.services.filter { ids.contains($0.id) }.map(\.name))
    }

    static func serviceIds(for names: Set<String>, in project: Project) -> Set<UUID> {
        Set(project.services.filter { names.contains($0.name) }.map(\.id))
    }

    private func prunePopoverSelections() {
        let validPaths = Set(projects.map(\.rootPath))
        var updated = popoverSelectedServiceNamesByRootPath.filter { validPaths.contains($0.key) }
        for project in projects {
            guard let names = updated[project.rootPath] else { continue }
            updated[project.rootPath] = Self.normalizeSelectedServiceNames(
                names,
                project: project,
                hiddenServiceIds: hiddenServiceIds
            )
        }
        popoverSelectedServiceNamesByRootPath = updated
        savePopoverSelectedServiceNames()
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
        popoverSelectedServiceNamesByRootPath.removeValue(forKey: project.rootPath)
        savePopoverSelectedServiceNames()
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
        prunePopoverSelections()
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
                // 把新日志追加到所有活跃书签
                for panelId in self.bookmarks.keys where self.bookmarks[panelId]?.isActive == true {
                    self.bookmarks[panelId]?.appendLog(entry)
                }
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

    // MARK: - Log Bookmarks

    func startBookmark(panelId: UUID, serviceId: UUID?) {
        var bm = LogBookmark(panelId: panelId, serviceId: serviceId)
        bm.startTime = Date()
        bookmarks[panelId] = bm
    }

    func endBookmark(panelId: UUID) {
        bookmarks[panelId]?.endTime = Date()
    }

    func clearBookmark(panelId: UUID) {
        bookmarks.removeValue(forKey: panelId)
    }

    // MARK: - Sync Bookmark

    // toggleSyncGroup 将 panelId 加入或移出同步组。
    //
    // 参数：
    //   - panelId: 目标面板 ID
    func toggleSyncGroup(panelId: UUID) {
        if syncGroupPanelIds.contains(panelId) {
            syncGroupPanelIds.remove(panelId)
        } else {
            syncGroupPanelIds.insert(panelId)
        }
    }

    // startSyncBookmark 对所有 syncGroupPanelIds 中的面板同时开始书签录制。
    //
    // 参数：
    //   - serviceIdByPanelId: 每个面板对应的服务 ID（可为 nil）
    func startSyncBookmark(serviceIdByPanelId: [UUID: UUID?]) {
        guard !syncGroupIsRecording else { return }
        for panelId in syncGroupPanelIds {
            let serviceId = serviceIdByPanelId[panelId] ?? nil
            startBookmark(panelId: panelId, serviceId: serviceId)
        }
        syncGroupIsRecording = true
    }

    // endSyncBookmark 对所有 syncGroupPanelIds 中的面板同时结束书签录制。
    func endSyncBookmark() {
        for panelId in syncGroupPanelIds {
            endBookmark(panelId: panelId)
        }
        syncGroupIsRecording = false
    }

    // syncBookmarkFormattedText 返回同步组内所有已完成书签的合并文本。
    //
    // 格式：按 serviceName 字母序分块，每块以 "=== <serviceName> ===" 为标题。
    // serviceId 为 nil 的面板跳过。
    //
    // 返回：合并后的纯文本字符串
    func syncBookmarkFormattedText() -> String {
        var blocksByService: [(name: String, text: String)] = []
        for panelId in syncGroupPanelIds {
            guard let bm = bookmarks[panelId],
                  bm.isCompleted,
                  bm.serviceId != nil,
                  !bm.lockedLogs.isEmpty else { continue }
            let serviceName = bm.lockedLogs.first?.serviceName ?? "unknown"
            blocksByService.append((name: serviceName, text: bm.formattedText))
        }
        guard !blocksByService.isEmpty else { return "" }
        return blocksByService
            .sorted { $0.name < $1.name }
            .map { "=== \($0.name) ===\n\($0.text)" }
            .joined(separator: "\n\n")
    }
}
