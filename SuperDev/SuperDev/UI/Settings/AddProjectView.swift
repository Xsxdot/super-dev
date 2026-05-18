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
