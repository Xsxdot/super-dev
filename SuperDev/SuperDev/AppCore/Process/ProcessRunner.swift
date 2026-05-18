import Foundation

// Wraps a single Foundation.Process, capturing stdout+stderr line-by-line.
// Callers receive each line via the onLine callback (called on a background thread).
final class ProcessRunner {
    private let command: String
    private let workingDir: String
    private let env: [String: String]
    private let envFile: String?
    private let onLine: (String) -> Void

    private var process: Process?
    private var outputPipe: Pipe?
    private var errorPipe: Pipe?

    var isRunning: Bool {
        process?.isRunning ?? false
    }

    var pid: Int32? {
        guard let p = process, p.isRunning else { return nil }
        return p.processIdentifier
    }

    init(
        command: String,
        workingDir: String,
        env: [String: String],
        envFile: String? = nil,
        onLine: @escaping (String) -> Void
    ) {
        self.command = command
        self.workingDir = workingDir
        self.env = env
        self.envFile = envFile
        self.onLine = onLine
    }

    func start() throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", command]
        process.currentDirectoryURL = URL(fileURLWithPath: workingDir)
        process.environment = buildEnvironment()

        let outPipe = Pipe()
        let errPipe = Pipe()
        process.standardOutput = outPipe
        process.standardError = errPipe

        outPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            self?.handleData(handle.availableData)
        }
        errPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            self?.handleData(handle.availableData)
        }

        self.outputPipe = outPipe
        self.errorPipe = errPipe
        self.process = process

        try process.run()

        process.terminationHandler = { [weak self] _ in
            self?.outputPipe?.fileHandleForReading.readabilityHandler = nil
            self?.errorPipe?.fileHandleForReading.readabilityHandler = nil
        }
    }

    func stop() {
        process?.terminate()
        outputPipe?.fileHandleForReading.readabilityHandler = nil
        errorPipe?.fileHandleForReading.readabilityHandler = nil
    }

    // MARK: - Private

    private func handleData(_ data: Data) {
        guard !data.isEmpty,
              let text = String(data: data, encoding: .utf8) else { return }
        text.components(separatedBy: "\n")
            .filter { !$0.isEmpty }
            .forEach { onLine($0) }
    }

    private func buildEnvironment() -> [String: String] {
        var result = ProcessInfo.processInfo.environment
        // App 启动时 PATH 不含开发工具路径，手动补全常见位置
        let devPaths = ["/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/go/bin"]
        let currentPath = result["PATH"] ?? "/usr/bin:/bin"
        let extraPaths = devPaths.filter { !currentPath.contains($0) }.joined(separator: ":")
        if !extraPaths.isEmpty {
            result["PATH"] = extraPaths + ":" + currentPath
        }
        if let envFile = envFile {
            loadEnvFile(envFile).forEach { result[$0] = $1 }
        }
        env.forEach { result[$0] = $1 }
        return result
    }

    private func loadEnvFile(_ path: String) -> [String: String] {
        guard let content = try? String(contentsOfFile: path, encoding: .utf8) else { return [:] }
        var result: [String: String] = [:]
        content.components(separatedBy: "\n").forEach { line in
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            guard !trimmed.isEmpty, !trimmed.hasPrefix("#") else { return }
            let parts = trimmed.split(separator: "=", maxSplits: 1)
            guard parts.count == 2 else { return }
            let key = String(parts[0]).trimmingCharacters(in: .whitespaces)
            let value = String(parts[1]).trimmingCharacters(in: .init(charactersIn: "\"' "))
            result[key] = value
        }
        return result
    }
}
