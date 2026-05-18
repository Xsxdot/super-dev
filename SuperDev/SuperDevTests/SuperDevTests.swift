//
//  SuperDevTests.swift
//  SuperDevTests
//
//  Created by 徐世鑫 on 2026/5/18.
//

import XCTest
@testable import SuperDev

@MainActor
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
            if case ConfigLoader.ConfigError.fileNotFound = error {
                // correct
            } else {
                XCTFail("Expected .fileNotFound, got \(error)")
            }
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

@MainActor
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
        XCTAssertThrowsError(try importer.importServices()) { error in
            if case LaunchJsonImporter.ImportError.fileNotFound = error {
                // correct
            } else {
                XCTFail("Expected .fileNotFound, got \(error)")
            }
        }
    }

    func test_import_throwsInvalidFormatForBadJson() throws {
        let vscodeDir = tempDir.appendingPathComponent(".vscode")
        try FileManager.default.createDirectory(at: vscodeDir, withIntermediateDirectories: true)
        // Valid JSON but missing "configurations" key
        let json = #"{"version": "0.2.0"}"#
        try json.write(to: vscodeDir.appendingPathComponent("launch.json"), atomically: true, encoding: .utf8)

        let importer = LaunchJsonImporter(rootPath: tempDir.path)
        XCTAssertThrowsError(try importer.importServices()) { error in
            if case LaunchJsonImporter.ImportError.invalidFormat = error {
                // correct
            } else {
                XCTFail("Expected .invalidFormat, got \(error)")
            }
        }
    }
}

final class ProcessRunnerTests: XCTestCase {

    func test_run_capturesStdout() async throws {
        var received: [String] = []
        let lock = NSLock()
        let tmpDir = FileManager.default.temporaryDirectory
        let runner = ProcessRunner(
            command: "echo hello",
            workingDir: tmpDir.path,
            env: [:]
        ) { line in
            lock.lock()
            received.append(line)
            lock.unlock()
        }
        try runner.start()
        try await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertTrue(received.contains(where: { $0.contains("hello") }))
    }

    func test_run_canBeStopped() async throws {
        let tmpDir = FileManager.default.temporaryDirectory
        let runner = ProcessRunner(
            command: "sleep 60",
            workingDir: tmpDir.path,
            env: [:]
        ) { _ in }
        try runner.start()
        XCTAssertTrue(runner.isRunning)
        runner.stop()
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertFalse(runner.isRunning)
    }

    func test_run_mergesEnvFile() async throws {
        let envContent = "TEST_KEY=from_file\n"
        let tmpDir = FileManager.default.temporaryDirectory
        let envFileURL = tmpDir.appendingPathComponent("superdev_test_\(UUID().uuidString).env")
        try envContent.write(to: envFileURL, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: envFileURL) }

        var received: [String] = []
        let lock = NSLock()
        let runner = ProcessRunner(
            command: "sh -c 'echo $TEST_KEY'",
            workingDir: tmpDir.path,
            env: [:],
            envFile: envFileURL.path
        ) { line in
            lock.lock()
            received.append(line)
            lock.unlock()
        }
        try runner.start()
        try await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertTrue(received.contains(where: { $0.contains("from_file") }))
    }
}
