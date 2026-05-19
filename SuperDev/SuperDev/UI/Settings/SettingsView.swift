import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var core: AppCore

    private enum SettingsTab: String, CaseIterable, Identifiable {
        case general = "通用"
        case projects = "项目"
        case integrations = "集成"

        var id: String { rawValue }

        var icon: String {
            switch self {
            case .general: return "gearshape"
            case .projects: return "folder"
            case .integrations: return "puzzlepiece"
            }
        }
    }

    @State private var selectedTab: SettingsTab = .general
    @State private var showAddProject = false
    @State private var retentionDays: Int = AppCore.defaultRetentionDays

    var body: some View {
        NavigationSplitView {
            List(SettingsTab.allCases, selection: $selectedTab) { tab in
                Label(tab.rawValue, systemImage: tab.icon)
                    .tag(tab)
            }
            .listStyle(.sidebar)
            .navigationSplitViewColumnWidth(min: 140, ideal: 150, max: 160)
        } detail: {
            Group {
                switch selectedTab {
                case .general:
                    generalPane
                case .projects:
                    projectsPane
                case .integrations:
                    integrationsPane
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .onAppear {
            retentionDays = core.logRetentionDays
        }
        .frame(width: 600, height: 420)
        .sheet(isPresented: $showAddProject) {
            AddProjectView().environmentObject(core)
        }
    }

    private var generalPane: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("日志保留天数")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(Theme.textPrimary)
                    Text("超过此天数的日志将在启动时自动删除")
                        .font(.caption)
                        .foregroundColor(Theme.textSecondary)
                }
                Spacer()
                Stepper("\(retentionDays) 天", value: $retentionDays, in: 1...90)
                    .onChange(of: retentionDays) { _, newValue in
                        core.logRetentionDays = newValue
                    }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 16)
            .background(Theme.bgElevated)
            .cornerRadius(8)
            .padding(16)
            Spacer()
        }
        .background(Theme.bgPrimary)
    }

    private var projectsPane: some View {
        VStack(spacing: 0) {
            HStack {
                Text("项目")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Theme.textPrimary)
                Spacer()
                Button {
                    showAddProject = true
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "plus")
                        Text("添加项目")
                    }
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(.white)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(Theme.accent)
                    .cornerRadius(6)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)

            ScrollView {
                VStack(spacing: 1) {
                    ForEach(core.projects) { project in
                        projectRow(project)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 8)
            }
            Spacer(minLength: 0)
        }
        .background(Theme.bgPrimary)
    }

    private var integrationsPane: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("MCP 集成")
                .font(.system(size: 12, weight: .semibold))
                .foregroundColor(Theme.textPrimary)

            VStack(alignment: .leading, spacing: 4) {
                Text("Control socket")
                    .font(.caption)
                    .foregroundColor(Theme.textSecondary)
                Text(ControlSocketServer.socketPath)
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(Theme.textTertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            Button {
                copyMCPConfig()
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "doc.on.clipboard")
                    Text("复制 Claude Code 配置")
                }
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(Theme.accent)
            }
            .buttonStyle(.plain)
            .help("复制后粘贴到 .claude/settings.json 的 mcpServers 字段")

            Spacer()
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Theme.bgPrimary)
    }

    private func projectRow(_ project: Project) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            // 项目标题行
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(project.name)
                        .fontWeight(.medium)
                        .foregroundColor(Theme.textPrimary)
                    Text(project.rootPath)
                        .font(.caption)
                        .foregroundColor(Theme.textSecondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer()
                Button {
                    try? core.reloadConfig(for: project)
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .foregroundColor(Theme.textSecondary)
                }
                .buttonStyle(.plain)
                .help("重新加载配置")

                Button(role: .destructive) {
                    core.removeProject(project)
                } label: {
                    Image(systemName: "trash")
                        .foregroundColor(Theme.statusFailed)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)

            // 服务可见性列表
            if !project.services.isEmpty {
                Divider()
                    .padding(.horizontal, 8)
                VStack(spacing: 0) {
                    ForEach(project.services) { service in
                        serviceVisibilityRow(service)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.bottom, 8)
            }
        }
        .background(Theme.bgElevated)
        .cornerRadius(6)
    }

    private func serviceVisibilityRow(_ service: Service) -> some View {
        let isHidden = core.isHidden(service)
        return HStack(spacing: 8) {
            Image(systemName: service.required ? "lock.fill" : "circle.fill")
                .font(.system(size: 7))
                .foregroundColor(service.required ? Theme.accent : Theme.textTertiary)
            Text(service.name)
                .font(.system(size: 11))
                .foregroundColor(isHidden ? Theme.textTertiary : Theme.textPrimary)
            Spacer()
            Button {
                core.toggleHidden(service)
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: isHidden ? "eye.slash" : "eye")
                        .font(.system(size: 9))
                    Text(isHidden ? "已隐藏" : "显示")
                        .font(.system(size: 9))
                }
                .foregroundColor(isHidden ? Theme.textTertiary : Theme.textSecondary)
            }
            .buttonStyle(.plain)
            .help(isHidden ? "点击以在菜单栏中显示此服务" : "点击以在菜单栏中隐藏此服务")
        }
        .padding(.vertical, 4)
    }

    private func copyMCPConfig() {
        let binaryPath: String
        if let bundlePath = Bundle.main.executableURL?
            .deletingLastPathComponent()
            .appendingPathComponent("superdev-mcp").path,
           FileManager.default.fileExists(atPath: bundlePath) {
            binaryPath = bundlePath
        } else {
            binaryPath = "/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"
        }

        let config = """
        {
          "mcpServers": {
            "superdev": {
              "command": "\(binaryPath)"
            }
          }
        }
        """
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(config, forType: .string)
    }
}
