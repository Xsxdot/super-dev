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
    /// Bumps on every live-log ingest (including dedup fold updates). UI uses this instead of `logs.count`.
    @Published private(set) var logSourceRevision: UInt64 = 0
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
    /// 同步组内每个面板对应的服务 ID（与 syncGroupPanelIds 同步维护）
    private var syncGroupServiceIds: [UUID: UUID?] = [:]
    private var filteredLogsCacheRevision: UInt64 = 0
    private var filteredLogsCache: [FilteredLogsQuery: [LogEntry]] = [:]

    static let logRetentionDaysKey = "superdev.log_retention_days"
    static let defaultRetentionDays = 7
    /// Cap in-memory live logs so multi-panel filtering stays bounded during long runs.
    static let maxInMemoryLogEntries = 8_000

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
        bumpLogSourceRevision()
    }

    func loadHistoryRun(_ runId: UUID) {
        viewingRunId = runId
        historyLogs = []
        bumpLogSourceRevision()
        let store = logStore
        Task.detached {
            let raw = store.fetchLogs(for: runId)
            await MainActor.run { [weak self] in
                guard self?.viewingRunId == runId else { return }
                self?.historyLogs = raw
                self?.bumpLogSourceRevision()
            }
        }
    }

    /// Loads history runs for a single service. Returns empty when `serviceId` is nil.
    func fetchHistoryRuns(serviceId: UUID?) async -> [RunSummary] {
        guard let serviceId,
              let project = project(forServiceId: serviceId),
              let service = project.services.first(where: { $0.id == serviceId }) else { return [] }
        let filter = Self.runFilter(forServiceId: serviceId, in: project)
        let store = logStore
        let currentRun = currentRunId
        let allProjects = projects
        let raw = await Task.detached {
            store.fetchRuns(serviceIds: filter.serviceIds, serviceNames: filter.serviceNames)
        }.value
        return Self.filterRunsForService(
            raw.filter { $0.runId != currentRun },
            service: service,
            project: project,
            allProjects: allProjects
        )
    }

    /// Refreshes global `availableRuns` for the given service only. No-op when `serviceId` is nil.
    func refreshAvailableRuns(serviceId: UUID?) {
        guard let serviceId else { return }
        Task {
            availableRuns = await fetchHistoryRuns(serviceId: serviceId)
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
                self?.availableRuns = []
                self?.historyLogs = previousLogs
                self?.bumpLogSourceRevision()
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
        bumpLogSourceRevision()
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

    func project(id projectId: UUID) -> Project? {
        projects.first { $0.id == projectId }
    }

    func project(forServiceId serviceId: UUID?) -> Project? {
        guard let serviceId else { return nil }
        return projects.first { $0.services.contains { $0.id == serviceId } }
    }

    private struct RunFilter: Sendable {
        let serviceIds: [UUID]?
        let serviceNames: [String]?
    }

    private static func runFilter(forProjectId projectId: UUID?, projects: [Project]) -> RunFilter {
        guard let projectId,
              let project = projects.first(where: { $0.id == projectId }) else {
            return RunFilter(serviceIds: nil, serviceNames: nil)
        }
        let ids = project.services.map(\.id)
        let names = project.services.map(\.name)
        return RunFilter(
            serviceIds: ids.isEmpty ? nil : ids,
            serviceNames: names.isEmpty ? nil : names
        )
    }

    private static func runFilter(forServiceId serviceId: UUID, in project: Project) -> RunFilter {
        let name = project.services.first(where: { $0.id == serviceId })?.name
        return RunFilter(
            serviceIds: [serviceId],
            serviceNames: name.map { [$0] }
        )
    }

    /// Keeps runs that include this service and no other project's exclusive services.
    static func filterRunsForService(
        _ runs: [RunSummary],
        service: Service,
        project: Project,
        allProjects: [Project]
    ) -> [RunSummary] {
        let singleServiceProject = Project(
            id: project.id,
            name: project.name,
            rootPath: project.rootPath,
            services: [service]
        )
        return filterRunsForProject(runs, project: singleServiceProject, allProjects: allProjects)
    }

    /// Keeps runs that touch this project's services but not another project's exclusive services.
    static func filterRunsForProject(
        _ runs: [RunSummary],
        project: Project,
        allProjects: [Project]
    ) -> [RunSummary] {
        let projectNames = Set(project.services.map(\.name))
        guard !projectNames.isEmpty else { return [] }
        let otherNames = Set(
            allProjects
                .filter { $0.id != project.id }
                .flatMap { $0.services.map(\.name) }
        )
        /// Service names that belong only to other projects (shared names like "api" are excluded).
        let otherExclusive = otherNames.subtracting(projectNames)
        return runs.filter { run in
            let runNames = Set(run.serviceNames)
            guard !runNames.isDisjoint(with: projectNames) else { return false }
            return runNames.isDisjoint(with: otherExclusive)
        }
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
        projectId: UUID? = nil,
        levels: Set<LogLevel>? = nil,
        includeChips: [String] = [],
        excludeChips: [String] = [],
        chipLogic: LogFilter.ChipLogic = .or
    ) -> [LogEntry] {
        let query = FilteredLogsQuery(
            serviceId: serviceId,
            projectId: projectId,
            levels: levels,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic,
            viewingRunId: viewingRunId
        )
        if filteredLogsCacheRevision == logSourceRevision, let cached = filteredLogsCache[query] {
            return cached
        }
        if filteredLogsCacheRevision != logSourceRevision {
            filteredLogsCache.removeAll(keepingCapacity: true)
            filteredLogsCacheRevision = logSourceRevision
        }
        let result = computeFilteredLogs(
            serviceId: serviceId,
            projectId: projectId,
            levels: levels,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic
        )
        filteredLogsCache[query] = result
        return result
    }

    private func computeFilteredLogs(
        serviceId: UUID?,
        projectId: UUID?,
        levels: Set<LogLevel>?,
        includeChips: [String],
        excludeChips: [String],
        chipLogic: LogFilter.ChipLogic
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
        } else if let pid = projectId, let project = project(id: pid) {
            let serviceIds = Set(project.services.map(\.id))
            let serviceNames = Set(project.services.map(\.name))
            if viewingRunId != nil {
                result = result.filter { serviceNames.contains($0.serviceName) }
            } else {
                result = result.filter { serviceIds.contains($0.serviceId) }
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

    private func bumpLogSourceRevision() {
        logSourceRevision += 1
    }

    private func ingestLiveLog(_ entry: LogEntry) {
        logEngine.ingest(entry, into: &logs)
        if logs.count > Self.maxInMemoryLogEntries {
            logs.removeFirst(logs.count - Self.maxInMemoryLogEntries)
        }
        bumpLogSourceRevision()
        logStore.append(entry)
        for panelId in bookmarks.keys where bookmarks[panelId]?.isActive == true {
            bookmarks[panelId]?.appendLog(entry)
        }
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
                self.ingestLiveLog(entry)
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

    // toggleSyncGroup 将 panelId 加入或移出同步组，同时记录其绑定的服务 ID。
    //
    // 参数：
    //   - panelId:   目标面板 ID
    //   - serviceId: 面板当前绑定的服务 ID（可为 nil）
    func toggleSyncGroup(panelId: UUID, serviceId: UUID?) {
        if syncGroupPanelIds.contains(panelId) {
            syncGroupPanelIds.remove(panelId)
            syncGroupServiceIds.removeValue(forKey: panelId)
        } else {
            syncGroupPanelIds.insert(panelId)
            syncGroupServiceIds[panelId] = serviceId
        }
    }

    // startSyncBookmark 对所有 syncGroupPanelIds 中的面板同时开始书签录制。
    // 使用 toggleSyncGroup 时已记录的 syncGroupServiceIds 映射。
    func startSyncBookmark() {
        guard !syncGroupIsRecording else { return }
        for panelId in syncGroupPanelIds {
            let serviceId = syncGroupServiceIds[panelId].flatMap { $0 }
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
            // 每个面板的书签绑定到单一服务（serviceId 非 nil），所有 lockedLogs 来自同一服务
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

/// Cache key for `AppCore.filteredLogs` — identical queries across panels share one filtered array.
struct FilteredLogsQuery: Hashable {
    let serviceId: UUID?
    let projectId: UUID?
    let levels: Set<LogLevel>?
    let includeChips: [String]
    let excludeChips: [String]
    let chipLogic: LogFilter.ChipLogic
    let viewingRunId: UUID?
}
