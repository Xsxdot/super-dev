// ControlSocketServer 监听 Unix Domain Socket，接收来自 superdev-mcp 的控制指令。
//
// 职责：
//   - 在 ~/Library/Application Support/SuperDev/control.sock 监听连接
//   - 每个连接读取一行 JSON 请求，回写一行 JSON 响应后关闭连接
//   - 将控制指令（restart/stop/list）调度到主线程执行
//
// 边界：
//   - 不持有业务状态，通过 weak AppCore 引用执行操作
//   - 每次连接独立处理，不维持长连接
import Foundation

@MainActor
final class ControlSocketServer {
    private weak var core: AppCore?
    // nonisolated(unsafe) 允许在 DispatchSource 回调（非主线程）中读取 serverFD，
    // 写入只发生在 start()/stop() 中（均在主线程），读取发生在 acceptConnection()（后台线程），
    // 生命周期上不会出现竞争：start() 完成后值固定，stop() 调用前回调已取消。
    nonisolated(unsafe) private var serverFD: Int32 = -1
    private var dispatchSource: DispatchSourceRead?

    static var socketPath: String {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        return (support?.appendingPathComponent("SuperDev/control.sock").path) ?? "/tmp/superdev-control.sock"
    }

    init(core: AppCore) {
        self.core = core
    }

    func start() {
        let path = ControlSocketServer.socketPath
        // 确保目录存在
        let dir = URL(fileURLWithPath: path).deletingLastPathComponent()
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        unlink(path)

        serverFD = socket(AF_UNIX, SOCK_STREAM, 0)
        guard serverFD >= 0 else { return }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &addr.sun_path) { ptr in
            path.withCString { src in
                _ = strlcpy(ptr.baseAddress!.assumingMemoryBound(to: CChar.self), src, ptr.count)
            }
        }

        let bindResult = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(serverFD, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else { close(serverFD); serverFD = -1; return }
        guard listen(serverFD, 5) == 0 else { close(serverFD); serverFD = -1; return }

        let source = DispatchSource.makeReadSource(fileDescriptor: serverFD, queue: .global(qos: .utility))
        source.setEventHandler { [weak self] in self?.acceptConnection() }
        source.resume()
        dispatchSource = source
    }

    func stop() {
        dispatchSource?.cancel()
        if serverFD >= 0 { close(serverFD); serverFD = -1 }
        unlink(ControlSocketServer.socketPath)
    }

    // MARK: - Connection handling

    // nonisolated 以便在 DispatchSource 回调（后台线程）中调用。
    // serverFD 标记为 nonisolated(unsafe)，此处只读，生命周期安全。
    nonisolated private func acceptConnection() {
        let fd = serverFD
        guard fd >= 0 else { return }
        let clientFD = accept(fd, nil, nil)
        guard clientFD >= 0 else { return }
        DispatchQueue.global(qos: .utility).async { [weak self] in
            self?.handleConnection(clientFD)
        }
    }

    nonisolated private func handleConnection(_ fd: Int32) {
        defer { close(fd) }

        var buffer = [UInt8](repeating: 0, count: 4096)
        var received = ""
        while received.last != "\n" {
            let n = read(fd, &buffer, buffer.count)
            guard n > 0 else { break }
            received += String(bytes: buffer.prefix(n), encoding: .utf8) ?? ""
        }
        let line = received.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !line.isEmpty,
              let data = line.data(using: .utf8),
              let request = try? JSONDecoder().decode(ControlRequest.self, from: data) else {
            writeResponse(fd, ControlResponse(ok: false, error: "invalid request"))
            return
        }

        let semaphore = DispatchSemaphore(value: 0)
        // nonisolated 闭包中用 var 捕获再赋值会触发 Swift 并发警告，
        // 改用 ManagedAtomic 或简单的类包装来规避。这里用 class 包装避免捕获 var。
        final class Box<T> { var value: T; init(_ v: T) { value = v } }
        let box = Box(ControlResponse(ok: false, error: "unknown error"))
        DispatchQueue.main.async { [weak self] in
            guard let self else {
                box.value = ControlResponse(ok: false, error: "server not ready")
                semaphore.signal()
                return
            }
            box.value = self.handle(request)
            semaphore.signal()
        }
        semaphore.wait()
        writeResponse(fd, box.value)
    }

    nonisolated private func writeResponse(_ fd: Int32, _ response: ControlResponse) {
        guard let data = try? JSONEncoder().encode(response),
              let line = String(data: data, encoding: .utf8) else { return }
        let out = line + "\n"
        out.withCString { ptr in
            _ = Foundation.write(fd, ptr, strlen(ptr))
        }
    }

    // MARK: - Protocol types

    // Sendable 使这些值类型可安全跨 actor 边界传递。
    struct ControlRequest: Decodable, Sendable {
        let action: String
        let target: String?
    }

    struct ServiceInfo: Encodable, Sendable {
        let project: String
        let service: String
        let status: String
    }

    struct ControlResponse: Encodable, Sendable {
        let ok: Bool
        var message: String?
        var error: String?
        var data: [ServiceInfo]?
    }

    // MARK: - Action dispatch (runs on MainActor)

    @MainActor
    private func handle(_ request: ControlRequest) -> ControlResponse {
        switch request.action {
        case "list_services":
            return handleListServices()
        case "restart_service":
            return handleServiceAction(target: request.target, action: .restart)
        case "stop_service":
            return handleServiceAction(target: request.target, action: .stop)
        default:
            return ControlResponse(ok: false, error: "unknown action: \(request.action)")
        }
    }

    @MainActor
    private func handleListServices() -> ControlResponse {
        guard let core else { return ControlResponse(ok: false, error: "core unavailable") }
        let infos = core.projects.flatMap { project in
            project.services.map { service in
                ServiceInfo(
                    project: project.name,
                    service: service.name,
                    status: service.status.rawStringValue
                )
            }
        }
        return ControlResponse(ok: true, data: infos)
    }

    private enum ServiceAction { case restart, stop }

    @MainActor
    private func handleServiceAction(target: String?, action: ServiceAction) -> ControlResponse {
        guard let core else { return ControlResponse(ok: false, error: "core unavailable") }
        guard let target else { return ControlResponse(ok: false, error: "target required") }

        let parts = target.split(separator: "/", maxSplits: 1).map(String.init)
        guard parts.count == 2 else {
            return ControlResponse(ok: false, error: "target must be 'project/service', got: \(target)")
        }
        let projectName = parts[0]
        let serviceName = parts[1]

        guard let project = core.projects.first(where: { $0.name == projectName }),
              let service = project.services.first(where: { $0.name == serviceName }) else {
            return ControlResponse(ok: false, error: "service not found: \(target)")
        }

        switch action {
        case .restart:
            core.restart(service, in: project)
            return ControlResponse(ok: true, message: "restarted \(target)")
        case .stop:
            core.stop(service, in: project)
            return ControlResponse(ok: true, message: "stopped \(target)")
        }
    }
}
