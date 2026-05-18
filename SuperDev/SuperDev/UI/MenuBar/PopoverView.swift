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

    // MARK: - Project list (left panel)

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
        .overlay(alignment: .leading) {
            if hoveredProjectId == project.id {
                Rectangle()
                    .frame(width: 3)
                    .foregroundColor(.accentColor)
            }
        }
        .contentShape(Rectangle())
        .onHover { isHovered in
            if isHovered {
                hoveredProjectId = project.id
                selectedServiceIds = Set(project.services.filter { $0.required }.map { $0.id })
            }
        }
    }

    // MARK: - Service panel (right panel)

    private func servicePanel(for project: Project) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
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

            // Select all toolbar
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

            // Service rows
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

            // Footer
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
    }

    private func openAddProject() {
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
