# SuperDev Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建 SuperDev macOS 菜单栏 App 第一阶段：进程管理 + 日志聚合 + 配置系统 + SwiftUI 界面。

**Architecture:** AppCore 层负责进程管理和日志引擎，SwiftUI 层分为 MenuBar/Popover 和主窗口两部分，两者通过 `@ObservableObject` 共享同一 AppCore 实例。配置从 `.superdev/config.yaml` 读写，日志持久化到 SQLite（GRDB.swift）。

**Tech Stack:** Swift 5.9+, SwiftUI, macOS 14.0+, Foundation.Process, GRDB.swift, Yams, XCTest

---

## 文件结构

```
SuperDev/
├── SuperDev.xcodeproj/          # Xcode 项目（Task 1 创建）
├── SuperDev/
│   ├── SuperDevApp.swift        # App 入口，NSStatusItem 设置
│   ├── AppCore/
│   │   ├── AppCore.swift        # 核心状态管理，@MainActor ObservableObject
│   │   ├── Models/
│   │   │   ├── Project.swift    # Project / Service / ServiceStatus 模型
│   │   │   └── LogEntry.swift   # LogEntry / LogLevel 模型
│   │   ├── Config/
│   │   │   ├── ConfigLoader.swift      # 读写 .superdev/config.yaml
│   │   │   ├── LaunchJsonImporter.swift # 导入 .vscode/launch.json
│   │   │   └── ProjectStore.swift      # 持久化项目列表（UserDefaults）
│   │   ├── Process/
│   │   │   ├── ProcessManager.swift    # 启停进程，管理 Process 实例
│   │   │   └── ProcessRunner.swift     # 单个进程封装，stdout/stderr pipe
│   │   └── Log/
│   │       ├── LogEngine.swift         # 日志接收、去重、过滤
│   │       └── LogStore.swift          # SQLite 持久化（GRDB）
│   ├── UI/
│   │   ├── MenuBar/
│   │   │   ├── MenuBarManager.swift    # NSStatusItem 生命周期
│   │   │   └── PopoverView.swift       # Popover 两层视图
│   │   ├── MainWindow/
│   │   │   ├── MainWindowView.swift    # 主窗口容器
│   │   │   ├── SidebarView.swift       # 左侧进程树
│   │   │   └── LogPanelView.swift      # 右侧日志面板
│   │   └── Settings/
│   │       ├── SettingsView.swift      # 设置窗口
│   │       └── AddProjectView.swift    # 添加项目 Sheet
│   └── Resources/
│       └── Assets.xcassets/            # 菜单栏图标
└── SuperDevTests/
    ├── ConfigLoaderTests.swift
    ├── LaunchJsonImporterTests.swift
    ├── LogEngineTests.swift
    └── ProcessRunnerTests.swift
```

---

## Task 1: 创建 Xcode 项目和依赖

**Files:**
- Create: `SuperDev.xcodeproj/`
- Create: `Package.swift` (SPM 依赖声明，仅用于参考，实际依赖在 Xcode 中添加)
- Create: `SuperDev/SuperDevApp.swift`

- [ ] **Step 1: 用 Xcode 创建项目**

打开 Xcode → File → New → Project → macOS → App
- Product Name: `SuperDev`
- Bundle Identifier: `com.superdev.app`
- Interface: SwiftUI
- Language: Swift
- 取消勾选 "Include Tests"（手动添加 Test target）
- 保存到 `/Users/xushixin/workspace/super-debug/`

- [ ] **Step 2: 添加 Swift Package 依赖**

Xcode → SuperDev target → Package Dependencies → +
依次添加：
- `https://github.com/groue/GRDB.swift` → Up to Next Major: 6.0.0
- `https://github.com/jpsim/Yams` → Up to Next Major: 5.0.0

- [ ] **Step 3: 添加 SuperDevTests target**

Xcode → File → New → Target → macOS → Unit Testing Bundle
- Product Name: `SuperDevTests`
- Target to be Tested: `SuperDev`

- [ ] **Step 4: 配置 App 为仅菜单栏（无 Dock 图标）**

修改 `Info.plist`（在 Xcode 中编辑 target Info）：
- 添加 key: `Application is agent (UIElement)` = `YES`
- 或在 `Info.plist` 源码中添加：
```xml
<key>LSUIElement</key>
<true/>
```

- [ ] **Step 5: 删除默认的 ContentView.swift**

Xcode 中删除自动生成的 `ContentView.swift`（Move to Trash）

- [ ] **Step 6: 创建目录结构**

在 Xcode 中创建 Groups（对应文件系统目录）：
`AppCore/Models`, `AppCore/Config`, `AppCore/Process`, `AppCore/Log`, `UI/MenuBar`, `UI/MainWindow`, `UI/Settings`

- [ ] **Step 7: 验证项目可编译**

Xcode → Product → Build（⌘B）
Expected: Build Succeeded，无警告无报错

- [ ] **Step 8: Commit**

```bash
cd /Users/xushixin/workspace/super-debug
git init
git add -A
git commit -m "feat: initialize SuperDev Xcode project with GRDB and Yams"
```

---

## Task 2: 数据模型

**Files:**
- Create: `SuperDev/AppCore/Models/Project.swift`
- Create: `SuperDev/AppCore/Models/LogEntry.swift`
- Test: 无（纯数据结构，在后续 Task 中通过集成测试覆盖）

- [ ] **Step 1: 创建 Project.swift**

```swift
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

    // pid 不持久化，运行时状态
    var isActive: Bool {
        self == .running || self == .starting
    }
}
```

- [ ] **Step 2: 创建 LogEntry.swift**

```swift
import Foundation
import GRDB

struct LogEntry: Identifiable, FetchableRecord, PersistableRecord {
    var id: UUID
    var timestamp: Date
    var serviceId: UUID
    var serviceName: String
    var level: LogLevel
    var message: String
    var normalizedMessage: String  // 去除可变部分后的消息，用于去重
    var runId: UUID
    var repeatCount: Int

    init(
        id: UUID = UUID(),
        timestamp: Date = Date(),
        serviceId: UUID,
        serviceName: String,
        level: LogLevel,
        message: String,
        normalizedMessage: String,
        runId: UUID,
        repeatCount: Int = 1
    ) {
        self.id = id
        self.timestamp = timestamp
        self.serviceId = serviceId
        self.serviceName = serviceName
        self.level = level
        self.message = message
        self.normalizedMessage = normalizedMessage
        self.runId = runId
        self.repeatCount = repeatCount
    }

    // GRDB table name
    static let databaseTableName = "log_entries"
}

enum LogLevel: String, Codable, CaseIterable {
    case error = "ERROR"
    case warn  = "WARN"
    case info  = "INFO"
    case debug = "DEBUG"
    case unknown = "UNKNOWN"

    var priority: Int {
        switch self {
        case .error: return 4
        case .warn:  return 3
        case .info:  return 2
        case .debug: return 1
        case .unknown: return 0
        }
    }
}
```

- [ ] **Step 3: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 4: Commit**

```bash
git add SuperDev/AppCore/Models/
git commit -m "feat: add Project, Service, LogEntry data models"
```

---

## Task 3: 配置加载器（ConfigLoader）

**Files:**
- Create: `SuperDev/AppCore/Config/ConfigLoader.swift`
- Test: `SuperDevTests/ConfigLoaderTests.swift`

- [ ] **Step 1: 写失败测试**

```swift
// SuperDevTests/ConfigLoaderTests.swift
import XCTest
@testable import SuperDev

final class ConfigLoaderTests: XCTestCase {

    var tempDir: URL!

    override func setUp() {
        super.setUp()
        tempDir = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
        try! FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: tempDir)
        super.tearDown()
    }

    func test_load_parsesNameAndServices() throws {
        let yaml = """
        name: TestProject
        services:
          - name: api
            command: go run ./cmd/api
            working_dir: .
            required: true
            env:
              PORT: "8080"
          - name: web
            command: pnpm dev
            working_dir: ./web
            required: false
        """
        let configDir = tempDir.appendingPathComponent(".superdev")
        try FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
        let configFile = configDir.appendingPathComponent("config.yaml")
        try yaml.write(to: configFile, atomically: true, encoding: .utf8)

        let loader = ConfigLoader(rootPath: tempDir.path)
        let project = try loader.load()

        XCTAssertEqual(project.name, "TestProject")
        XCTAssertEqual(project.services.count, 2)
        XCTAssertEqual(project.services[0].name, "api")
        XCTAssertEqual(project.services[0].command, "go run ./cmd/api")
        XCTAssertEqual(project.services[0].required, true)
        XCTAssertEqual(project.services[0].env["PORT"], "8080")
        XCTAssertEqual(project.services[1].name, "web")
        XCTAssertEqual(project.services[1].required, false)
    }

    func test_load_throwsWhenConfigMissing() {
        let loader = ConfigLoader(rootPath: tempDir.path)
        XCTAssertThrowsError(try loader.load()) { error in
            XCTAssertTrue(error is ConfigLoader.ConfigError)
        }
    }

    func test_save_writesValidYaml() throws {
        let project = Project(
            name: "SaveTest",
            rootPath: tempDir.path,
            services: [
                Service(name: "svc", command: "echo hello", workingDir: ".", required: true)
            ]
        )
        let loader = ConfigLoader(rootPath: tempDir.path)
        try loader.save(project)

        let reloaded = try loader.load()
        XCTAssertEqual(reloaded.name, "SaveTest")
        XCTAssertEqual(reloaded.services.count, 1)
        XCTAssertEqual(reloaded.services[0].name, "svc")
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

Xcode → ⌘U（或 Product → Test）
Expected: 编译失败，`ConfigLoader` 未定义

- [ ] **Step 3: 实现 ConfigLoader.swift**

```swift
import Foundation
import Yams

final class ConfigLoader {
    let rootPath: String

    private var configDir: URL {
        URL(fileURLWithPath: rootPath).appendingPathComponent(".superdev")
    }

    private var configFile: URL {
        configDir.appendingPathComponent("config.yaml")
    }

    enum ConfigError: Error {
        case fileNotFound
        case parseError(String)
    }

    init(rootPath: String) {
        self.rootPath = rootPath
    }

    func load() throws -> Project {
        guard FileManager.default.fileExists(atPath: configFile.path) else {
            throw ConfigError.fileNotFound
        }
        let content = try String(contentsOf: configFile, encoding: .utf8)
        let raw = try Yams.load(yaml: content)
        return try parseProject(from: raw)
    }

    func save(_ project: Project) throws {
        try FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
        let dict = projectToDict(project)
        let yaml = try Yams.dump(object: dict)
        try yaml.write(to: configFile, atomically: true, encoding: .utf8)
    }

    // MARK: - Private

    private func parseProject(from raw: Any?) throws -> Project {
        guard let dict = raw as? [String: Any] else {
            throw ConfigError.parseError("Root must be a mapping")
        }
        let name = dict["name"] as? String ?? "Unnamed"
        let rawServices = dict["services"] as? [[String: Any]] ?? []
        let services = try rawServices.map { try parseService(from: $0) }
        return Project(name: name, rootPath: rootPath, services: services)
    }

    private func parseService(from dict: [String: Any]) throws -> Service {
        guard let name = dict["name"] as? String,
              let command = dict["command"] as? String else {
            throw ConfigError.parseError("Service missing name or command")
        }
        let workingDir = dict["working_dir"] as? String ?? "."
        let required = dict["required"] as? Bool ?? false
        let envFile = dict["env_file"] as? String
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

    private func projectToDict(_ project: Project) -> [String: Any] {
        var dict: [String: Any] = ["name": project.name]
        dict["services"] = project.services.map { serviceToDict($0) }
        return dict
    }

    private func serviceToDict(_ service: Service) -> [String: Any] {
        var dict: [String: Any] = [
            "name": service.name,
            "command": service.command,
            "working_dir": service.workingDir,
            "required": service.required
        ]
        if let envFile = service.envFile { dict["env_file"] = envFile }
        if !service.env.isEmpty { dict["env"] = service.env }
        return dict
    }
}
```

- [ ] **Step 4: 运行测试，确认通过**

Xcode → ⌘U
Expected: 3 tests passed

- [ ] **Step 5: Commit**

```bash
git add SuperDev/AppCore/Config/ConfigLoader.swift SuperDevTests/ConfigLoaderTests.swift
git commit -m "feat: add ConfigLoader with YAML read/write and tests"
```

---

## Task 4: launch.json 导入器

**Files:**
- Create: `SuperDev/AppCore/Config/LaunchJsonImporter.swift`
- Test: `SuperDevTests/LaunchJsonImporterTests.swift`

- [ ] **Step 1: 写失败测试**

```swift
// SuperDevTests/LaunchJsonImporterTests.swift
import XCTest
@testable import SuperDev

final class LaunchJsonImporterTests: XCTestCase {

    var tempDir: URL!

    override func setUp() {
        super.setUp()
        tempDir = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
        try! FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: tempDir)
        super.tearDown()
    }

    func test_import_convertsConfigurations() throws {
        let json = """
        {
            "version": "0.2.0",
            "configurations": [
                {
                    "name": "Launch API",
                    "type": "go",
                    "request": "launch",
                    "program": "./cmd/api",
                    "args": ["--port", "8080"],
                    "cwd": "${workspaceFolder}",
                    "env": {
                        "LOG_LEVEL": "debug"
                    }
                },
                {
                    "name": "Launch Web",
                    "type": "node",
                    "request": "launch",
                    "runtimeExecutable": "pnpm",
                    "runtimeArgs": ["dev"],
                    "cwd": "${workspaceFolder}/web"
                }
            ]
        }
        """
        let vscodeDir = tempDir.appendingPathComponent(".vscode")
        try FileManager.default.createDirectory(at: vscodeDir, withIntermediateDirectories: true)
        try json.write(to: vscodeDir.appendingPathComponent("launch.json"), atomically: true, encoding: .utf8)

        let importer = LaunchJsonImporter(rootPath: tempDir.path)
        let services = try importer.importServices()

        XCTAssertEqual(services.count, 2)
        XCTAssertEqual(services[0].name, "Launch API")
        XCTAssertTrue(services[0].command.contains("./cmd/api"))
        XCTAssertEqual(services[0].env["LOG_LEVEL"], "debug")
        XCTAssertEqual(services[1].name, "Launch Web")
    }

    func test_import_throwsWhenFileAbsent() {
        let importer = LaunchJsonImporter(rootPath: tempDir.path)
        XCTAssertThrowsError(try importer.importServices())
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

Xcode → ⌘U
Expected: 编译失败，`LaunchJsonImporter` 未定义

- [ ] **Step 3: 实现 LaunchJsonImporter.swift**

```swift
import Foundation

final class LaunchJsonImporter {
    let rootPath: String

    enum ImportError: Error {
        case fileNotFound
        case invalidFormat
    }

    init(rootPath: String) {
        self.rootPath = rootPath
    }

    func importServices() throws -> [Service] {
        let launchFile = URL(fileURLWithPath: rootPath)
            .appendingPathComponent(".vscode/launch.json")
        guard FileManager.default.fileExists(atPath: launchFile.path) else {
            throw ImportError.fileNotFound
        }
        let data = try Data(contentsOf: launchFile)
        guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let configs = json["configurations"] as? [[String: Any]] else {
            throw ImportError.invalidFormat
        }
        return configs.compactMap { parseConfiguration($0) }
    }

    // MARK: - Private

    private func parseConfiguration(_ config: [String: Any]) -> Service? {
        guard let name = config["name"] as? String else { return nil }

        let command = buildCommand(from: config)
        let cwd = (config["cwd"] as? String ?? "${workspaceFolder}")
            .replacingOccurrences(of: "${workspaceFolder}", with: ".")
            .replacingOccurrences(of: "${workspaceFolder}/", with: "./")
        let env = (config["env"] as? [String: Any])?.compactMapValues { $0 as? String } ?? [:]

        return Service(
            name: name,
            command: command,
            workingDir: cwd,
            required: false,
            env: env
        )
    }

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
        return config["name"] as? String ?? "echo 'unknown command'"
    }
}
```

- [ ] **Step 4: 运行测试，确认通过**

Xcode → ⌘U
Expected: 2 tests passed

- [ ] **Step 5: Commit**

```bash
git add SuperDev/AppCore/Config/LaunchJsonImporter.swift SuperDevTests/LaunchJsonImporterTests.swift
git commit -m "feat: add LaunchJsonImporter for .vscode/launch.json"
```

---

## Task 5: 项目列表持久化（ProjectStore）

**Files:**
- Create: `SuperDev/AppCore/Config/ProjectStore.swift`

- [ ] **Step 1: 实现 ProjectStore.swift**

```swift
import Foundation

// 持久化用户添加的项目根路径列表（UserDefaults）
// 配置内容本身存在各项目的 .superdev/config.yaml 中
final class ProjectStore {
    private let key = "superdev.project_paths"
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    func loadPaths() -> [String] {
        defaults.stringArray(forKey: key) ?? []
    }

    func addPath(_ path: String) {
        var paths = loadPaths()
        guard !paths.contains(path) else { return }
        paths.append(path)
        defaults.set(paths, forKey: key)
    }

    func removePath(_ path: String) {
        var paths = loadPaths()
        paths.removeAll { $0 == path }
        defaults.set(paths, forKey: key)
    }
}
```

- [ ] **Step 2: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 3: Commit**

```bash
git add SuperDev/AppCore/Config/ProjectStore.swift
git commit -m "feat: add ProjectStore for persisting project paths"
```

---

## Task 6: 单进程运行器（ProcessRunner）

**Files:**
- Create: `SuperDev/AppCore/Process/ProcessRunner.swift`
- Test: `SuperDevTests/ProcessRunnerTests.swift`

- [ ] **Step 1: 写失败测试**

```swift
// SuperDevTests/ProcessRunnerTests.swift
import XCTest
@testable import SuperDev

final class ProcessRunnerTests: XCTestCase {

    func test_run_capturesStdout() async throws {
        var received: [String] = []
        let runner = ProcessRunner(
            command: "echo hello",
            workingDir: "/tmp",
            env: [:]
        ) { line in
            received.append(line)
        }
        try runner.start()
        try await Task.sleep(nanoseconds: 500_000_000) // 0.5s
        XCTAssertTrue(received.contains(where: { $0.contains("hello") }))
    }

    func test_run_canBeStopped() async throws {
        let runner = ProcessRunner(
            command: "sleep 60",
            workingDir: "/tmp",
            env: [:]
        ) { _ in }
        try runner.start()
        XCTAssertTrue(runner.isRunning)
        runner.stop()
        try await Task.sleep(nanoseconds: 300_000_000) // 0.3s
        XCTAssertFalse(runner.isRunning)
    }

    func test_run_mergesEnvFile() async throws {
        // 写一个临时 .env 文件
        let envContent = "TEST_KEY=from_file\n"
        let envFile = URL(fileURLWithPath: "/tmp/test_runner.env")
        try envContent.write(to: envFile, atomically: true, encoding: .utf8)

        var received: [String] = []
        let runner = ProcessRunner(
            command: "sh -c 'echo $TEST_KEY'",
            workingDir: "/tmp",
            env: [:],
            envFile: "/tmp/test_runner.env"
        ) { line in
            received.append(line)
        }
        try runner.start()
        try await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertTrue(received.contains(where: { $0.contains("from_file") }))
        try? FileManager.default.removeItem(at: envFile)
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

Xcode → ⌘U
Expected: 编译失败，`ProcessRunner` 未定义

- [ ] **Step 3: 实现 ProcessRunner.swift**

```swift
import Foundation

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
        // 先加载 env_file
        if let envFile = envFile {
            loadEnvFile(envFile).forEach { result[$0] = $1 }
        }
        // 直接定义的 env 优先级更高
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
```

- [ ] **Step 4: 运行测试，确认通过**

Xcode → ⌘U
Expected: 3 tests passed

- [ ] **Step 5: Commit**

```bash
git add SuperDev/AppCore/Process/ProcessRunner.swift SuperDevTests/ProcessRunnerTests.swift
git commit -m "feat: add ProcessRunner with stdout/stderr capture and env_file support"
```

---

## Task 7: 进程管理器（ProcessManager）

**Files:**
- Create: `SuperDev/AppCore/Process/ProcessManager.swift`

- [ ] **Step 1: 实现 ProcessManager.swift**

```swift
import Foundation

// 管理一个 Project 内所有 Service 的 ProcessRunner 生命周期
@MainActor
final class ProcessManager {
    private var runners: [UUID: ProcessRunner] = [:]
    private let onLog: (UUID, String, String) -> Void  // (serviceId, serviceName, line)
    private let onStatusChange: (UUID, ServiceStatus) -> Void

    init(
        onLog: @escaping (UUID, String, String) -> Void,
        onStatusChange: @escaping (UUID, ServiceStatus) -> Void
    ) {
        self.onLog = onLog
        self.onStatusChange = onStatusChange
    }

    func start(_ service: Service, projectRootPath: String) {
        guard runners[service.id] == nil else { return }

        let workingDir = resolveWorkingDir(service.workingDir, rootPath: projectRootPath)
        let envFilePath = service.envFile.map {
            URL(fileURLWithPath: projectRootPath).appendingPathComponent($0).path
        }

        onStatusChange(service.id, .starting)

        let runner = ProcessRunner(
            command: service.command,
            workingDir: workingDir,
            env: service.env,
            envFile: envFilePath
        ) { [weak self] line in
            DispatchQueue.main.async {
                self?.onLog(service.id, service.name, line)
            }
        }

        do {
            try runner.start()
            runners[service.id] = runner
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                guard let self else { return }
                if runner.isRunning {
                    self.onStatusChange(service.id, .running)
                }
            }
        } catch {
            onStatusChange(service.id, .failed)
        }
    }

    func stop(_ serviceId: UUID) {
        runners[serviceId]?.stop()
        runners[serviceId] = nil
        onStatusChange(serviceId, .stopped)
    }

    func stopAll() {
        runners.values.forEach { $0.stop() }
        runners.removeAll()
    }

    func isRunning(_ serviceId: UUID) -> Bool {
        runners[serviceId]?.isRunning ?? false
    }

    // MARK: - Private

    private func resolveWorkingDir(_ dir: String, rootPath: String) -> String {
        if dir == "." { return rootPath }
        if dir.hasPrefix("/") { return dir }
        return URL(fileURLWithPath: rootPath).appendingPathComponent(dir).path
    }
}
```

- [ ] **Step 2: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 3: Commit**

```bash
git add SuperDev/AppCore/Process/ProcessManager.swift
git commit -m "feat: add ProcessManager for multi-service lifecycle management"
```

---

## Task 8: 日志引擎（LogEngine）

**Files:**
- Create: `SuperDev/AppCore/Log/LogEngine.swift`
- Test: `SuperDevTests/LogEngineTests.swift`

- [ ] **Step 1: 写失败测试**

```swift
// SuperDevTests/LogEngineTests.swift
import XCTest
@testable import SuperDev

final class LogEngineTests: XCTestCase {

    func test_parse_detectsErrorLevel() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("2024-01-01 10:00:00 ERROR database connection failed",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .error)
    }

    func test_parse_detectsWarnLevel() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("[WARN] slow query detected",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .warn)
    }

    func test_parse_defaultsToInfo() {
        let engine = LogEngine(runId: UUID())
        let entry = engine.parseLine("server started on port 8080",
                                     serviceId: UUID(), serviceName: "api")
        XCTAssertEqual(entry.level, .info)
    }

    func test_dedup_foldsDuplicates() {
        let engine = LogEngine(runId: UUID())
        let sid = UUID()
        let e1 = engine.parseLine("ERROR disk full", serviceId: sid, serviceName: "api")
        let e2 = engine.parseLine("ERROR disk full", serviceId: sid, serviceName: "api")

        XCTAssertEqual(e1.normalizedMessage, e2.normalizedMessage)

        var entries: [LogEntry] = []
        engine.ingest(e1, into: &entries)
        engine.ingest(e2, into: &entries)

        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries[0].repeatCount, 2)
    }

    func test_dedup_doesNotFoldDifferentServices() {
        let engine = LogEngine(runId: UUID())
        let e1 = engine.parseLine("ERROR disk full", serviceId: UUID(), serviceName: "svc1")
        let e2 = engine.parseLine("ERROR disk full", serviceId: UUID(), serviceName: "svc2")

        var entries: [LogEntry] = []
        engine.ingest(e1, into: &entries)
        engine.ingest(e2, into: &entries)

        XCTAssertEqual(entries.count, 2)
    }

    func test_normalize_stripsTimestamps() {
        let engine = LogEngine(runId: UUID())
        let n1 = engine.normalize("10:23:01 ERROR disk full")
        let n2 = engine.normalize("10:23:59 ERROR disk full")
        XCTAssertEqual(n1, n2)
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

Xcode → ⌘U
Expected: 编译失败，`LogEngine` 未定义

- [ ] **Step 3: 实现 LogEngine.swift**

```swift
import Foundation

final class LogEngine {
    let runId: UUID

    init(runId: UUID) {
        self.runId = runId
    }

    /// 解析原始行，生成 LogEntry
    func parseLine(_ line: String, serviceId: UUID, serviceName: String) -> LogEntry {
        let level = detectLevel(in: line)
        let normalized = normalize(line)
        return LogEntry(
            serviceId: serviceId,
            serviceName: serviceName,
            level: level,
            message: line,
            normalizedMessage: normalized,
            runId: runId
        )
    }

    /// 将 entry 放入列表，若与末尾重复则折叠（更新 repeatCount）
    func ingest(_ entry: LogEntry, into entries: inout [LogEntry]) {
        if var last = entries.last,
           last.serviceId == entry.serviceId,
           last.normalizedMessage == entry.normalizedMessage {
            last.repeatCount += 1
            last.timestamp = entry.timestamp
            entries[entries.count - 1] = last
        } else {
            entries.append(entry)
        }
    }

    /// 去除行中可变部分，用于去重比对
    func normalize(_ line: String) -> String {
        var result = line
        // 去除开头时间戳 HH:MM:SS
        result = result.replacingOccurrences(
            of: #"^\d{2}:\d{2}:\d{2}(\.\d+)?\s*"#,
            with: "",
            options: .regularExpression
        )
        // 去除 ISO 日期前缀
        result = result.replacingOccurrences(
            of: #"^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?\s*"#,
            with: "",
            options: .regularExpression
        )
        // 数字 ID 替换（uid=123 → uid=*）
        result = result.replacingOccurrences(
            of: #"=\d+"#,
            with: "=*",
            options: .regularExpression
        )
        // IP:port 替换
        result = result.replacingOccurrences(
            of: #"\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+"#,
            with: "*:*",
            options: .regularExpression
        )
        return result.trimmingCharacters(in: .whitespaces)
    }

    // MARK: - Private

    private func detectLevel(in line: String) -> LogLevel {
        let upper = line.uppercased()
        if upper.contains("ERROR") || upper.contains("FATAL") || upper.contains("CRITICAL") {
            return .error
        }
        if upper.contains("WARN") || upper.contains("WARNING") {
            return .warn
        }
        if upper.contains("DEBUG") || upper.contains("TRACE") {
            return .debug
        }
        if upper.contains("INFO") {
            return .info
        }
        return .info  // 默认 info，避免过多 unknown 噪音
    }
}
```

- [ ] **Step 4: 运行测试，确认通过**

Xcode → ⌘U
Expected: 5 tests passed

- [ ] **Step 5: Commit**

```bash
git add SuperDev/AppCore/Log/LogEngine.swift SuperDevTests/LogEngineTests.swift
git commit -m "feat: add LogEngine with level detection, dedup, and normalization"
```

---

## Task 9: 日志持久化（LogStore）

**Files:**
- Create: `SuperDev/AppCore/Log/LogStore.swift`

- [ ] **Step 1: 实现 LogStore.swift**

```swift
import Foundation
import GRDB

// SQLite 持久化，存储路径：~/Library/Application Support/SuperDev/logs.db
final class LogStore {
    private var db: DatabaseQueue?

    init() {
        setupDatabase()
    }

    // MARK: - Public

    func append(_ entry: LogEntry) {
        try? db?.write { db in
            try entry.insert(db)
        }
    }

    func fetch(
        serviceId: UUID? = nil,
        runId: UUID? = nil,
        levels: Set<LogLevel>? = nil,
        keyword: String? = nil,
        limit: Int = 1000
    ) -> [LogEntry] {
        (try? db?.read { db in
            var conditions: [String] = []
            var args: [DatabaseValue] = []

            if let sid = serviceId {
                conditions.append("service_id = ?")
                args.append(sid.uuidString.databaseValue)
            }
            if let rid = runId {
                conditions.append("run_id = ?")
                args.append(rid.uuidString.databaseValue)
            }
            if let levels = levels, !levels.isEmpty {
                let placeholders = levels.map { _ in "?" }.joined(separator: ",")
                conditions.append("level IN (\(placeholders))")
                levels.forEach { args.append($0.rawValue.databaseValue) }
            }
            if let kw = keyword, !kw.isEmpty {
                conditions.append("message LIKE ?")
                args.append("%\(kw)%".databaseValue)
            }

            let where_ = conditions.isEmpty ? "" : "WHERE " + conditions.joined(separator: " AND ")
            let sql = "SELECT * FROM log_entries \(where_) ORDER BY timestamp DESC LIMIT \(limit)"
            return try LogEntry.fetchAll(db, sql: SQL(sql, arguments: StatementArguments(args)))
        }) ?? []
    }

    func deleteOldRuns(keepLast count: Int = 10) {
        try? db?.write { db in
            let sql = """
                DELETE FROM log_entries WHERE run_id NOT IN (
                    SELECT DISTINCT run_id FROM log_entries
                    ORDER BY MIN(timestamp) DESC LIMIT ?
                )
            """
            try db.execute(sql: sql, arguments: [count])
        }
    }

    // MARK: - Private

    private func setupDatabase() {
        guard let appSupport = FileManager.default.urls(
            for: .applicationSupportDirectory, in: .userDomainMask
        ).first else { return }

        let dbDir = appSupport.appendingPathComponent("SuperDev")
        try? FileManager.default.createDirectory(at: dbDir, withIntermediateDirectories: true)
        let dbPath = dbDir.appendingPathComponent("logs.db").path

        db = try? DatabaseQueue(path: dbPath)
        createTableIfNeeded()
    }

    private func createTableIfNeeded() {
        try? db?.write { db in
            try db.create(table: "log_entries", ifNotExists: true) { t in
                t.column("id", .text).primaryKey()
                t.column("timestamp", .datetime).notNull().indexed()
                t.column("service_id", .text).notNull().indexed()
                t.column("service_name", .text).notNull()
                t.column("level", .text).notNull().indexed()
                t.column("message", .text).notNull()
                t.column("normalized_message", .text).notNull()
                t.column("run_id", .text).notNull().indexed()
                t.column("repeat_count", .integer).notNull().defaults(to: 1)
            }
        }
    }
}
```

- [ ] **Step 2: 让 LogEntry 支持 GRDB column mapping**

在 `LogEntry.swift` 末尾添加：

```swift
extension LogEntry {
    enum Columns {
        static let id = Column("id")
        static let timestamp = Column("timestamp")
        static let serviceId = Column("service_id")
        static let serviceName = Column("service_name")
        static let level = Column("level")
        static let message = Column("message")
        static let normalizedMessage = Column("normalized_message")
        static let runId = Column("run_id")
        static let repeatCount = Column("repeat_count")
    }

    init(row: Row) throws {
        id = UUID(uuidString: row[Columns.id]) ?? UUID()
        timestamp = row[Columns.timestamp]
        serviceId = UUID(uuidString: row[Columns.serviceId]) ?? UUID()
        serviceName = row[Columns.serviceName]
        level = LogLevel(rawValue: row[Columns.level]) ?? .unknown
        message = row[Columns.message]
        normalizedMessage = row[Columns.normalizedMessage]
        runId = UUID(uuidString: row[Columns.runId]) ?? UUID()
        repeatCount = row[Columns.repeatCount]
    }

    func encode(to container: inout PersistenceContainer) throws {
        container[Columns.id] = id.uuidString
        container[Columns.timestamp] = timestamp
        container[Columns.serviceId] = serviceId.uuidString
        container[Columns.serviceName] = serviceName
        container[Columns.level] = level.rawValue
        container[Columns.message] = message
        container[Columns.normalizedMessage] = normalizedMessage
        container[Columns.runId] = runId.uuidString
        container[Columns.repeatCount] = repeatCount
    }
}
```

- [ ] **Step 3: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 4: Commit**

```bash
git add SuperDev/AppCore/Log/LogStore.swift SuperDev/AppCore/Models/LogEntry.swift
git commit -m "feat: add LogStore with SQLite persistence via GRDB"
```

---

## Task 10: AppCore（核心状态管理）

**Files:**
- Create: `SuperDev/AppCore/AppCore.swift`

- [ ] **Step 1: 实现 AppCore.swift**

```swift
import Foundation
import SwiftUI

// 整个 App 的核心状态，UI 层通过 @StateObject/@EnvironmentObject 共享此实例
@MainActor
final class AppCore: ObservableObject {
    @Published var projects: [Project] = []
    @Published var logs: [LogEntry] = []
    @Published var currentRunId: UUID = UUID()

    private let projectStore = ProjectStore()
    private let logStore = LogStore()
    private let logEngine: LogEngine
    private var processManagers: [UUID: ProcessManager] = [:]  // projectId → manager

    init() {
        logEngine = LogEngine(runId: currentRunId)
        loadProjects()
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
        processManagers[project.id]?.stop(service.id)
        updateServiceStatus(service.id, status: .stopped, in: project.id)
    }

    func startSelected(services: [Service], in project: Project) {
        services.forEach { start($0, in: project) }
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
}
```

- [ ] **Step 2: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 3: Commit**

```bash
git add SuperDev/AppCore/AppCore.swift
git commit -m "feat: add AppCore as central state manager connecting all subsystems"
```

---

## Task 11: App 入口和菜单栏

**Files:**
- Create: `SuperDev/SuperDevApp.swift`
- Create: `SuperDev/UI/MenuBar/MenuBarManager.swift`
- Create: `SuperDev/Resources/Assets.xcassets/` (菜单栏图标)

- [ ] **Step 1: 添加菜单栏图标资源**

在 Xcode Assets.xcassets 中：
- 新建 Image Set，命名 `menubar-icon`
- 添加一个 16×16 的 PNG 图标（可暂用系统 SF Symbol 代替）
- 在 Image Set 属性中勾选 "Render As: Template Image"

或在代码中直接使用 SF Symbol（开发阶段）：
```swift
// 使用 "terminal" SF Symbol 作为临时图标
NSImage(systemSymbolName: "terminal", accessibilityDescription: "SuperDev")
```

- [ ] **Step 2: 实现 MenuBarManager.swift**

```swift
import AppKit
import SwiftUI

@MainActor
final class MenuBarManager {
    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private let core: AppCore

    init(core: AppCore) {
        self.core = core
        setup()
    }

    func updateIcon(status: ProjectStatus) {
        let icon: NSImage?
        switch status {
        case .stopped:
            icon = NSImage(systemSymbolName: "terminal", accessibilityDescription: "SuperDev")
        case .starting:
            icon = NSImage(systemSymbolName: "terminal.fill", accessibilityDescription: "SuperDev")
        case .running:
            icon = NSImage(systemSymbolName: "play.circle.fill", accessibilityDescription: "SuperDev")
        case .failed:
            icon = NSImage(systemSymbolName: "exclamationmark.circle.fill", accessibilityDescription: "SuperDev")
        }
        icon?.isTemplate = true
        statusItem?.button?.image = icon
    }

    // MARK: - Private

    private func setup() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let icon = NSImage(systemSymbolName: "terminal", accessibilityDescription: "SuperDev")
        icon?.isTemplate = true
        statusItem?.button?.image = icon
        statusItem?.button?.action = #selector(togglePopover)
        statusItem?.button?.target = self

        let popover = NSPopover()
        popover.contentSize = NSSize(width: 440, height: 360)
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(
            rootView: PopoverView().environmentObject(core)
        )
        self.popover = popover
    }

    @objc private func togglePopover() {
        guard let button = statusItem?.button else { return }
        if popover?.isShown == true {
            popover?.performClose(nil)
        } else {
            popover?.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        }
    }
}
```

- [ ] **Step 3: 实现 SuperDevApp.swift**

```swift
import SwiftUI

@main
struct SuperDevApp: App {
    @StateObject private var core = AppCore()
    @State private var menuBarManager: MenuBarManager?

    var body: some Scene {
        // 主窗口（通过 Popover 的"查看日志"按钮打开）
        Window("SuperDev", id: "main") {
            MainWindowView()
                .environmentObject(core)
        }
        .windowResizability(.contentSize)

        Settings {
            SettingsView()
                .environmentObject(core)
        }
    }

    init() {
        // MenuBarManager 在 AppDelegate-like 方式中初始化
        // 此处使用 onAppear 时机
    }
}

// App 级别的 NSApplicationDelegate，用于菜单栏初始化
final class AppDelegate: NSObject, NSApplicationDelegate {
    var menuBarManager: MenuBarManager?
    var core: AppCore?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)  // 无 Dock 图标
    }
}
```

**注意**：SwiftUI `@main` 与 `NSApplicationDelegate` 的配合需要在 `SuperDevApp` 中用 `@NSApplicationDelegateAdaptor`：

```swift
@main
struct SuperDevApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var core = AppCore()

    var body: some Scene {
        Window("SuperDev", id: "main") {
            MainWindowView().environmentObject(core)
        }
        Settings {
            SettingsView().environmentObject(core)
        }
    }
}
```

在 `AppDelegate.applicationDidFinishLaunching` 中：
```swift
func applicationDidFinishLaunching(_ notification: Notification) {
    NSApp.setActivationPolicy(.accessory)
    // core 通过 notification/shared 方式传入，或在 AppDelegate 自建
}
```

- [ ] **Step 4: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded（PopoverView 等 UI 文件还不存在，先创建空占位）

创建空占位文件：
```swift
// SuperDev/UI/MenuBar/PopoverView.swift
import SwiftUI
struct PopoverView: View {
    var body: some View { Text("TODO") }
}

// SuperDev/UI/MainWindow/MainWindowView.swift
import SwiftUI
struct MainWindowView: View {
    var body: some View { Text("TODO") }
}

// SuperDev/UI/Settings/SettingsView.swift
import SwiftUI
struct SettingsView: View {
    var body: some View { Text("TODO") }
}
```

- [ ] **Step 5: Commit**

```bash
git add SuperDev/SuperDevApp.swift SuperDev/UI/MenuBar/MenuBarManager.swift SuperDev/UI/
git commit -m "feat: add app entry, MenuBarManager, and UI placeholder files"
```

---

## Task 12: Popover UI（两层交互）

**Files:**
- Modify: `SuperDev/UI/MenuBar/PopoverView.swift`

- [ ] **Step 1: 实现 PopoverView.swift**

```swift
import SwiftUI

struct PopoverView: View {
    @EnvironmentObject var core: AppCore
    @State private var hoveredProjectId: UUID?
    @State private var selectedServiceIds: Set<UUID> = []

    var body: some View {
        HStack(spacing: 0) {
            projectList
            if let project = hoveredProject {
                Divider()
                servicePanel(for: project)
            }
        }
        .frame(minWidth: hoveredProject == nil ? 200 : 440, minHeight: 300)
    }

    // MARK: - 一级：项目列表

    private var projectList: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("项目")
                .font(.caption)
                .foregroundColor(.secondary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)

            ForEach(core.projects) { project in
                projectRow(project)
            }

            Divider().padding(.vertical, 4)

            Button {
                openAddProject()
            } label: {
                Label("添加项目", systemImage: "plus")
                    .font(.subheadline)
            }
            .buttonStyle(.plain)
            .foregroundColor(.accentColor)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)

            Spacer()
        }
        .frame(width: 200)
    }

    private func projectRow(_ project: Project) -> some View {
        HStack(spacing: 8) {
            Circle()
                .fill(statusColor(project.overallStatus))
                .frame(width: 8, height: 8)
            Text(project.name)
                .lineLimit(1)
            Spacer()
            Image(systemName: "chevron.right")
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(hoveredProjectId == project.id ? Color.accentColor.opacity(0.1) : Color.clear)
        .overlay(
            hoveredProjectId == project.id
                ? Rectangle().frame(width: 3).foregroundColor(.accentColor)
                : nil,
            alignment: .leading
        )
        .contentShape(Rectangle())
        .onHover { isHovered in
            if isHovered {
                hoveredProjectId = project.id
                selectedServiceIds = Set(project.services.filter { $0.required }.map { $0.id })
            }
        }
    }

    // MARK: - 二级：子进程面板

    private func servicePanel(for project: Project) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            // 标题栏
            HStack {
                Text(project.name).fontWeight(.semibold)
                Spacer()
                Button("全部停止") { core.stopAll(project: project) }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .tint(.red)
                Button("▶ 启动选中") {
                    let toStart = project.services.filter { selectedServiceIds.contains($0.id) }
                    core.startSelected(services: toStart, in: project)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Color(NSColor.controlBackgroundColor))

            Divider()

            // 全选工具栏
            HStack {
                Toggle(isOn: allSelectedBinding(for: project)) {
                    Text("全选").font(.subheadline)
                }
                .toggleStyle(.checkbox)
                Spacer()
                Button("反选") { toggleInvert(for: project) }
                    .buttonStyle(.plain)
                    .foregroundColor(.accentColor)
                    .font(.subheadline)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)

            Divider()

            // 服务列表
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    let required = project.services.filter { $0.required }
                    let optional = project.services.filter { !$0.required }

                    if !required.isEmpty {
                        serviceGroupHeader("必须启动")
                        ForEach(required) { service in
                            serviceRow(service, in: project)
                        }
                    }

                    if !optional.isEmpty {
                        serviceGroupHeader("可选")
                        ForEach(optional) { service in
                            serviceRow(service, in: project)
                        }
                    }
                }
            }

            Divider()

            // 底部
            HStack {
                Spacer()
                Button {
                    openMainWindow()
                } label: {
                    Label("查看日志", systemImage: "doc.text")
                        .font(.subheadline)
                }
                .buttonStyle(.plain)
                .foregroundColor(.accentColor)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        }
        .frame(width: 260)
    }

    private func serviceGroupHeader(_ title: String) -> some View {
        Text(title)
            .font(.caption)
            .foregroundColor(.secondary)
            .textCase(.uppercase)
            .padding(.horizontal, 12)
            .padding(.top, 8)
            .padding(.bottom, 2)
    }

    private func serviceRow(_ service: Service, in project: Project) -> some View {
        HStack(spacing: 8) {
            Toggle("", isOn: Binding(
                get: { selectedServiceIds.contains(service.id) },
                set: { checked in
                    if checked { selectedServiceIds.insert(service.id) }
                    else { selectedServiceIds.remove(service.id) }
                }
            ))
            .toggleStyle(.checkbox)
            .labelsHidden()

            Circle()
                .fill(statusColor(service.status))
                .frame(width: 7, height: 7)

            Text(service.name)
                .font(.subheadline)
                .lineLimit(1)

            Spacer()

            Text(statusLabel(service.status))
                .font(.caption)
                .foregroundColor(statusColor(service.status))

            Button {
                if service.status.isActive {
                    core.stop(service, in: project)
                } else {
                    core.start(service, in: project)
                }
            } label: {
                Image(systemName: service.status.isActive ? "stop.fill" : "play.fill")
                    .font(.caption)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.mini)
            .tint(service.status.isActive ? .red : .green)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    // MARK: - Helpers

    private var hoveredProject: Project? {
        guard let id = hoveredProjectId else { return nil }
        return core.projects.first { $0.id == id }
    }

    private func allSelectedBinding(for project: Project) -> Binding<Bool> {
        Binding(
            get: { project.services.allSatisfy { selectedServiceIds.contains($0.id) } },
            set: { all in
                if all { project.services.forEach { selectedServiceIds.insert($0.id) } }
                else { selectedServiceIds.removeAll() }
            }
        )
    }

    private func toggleInvert(for project: Project) {
        let all = Set(project.services.map { $0.id })
        selectedServiceIds = all.subtracting(selectedServiceIds)
    }

    private func openMainWindow() {
        NSApp.sendAction(Selector(("showMainWindow:")), to: nil, from: nil)
        // 或使用 openWindow environment action
    }

    private func openAddProject() {
        // 打开添加项目 sheet（通过 NSApp 激活 Settings 或自定义窗口）
        NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
    }

    private func statusColor(_ status: ServiceStatus) -> Color {
        switch status {
        case .stopped: return .gray
        case .starting: return .yellow
        case .running: return .green
        case .failed: return .red
        }
    }

    private func statusColor(_ status: ProjectStatus) -> Color {
        switch status {
        case .stopped: return .gray
        case .starting: return .yellow
        case .running: return .green
        case .failed: return .red
        }
    }

    private func statusLabel(_ status: ServiceStatus) -> String {
        switch status {
        case .stopped: return "未启动"
        case .starting: return "启动中…"
        case .running: return "运行中"
        case .failed: return "已退出"
        }
    }
}
```

- [ ] **Step 2: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 3: Commit**

```bash
git add SuperDev/UI/MenuBar/PopoverView.swift
git commit -m "feat: implement two-tier Popover UI with project list and service panel"
```

---

## Task 13: 主窗口（侧边栏 + 日志面板）

**Files:**
- Modify: `SuperDev/UI/MainWindow/MainWindowView.swift`
- Create: `SuperDev/UI/MainWindow/SidebarView.swift`
- Create: `SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 1: 实现 SidebarView.swift**

```swift
import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @Binding var selectedProjectId: UUID?
    @Binding var selectedServiceId: UUID?  // nil = All Logs

    var body: some View {
        List(selection: $selectedServiceId) {
            ForEach(core.projects) { project in
                Section(project.name) {
                    // All Logs 行
                    Label("All Logs", systemImage: "doc.text.magnifyingglass")
                        .tag(Optional<UUID>.none)

                    ForEach(project.services) { service in
                        HStack {
                            Circle()
                                .fill(serviceStatusColor(service.status))
                                .frame(width: 7, height: 7)
                            Text(service.name)
                        }
                        .tag(Optional(service.id))
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .frame(minWidth: 160, maxWidth: 200)
    }

    private func serviceStatusColor(_ status: ServiceStatus) -> Color {
        switch status {
        case .stopped: return .gray
        case .starting: return .yellow
        case .running: return .green
        case .failed: return .red
        }
    }
}
```

- [ ] **Step 2: 实现 LogPanelView.swift**

```swift
import SwiftUI

struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let serviceId: UUID?  // nil = 显示所有

    @State private var keyword: String = ""
    @State private var enabledLevels: Set<LogLevel> = [.error, .warn, .info]

    private var filteredLogs: [LogEntry] {
        core.filteredLogs(serviceId: serviceId, levels: enabledLevels,
                          keyword: keyword.isEmpty ? nil : keyword)
    }

    private var stats: (total: Int, folded: Int, errors: Int, warns: Int) {
        let all = core.filteredLogs(serviceId: serviceId)
        let folded = all.filter { $0.repeatCount > 1 }.reduce(0) { $0 + $1.repeatCount - 1 }
        let errors = all.filter { $0.level == .error }.count
        let warns = all.filter { $0.level == .warn }.count
        return (all.count, folded, errors, warns)
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            logList
            Divider()
            statusBar
        }
    }

    // MARK: - Toolbar

    private var toolbar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
            TextField("关键词过滤", text: $keyword)
                .textFieldStyle(.plain)

            Divider().frame(height: 16)

            ForEach([LogLevel.error, .warn, .info], id: \.self) { level in
                levelToggle(level)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private func levelToggle(_ level: LogLevel) -> some View {
        Button {
            if enabledLevels.contains(level) { enabledLevels.remove(level) }
            else { enabledLevels.insert(level) }
        } label: {
            Text(level.rawValue)
                .font(.caption)
                .fontWeight(.medium)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(enabledLevels.contains(level) ? levelColor(level).opacity(0.15) : Color.clear)
                .foregroundColor(enabledLevels.contains(level) ? levelColor(level) : .secondary)
                .cornerRadius(4)
        }
        .buttonStyle(.plain)
    }

    // MARK: - Log List

    private var logList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(filteredLogs) { entry in
                        logRow(entry)
                            .id(entry.id)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
            }
            .background(Color(red: 0.12, green: 0.12, blue: 0.12))
            .onChange(of: filteredLogs.count) { _ in
                if let last = filteredLogs.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }
        }
    }

    private func logRow(_ entry: LogEntry) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Text(entry.timestamp.formatted(.dateTime.hour().minute().second()))
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Color(white: 0.45))

            Text("[\(entry.serviceName)]")
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(serviceColor(entry.serviceName))

            Text(entry.level.rawValue)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(levelColor(entry.level))
                .frame(width: 48, alignment: .leading)

            Text(entry.message)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Color(white: 0.85))
                .textSelection(.enabled)
                .lineLimit(nil)

            Spacer()

            if entry.repeatCount > 1 {
                Text("×\(entry.repeatCount)")
                    .font(.caption2)
                    .fontWeight(.bold)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 2)
                    .background(levelColor(entry.level))
                    .foregroundColor(.black)
                    .clipShape(Capsule())
            }
        }
        .padding(.vertical, 2)
        .padding(.horizontal, 4)
        .background(rowBackground(entry.level))
        .cornerRadius(2)
    }

    // MARK: - Status Bar

    private var statusBar: some View {
        HStack {
            let s = stats
            Text("共 \(s.total) 条 · 已折叠 \(s.folded) 条")
                .font(.caption)
                .foregroundColor(.secondary)
            Spacer()
            if s.errors > 0 {
                Text("\(s.errors)E").font(.caption).foregroundColor(.red)
            }
            if s.warns > 0 {
                Text("\(s.warns)W").font(.caption).foregroundColor(.yellow)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .background(Color(NSColor.controlBackgroundColor))
    }

    // MARK: - Helpers

    private func levelColor(_ level: LogLevel) -> Color {
        switch level {
        case .error: return .red
        case .warn:  return .yellow
        case .info:  return Color(red: 0.53, green: 0.93, blue: 0.53)
        case .debug: return .gray
        case .unknown: return .gray
        }
    }

    private func rowBackground(_ level: LogLevel) -> Color {
        switch level {
        case .error: return Color.red.opacity(0.12)
        case .warn:  return Color.yellow.opacity(0.08)
        default:     return Color.clear
        }
    }

    // 固定颜色池，按服务名哈希取色
    private let serviceColors: [Color] = [
        .blue, .pink, .purple, .orange, .cyan, .mint, .indigo, .teal
    ]
    private func serviceColor(_ name: String) -> Color {
        let idx = abs(name.hashValue) % serviceColors.count
        return serviceColors[idx]
    }
}
```

- [ ] **Step 3: 实现 MainWindowView.swift**

```swift
import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var selectedProjectId: UUID?
    @State private var selectedServiceId: UUID?

    var body: some View {
        NavigationSplitView {
            SidebarView(
                selectedProjectId: $selectedProjectId,
                selectedServiceId: $selectedServiceId
            )
        } detail: {
            LogPanelView(serviceId: selectedServiceId)
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
    }
}
```

- [ ] **Step 4: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 5: Commit**

```bash
git add SuperDev/UI/MainWindow/
git commit -m "feat: implement main window with sidebar and log panel"
```

---

## Task 14: 添加项目和设置界面

**Files:**
- Modify: `SuperDev/UI/Settings/SettingsView.swift`
- Create: `SuperDev/UI/Settings/AddProjectView.swift`

- [ ] **Step 1: 实现 AddProjectView.swift**

```swift
import SwiftUI

struct AddProjectView: View {
    @EnvironmentObject var core: AppCore
    @Environment(\.dismiss) var dismiss

    @State private var selectedPath: String = ""
    @State private var errorMessage: String?
    @State private var showImportOption = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("添加项目").font(.headline)

            HStack {
                Text(selectedPath.isEmpty ? "未选择目录" : selectedPath)
                    .foregroundColor(selectedPath.isEmpty ? .secondary : .primary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button("选择目录…") { selectDirectory() }
            }
            .padding(8)
            .background(Color(NSColor.controlBackgroundColor))
            .cornerRadius(6)

            if showImportOption {
                HStack {
                    Image(systemName: "info.circle").foregroundColor(.blue)
                    Text("检测到 .vscode/launch.json，是否导入配置？")
                        .font(.subheadline)
                    Spacer()
                    Button("导入") { importFromLaunchJson() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
                .padding(8)
                .background(Color.blue.opacity(0.08))
                .cornerRadius(6)
            }

            if let err = errorMessage {
                Text(err).foregroundColor(.red).font(.caption)
            }

            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("添加") { addProject() }
                    .buttonStyle(.borderedProminent)
                    .disabled(selectedPath.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func selectDirectory() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url {
            selectedPath = url.path
            errorMessage = nil
            checkForLaunchJson(at: url)
        }
    }

    private func checkForLaunchJson(at url: URL) {
        let launchJson = url.appendingPathComponent(".vscode/launch.json")
        let configYaml = url.appendingPathComponent(".superdev/config.yaml")
        showImportOption = FileManager.default.fileExists(atPath: launchJson.path)
            && !FileManager.default.fileExists(atPath: configYaml.path)
    }

    private func addProject() {
        do {
            try core.addProject(rootPath: selectedPath)
            dismiss()
        } catch {
            errorMessage = "无法加载配置：\(error.localizedDescription)\n请确认目录中有 .superdev/config.yaml"
        }
    }

    private func importFromLaunchJson() {
        do {
            try core.importFromLaunchJson(rootPath: selectedPath)
            dismiss()
        } catch {
            errorMessage = "导入失败：\(error.localizedDescription)"
        }
    }
}
```

- [ ] **Step 2: 实现 SettingsView.swift**

```swift
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var core: AppCore
    @State private var showAddProject = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // 项目列表
            List {
                ForEach(core.projects) { project in
                    HStack {
                        VStack(alignment: .leading) {
                            Text(project.name).fontWeight(.medium)
                            Text(project.rootPath)
                                .font(.caption)
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                        Spacer()
                        Button {
                            try? core.reloadConfig(for: project)
                        } label: {
                            Image(systemName: "arrow.clockwise")
                        }
                        .buttonStyle(.plain)
                        .help("重新加载配置")

                        Button(role: .destructive) {
                            core.removeProject(project)
                        } label: {
                            Image(systemName: "trash")
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.vertical, 4)
                }
            }

            Divider()

            HStack {
                Spacer()
                Button {
                    showAddProject = true
                } label: {
                    Label("添加项目", systemImage: "plus")
                }
                .buttonStyle(.borderedProminent)
                .padding(12)
            }
        }
        .frame(width: 480, height: 320)
        .sheet(isPresented: $showAddProject) {
            AddProjectView().environmentObject(core)
        }
    }
}
```

- [ ] **Step 3: 编译验证**

Xcode → ⌘B → Expected: Build Succeeded

- [ ] **Step 4: Commit**

```bash
git add SuperDev/UI/Settings/
git commit -m "feat: implement Settings and AddProject views"
```

---

## Task 15: 端到端冒烟测试

**目标**：在真机上走一遍核心路径，确认功能联通。

- [ ] **Step 1: 创建测试用配置**

```bash
mkdir -p /tmp/test-project/.superdev
cat > /tmp/test-project/.superdev/config.yaml << 'EOF'
name: TestProject
services:
  - name: server
    command: python3 -m http.server 9999
    working_dir: /tmp
    required: true
  - name: logger
    command: bash -c 'while true; do echo "$(date) INFO heartbeat"; sleep 1; done'
    working_dir: /tmp
    required: false
EOF
```

- [ ] **Step 2: 运行 App**

Xcode → Product → Run（⌘R）

- [ ] **Step 3: 验证菜单栏**

- [ ] 菜单栏出现 terminal 图标
- [ ] 点击图标弹出 Popover
- [ ] 一级列表为空（未添加项目）

- [ ] **Step 4: 添加测试项目**

- [ ] Popover 点击"添加项目" → 选择 `/tmp/test-project`
- [ ] 项目出现在一级列表中
- [ ] hover 到项目，展开二级面板
- [ ] server 在"必须启动"组，logger 在"可选"组

- [ ] **Step 5: 启动进程**

- [ ] 全选 → 点击"▶ 启动选中"
- [ ] server 状态变为"运行中"（绿色）
- [ ] logger 状态变为"运行中"（绿色）

- [ ] **Step 6: 验证日志**

- [ ] 点击"查看日志"打开主窗口
- [ ] 日志面板显示来自 server 和 logger 的输出
- [ ] logger 的 heartbeat 日志出现折叠（等待 ~5 条后出现 ×N 徽章）
- [ ] ERROR/WARN/INFO 过滤按钮可切换

- [ ] **Step 7: 停止进程**

- [ ] Popover 中点击 server 的 ■ 停止按钮
- [ ] server 状态变为"未启动"

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: superdev phase 1 complete — smoke test passed"
```

---

## Self-Review

**Spec coverage check:**

| 需求 | Task |
|------|------|
| macOS 菜单栏 App | Task 11 |
| Popover 两层交互 | Task 12 |
| 主窗口日志面板 | Task 13 |
| 进程管理：启停、分组 | Task 7, 10 |
| 日志聚合、Level 过滤 | Task 8, 13 |
| 日志关键词搜索 | Task 13 |
| 重复折叠 + ×N 徽章 | Task 8, 13 |
| 日志持久化 | Task 9 |
| 配置 .superdev/config.yaml | Task 3 |
| GUI 添加项目 | Task 14 |
| 导入 launch.json | Task 4 |
| 必须/可选分组 | Task 2, 12 |
| env_file 支持 | Task 6 |
| 全选/取消/反选 | Task 12 |
| 菜单栏图标状态 | Task 11 |

**所有需求均有对应 Task，无遗漏。**
