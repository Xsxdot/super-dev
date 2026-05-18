// LaunchJsonImporter.swift
// SuperDev
//
// 职责：
//   - 读取 .vscode/launch.json 中的调试配置
//   - 将 VS Code 调试配置转换为 SuperDev Service 模型
//   - 支持 go/python（program + args）和 node/pnpm（runtimeExecutable + runtimeArgs）两种配置格式
//
// 边界：
//   - 仅负责解析与转换，不负责写入 .superdev/config.yaml（由调用方决定是否保存）
//   - 不修改 rootPath 以外的任何文件
//   - 所有 env 值统一转换为 String

import Foundation

/// LaunchJsonImporter 负责从 `.vscode/launch.json` 中读取 VS Code 调试配置，
/// 并转换为 SuperDev `Service` 模型列表。
///
/// 注意：显式标注 `@MainActor` 以与项目全局 `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor`
/// 保持一致，确保对象在正确的执行上下文中初始化和销毁。
@MainActor
final class LaunchJsonImporter {

    let rootPath: String

    enum ImportError: Error {
        case fileNotFound
        case invalidFormat
    }

    // MARK: - Init

    /// 初始化导入器。
    ///
    /// - Parameter rootPath: 项目根目录路径，导入器将从该目录下的 `.vscode/launch.json` 读取配置。
    init(rootPath: String) {
        self.rootPath = rootPath
    }

    /// 显式声明 nonisolated deinit，避免 Xcode 26 beta 在 XCTest 环境下
    /// `@MainActor` 类销毁时触发 swift_task_deinitOnExecutorImpl 崩溃问题。
    nonisolated deinit {}

    // MARK: - Public

    /// 从 `.vscode/launch.json` 中导入所有调试配置，并转换为 SuperDev Service 列表。
    ///
    /// - Returns: 转换后的 `Service` 数组，顺序与 launch.json 中的 configurations 顺序一致。
    /// - Throws:
    ///   - `ImportError.fileNotFound`：当 `.vscode/launch.json` 不存在时。
    ///   - `ImportError.invalidFormat`：当文件内容无法解析为预期 JSON 结构时。
    func importServices() throws -> [Service] {
        let launchFile = URL(fileURLWithPath: rootPath)
            .appendingPathComponent(".vscode/launch.json")

        guard FileManager.default.fileExists(atPath: launchFile.path) else {
            throw ImportError.fileNotFound
        }

        let data: Data
        do {
            data = try Data(contentsOf: launchFile)
        } catch {
            throw ImportError.fileNotFound
        }

        guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let configs = json["configurations"] as? [[String: Any]] else {
            throw ImportError.invalidFormat
        }

        return configs.compactMap { parseConfiguration($0) }
    }

    // MARK: - Private

    /// 将单个 launch.json 配置项转换为 Service 模型。
    ///
    /// - Parameter config: launch.json 中的单个配置字典。
    /// - Returns: 转换后的 `Service`，若缺少必要字段（name）则返回 nil。
    private func parseConfiguration(_ config: [String: Any]) -> Service? {
        guard let name = config["name"] as? String else { return nil }

        let command = buildCommand(from: config)

        // 替换顺序重要：必须先替换带尾部斜杠的形式，再替换不带斜杠的形式，
        // 否则 "${workspaceFolder}/web" 会被处理为 "./web" → ".web"。
        let cwd = (config["cwd"] as? String ?? "${workspaceFolder}")
            .replacingOccurrences(of: "${workspaceFolder}/", with: "./")
            .replacingOccurrences(of: "${workspaceFolder}", with: ".")

        // 将所有 env 值统一转换为 String，与 ConfigLoader 保持一致。
        let env = (config["env"] as? [String: Any])?.reduce(into: [String: String]()) { result, pair in
            result[pair.key] = "\(pair.value)"
        } ?? [:]

        return Service(
            name: name,
            command: command,
            workingDir: cwd,
            required: false,
            env: env
        )
    }

    /// 根据配置类型构建命令字符串。
    ///
    /// - node/pnpm 类型：使用 `runtimeExecutable` + `runtimeArgs`
    /// - go/python 等类型：使用 `program` + `args`
    /// - 两者都没有时：返回占位命令
    private func buildCommand(from config: [String: Any]) -> String {
        // node/pnpm 类型：runtimeExecutable + runtimeArgs
        if let runtime = config["runtimeExecutable"] as? String {
            let args = (config["runtimeArgs"] as? [String] ?? []).joined(separator: " ")
            return args.isEmpty ? runtime : "\(runtime) \(args)"
        }

        // go/python 类型：program + args
        if let program = config["program"] as? String {
            let args = (config["args"] as? [String] ?? []).joined(separator: " ")
            return args.isEmpty ? program : "\(program) \(args)"
        }

        return "echo 'unknown command'"
    }
}
