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
