import Foundation

struct Project: Identifiable, Codable, Equatable {
    let id: UUID
    var name: String
    var rootPath: String
    var services: [Service]

    init(id: UUID = UUID(), name: String, rootPath: String, services: [Service] = []) {
        self.id = id
        self.name = name
        self.rootPath = rootPath
        self.services = services
    }

    var overallStatus: ProjectStatus {
        if services.isEmpty { return .stopped }
        if services.contains(where: { $0.status == .failed }) { return .failed }
        if services.contains(where: { $0.status == .starting }) { return .starting }
        if services.contains(where: { $0.status == .running }) { return .running }
        return .stopped
    }
}

enum ProjectStatus {
    case stopped, starting, running, failed
}

struct Service: Identifiable, Codable, Equatable {
    let id: UUID
    var name: String
    var command: String
    var workingDir: String
    var required: Bool
    var envFile: String?
    var env: [String: String]
    var status: ServiceStatus

    init(
        id: UUID = UUID(),
        name: String,
        command: String,
        workingDir: String = ".",
        required: Bool = false,
        envFile: String? = nil,
        env: [String: String] = [:]
    ) {
        self.id = id
        self.name = name
        self.command = command
        self.workingDir = workingDir
        self.required = required
        self.envFile = envFile
        self.env = env
        self.status = .stopped
    }
}

enum ServiceStatus: Codable, Equatable {
    case stopped
    case starting
    case running
    case failed

    var isActive: Bool {
        self == .running || self == .starting
    }
}
