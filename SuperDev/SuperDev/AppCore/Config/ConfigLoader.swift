// Package config 提供项目配置文件的读写功能。
//
// 职责：
//   - 读取 {projectRoot}/.superdev/config.yaml 文件，解析为 Project 模型
//   - 将 Project 模型序列化并写入 config.yaml 文件
//   - 处理 YAML snake_case 字段（working_dir）到 Swift camelCase（workingDir）的映射
//
// 边界：
//   - 不管理项目列表，不处理多项目逻辑
//   - 不持久化 Service.status（始终以 .stopped 加载）
//   - 所有 I/O 通过 FileManager 和 Yams 完成

import Foundation
import Yams

/// ConfigLoader 负责读取和写入单个项目的 `.superdev/config.yaml` 配置文件。
///
/// 注意：显式标注 `@MainActor` 以与项目全局 `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor`
/// 保持一致，确保对象在正确的执行上下文中初始化和销毁。
@MainActor
final class ConfigLoader {

    // MARK: - Types

    /// ConfigLoader 可能抛出的错误类型
    enum ConfigError: Error {
        /// 配置文件不存在
        case fileNotFound
        /// YAML 解析失败，附带具体原因
        case parseError(String)
    }

    // MARK: - Properties

    /// 项目根目录的绝对路径
    let rootPath: String

    /// .superdev 目录 URL
    private var configDir: URL {
        URL(fileURLWithPath: rootPath).appendingPathComponent(".superdev")
    }

    /// config.yaml 文件 URL
    private var configFile: URL {
        configDir.appendingPathComponent("config.yaml")
    }

    // MARK: - Init

    /// 初始化 ConfigLoader。
    ///
    /// 参数：
    ///   - rootPath: 项目根目录的绝对路径，config.yaml 位于 `{rootPath}/.superdev/config.yaml`
    init(rootPath: String) {
        self.rootPath = rootPath
    }

    /// 显式声明 nonisolated deinit，避免 Xcode 26 beta 在 XCTest 环境下
    /// `@MainActor` 类销毁时触发 swift_task_deinitOnExecutorImpl 崩溃问题。
    nonisolated deinit {}

    // MARK: - Public

    /// 从磁盘加载项目配置。
    ///
    /// 返回：
    ///   - 解析完成的 Project 实例，其中所有 Service.status 均为 .stopped
    ///
    /// 注意：
    ///   - 若配置文件不存在，抛出 ConfigError.fileNotFound
    ///   - 若 YAML 格式不合法或缺少必要字段，抛出 ConfigError.parseError
    func load() throws -> Project {
        guard FileManager.default.fileExists(atPath: configFile.path) else {
            throw ConfigError.fileNotFound
        }
        let content = try String(contentsOf: configFile, encoding: .utf8)
        let raw = try Yams.load(yaml: content)
        return try parseProject(from: raw)
    }

    /// 将项目配置序列化并写入磁盘。
    ///
    /// 参数：
    ///   - project: 要保存的 Project 实例
    ///
    /// 注意：
    ///   - 若 .superdev 目录不存在，会自动创建
    ///   - Service.status 不会被写入 YAML
    ///   - workingDir 以 snake_case（working_dir）写入 YAML
    func save(_ project: Project) throws {
        try FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
        let dict = projectToDict(project)
        let yaml = try Yams.dump(object: dict)
        try yaml.write(to: configFile, atomically: true, encoding: .utf8)
    }

    // MARK: - Private Parsing

    /// 将 Yams 解析结果转换为 Project 实例。
    private func parseProject(from raw: Any?) throws -> Project {
        guard let dict = raw as? [String: Any] else {
            throw ConfigError.parseError("Root must be a mapping")
        }
        let name = dict["name"] as? String ?? "Unnamed"
        let rawServices = dict["services"] as? [[String: Any]] ?? []
        let services = try rawServices.map { try parseService(from: $0) }
        return Project(name: name, rootPath: rootPath, services: services)
    }

    /// 将单个 service 字典转换为 Service 实例。
    ///
    /// 注意：
    ///   - YAML 中使用 snake_case 的 `working_dir`，映射到 Swift 的 `workingDir`
    ///   - `status` 字段始终为 .stopped，不从 YAML 读取
    private func parseService(from dict: [String: Any]) throws -> Service {
        guard let name = dict["name"] as? String,
              let command = dict["command"] as? String else {
            throw ConfigError.parseError("Service missing required field 'name' or 'command'")
        }
        // YAML 使用 snake_case working_dir，映射到 Swift camelCase workingDir
        let workingDir = dict["working_dir"] as? String ?? "."
        let required = dict["required"] as? Bool ?? false
        let envFile = dict["env_file"] as? String
        // env 的值可能是任意 scalar 类型，统一转为 String
        let env = (dict["env"] as? [String: Any])?.compactMapValues { $0 as? String } ?? [:]
        return Service(
            name: name,
            command: command,
            workingDir: workingDir,
            required: required,
            envFile: envFile,
            env: env
        )
    }

    // MARK: - Private Serialization

    /// 将 Project 转换为可由 Yams 序列化的字典。
    private func projectToDict(_ project: Project) -> [String: Any] {
        var dict: [String: Any] = ["name": project.name]
        dict["services"] = project.services.map { serviceToDict($0) }
        return dict
    }

    /// 将 Service 转换为可由 Yams 序列化的字典。
    ///
    /// 注意：
    ///   - Swift camelCase workingDir 以 snake_case working_dir 写入 YAML
    ///   - status 字段不写入 YAML
    private func serviceToDict(_ service: Service) -> [String: Any] {
        var dict: [String: Any] = [
            "name": service.name,
            "command": service.command,
            "working_dir": service.workingDir,
            "required": service.required
        ]
        if let envFile = service.envFile {
            dict["env_file"] = envFile
        }
        if !service.env.isEmpty {
            dict["env"] = service.env
        }
        return dict
    }
}
