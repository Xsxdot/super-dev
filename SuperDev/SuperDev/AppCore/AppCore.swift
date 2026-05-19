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
    /// 用户选择在 Popover 中隐藏的服务 ID 集合（持久化到 UserDefaults）
    @Published var hiddenServiceIds: Set<UUID> = []

    private let projectStore = ProjectStore()
    private let logStore = LogStore()
    private let logEngine: LogEngine
    private var processManagers: [UUID: ProcessManager] = [:]  // projectId → manager

    private let hiddenServiceIdsKey = "superdev.hidden_service_ids"

    init() {
        let runId = UUID()
        currentRunId = runId
        logEngine = LogEngine(runId: runId)
        hiddenServiceIds = Self.loadHiddenServiceIds()
        killOrphanProcesses()
        loadProjects()
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
    }

    func removeProject(_ project: Project) {
        stopAll(project: project)
        projects.removeAll { $0.id == project.id }
        projectStore.removePath(project.rootPath)
    }

    func reloadConfig(for project: Project) throws {
        let loader = ConfigLoader(rootPath: project.rootPath)
        let updated = try loader.load()
        if let idx = projects.firstIndex(where: { $0.id == project.id }) {
            projects[idx].name = updated.name
            projects[idx].services = updated.services
        }
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
        // ProcessManager.stop 内部会通过 onStatusChange 回调更新状态，无需在此重复
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

    func filteredLogs(
        serviceId: UUID? = nil,
        levels: Set<LogLevel>? = nil,
        keyword: String? = nil
    ) -> [LogEntry] {
        var result = logs
        if let sid = serviceId { result = result.filter { $0.serviceId == sid } }
        if let lvls = levels, !lvls.isEmpty { result = result.filter { lvls.contains($0.level) } }
        if let kw = keyword, !kw.isEmpty {
            result = result.filter { $0.message.localizedCaseInsensitiveContains(kw) }
        }
        return result
    }

    // TODO: LogStore.fetch 是同步阻塞调用，LogStore 文档要求在后台线程调用。
    // 当前在 @MainActor 上执行，日志量小时无感知，后续可改为 async/GRDB asyncRead。
    func lastErrorLog(for serviceId: UUID) -> LogEntry? {
        logStore.lastErrorLog(for: serviceId)
    }

    // MARK: - Private

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

    // PID 文件路径，保存上次运行时各服务的 PID
    private var pidFilePath: String {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        return (support?.appendingPathComponent("SuperDev/running_pids.json").path) ?? "/tmp/superdev_pids.json"
    }

    // 记录当前运行中的 PID（在进程启动后调用）
    func recordPid(_ pid: Int32, for serviceId: UUID) {
        var pids = loadSavedPids()
        pids[serviceId.uuidString] = pid
        savePids(pids)
    }

    // 清除某服务的 PID 记录
    func clearPid(for serviceId: UUID) {
        var pids = loadSavedPids()
        pids.removeValue(forKey: serviceId.uuidString)
        savePids(pids)
    }

    // 启动时 kill 上次遗留的孤儿进程
    private func killOrphanProcesses() {
        let pids = loadSavedPids()
        for (_, pid) in pids {
            kill(pid, SIGTERM)
        }
        // 清空 PID 文件
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
