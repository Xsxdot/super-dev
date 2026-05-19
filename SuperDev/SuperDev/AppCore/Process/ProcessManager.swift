import Foundation

// Manages all ProcessRunner instances for a single Project.
//
// Responsibilities:
//   - Start and stop ProcessRunner instances keyed by service UUID
//   - Dispatch log lines and status changes back to the main thread
//   - Resolve working directory paths relative to the project root
//
// Boundaries:
//   - @MainActor because it is owned by AppCore and its callbacks update @Published state
//   - Does not own Service model state; delegates status updates via callbacks
//   - ProcessRunner callbacks arrive on a background thread; this class dispatches to main
@MainActor
final class ProcessManager {
    private var runners: [UUID: ProcessRunner] = [:]
    private let onLog: (UUID, String, String) -> Void   // (serviceId, serviceName, rawLine)
    private let onStatusChange: (UUID, ServiceStatus) -> Void
    private let onPidReady: (UUID, Int32?) -> Void      // (serviceId, pid?) — nil means stopped

    init(
        onLog: @escaping (UUID, String, String) -> Void,
        onStatusChange: @escaping (UUID, ServiceStatus) -> Void,
        onPidReady: @escaping (UUID, Int32?) -> Void = { _, _ in }
    ) {
        self.onLog = onLog
        self.onStatusChange = onStatusChange
        self.onPidReady = onPidReady
    }

    // Starts a ProcessRunner for the given service if one is not already running.
    // Immediately emits .starting, then emits .running or .failed after a brief delay.
    func start(_ service: Service, projectRootPath: String) {
        guard runners[service.id] == nil else { return }

        let workingDir = resolveWorkingDir(service.workingDir, rootPath: projectRootPath)
        let envFilePath = service.envFile.map {
            URL(fileURLWithPath: projectRootPath).appendingPathComponent($0).path
        }

        onStatusChange(service.id, .starting)

        let serviceId = service.id
        let serviceName = service.name

        let runner = ProcessRunner(
            command: service.command,
            workingDir: workingDir,
            env: service.env,
            envFile: envFilePath
        ) { [weak self] line in
            DispatchQueue.main.async {
                self?.onLog(serviceId, serviceName, line)
            }
        }

        do {
            try runner.start()
            runners[service.id] = runner
            // Brief delay to let the process establish before declaring it running.
            // If the process exits immediately (e.g., bad command), isRunning will be false
            // and we report .failed — a process that exits right after start is considered failed.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                guard let self else { return }
                if runner.isRunning {
                    self.onStatusChange(serviceId, .running)
                    self.onPidReady(serviceId, runner.pid)
                } else {
                    self.runners[serviceId] = nil
                    self.onStatusChange(serviceId, .failed)
                }
            }
        } catch {
            // runner.start() threw — do not store the runner; emit failure immediately
            onStatusChange(service.id, .failed)
        }
    }

    // Stops the runner for the given service ID and emits .stopped.
    func stop(_ serviceId: UUID) {
        runners[serviceId]?.stop()
        runners[serviceId] = nil
        onStatusChange(serviceId, .stopped)
        onPidReady(serviceId, nil)
    }

    // Stops all running services without emitting individual status changes.
    // Used on project close / app quit.
    func stopAll() {
        runners.values.forEach { $0.stop() }
        runners.removeAll()
    }

    // Restarts the runner for the given service: stops it first, then starts again.
    func restart(_ service: Service, projectRootPath: String) {
        runners[service.id]?.stop()
        runners[service.id] = nil
        onStatusChange(service.id, .stopped)
        start(service, projectRootPath: projectRootPath)
    }

    // Returns whether the runner for the given service is currently running.
    func isRunning(_ serviceId: UUID) -> Bool {
        runners[serviceId]?.isRunning ?? false
    }

    // MARK: - Private

    // Resolves a service working directory to an absolute path.
    //
    // Rules:
    //   - "." → project root
    //   - Absolute path → returned as-is
    //   - Relative path → joined with project root
    private func resolveWorkingDir(_ dir: String, rootPath: String) -> String {
        if dir == "." { return rootPath }
        if dir.hasPrefix("/") { return dir }
        return URL(fileURLWithPath: rootPath).appendingPathComponent(dir).path
    }
}
