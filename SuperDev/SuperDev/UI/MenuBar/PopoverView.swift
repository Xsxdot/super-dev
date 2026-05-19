import SwiftUI

struct PopoverView: View {
    @EnvironmentObject var core: AppCore
    @EnvironmentObject var menuBarManager: MenuBarManager
    @State private var hoveredProjectId: UUID?
    @State private var selectedServiceIds: Set<UUID> = []
    @State private var searchText: String = ""
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
        VStack(spacing: 0) {
            // 搜索栏
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 10))
                    .foregroundColor(Theme.textTertiary)
                TextField("搜索服务…", text: $searchText)
                    .textFieldStyle(.plain)
                    .font(.system(size: 10))
                    .foregroundColor(Theme.textSecondary)
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 5)
            .background(Theme.bgElevated)
            .overlay(
                RoundedRectangle(cornerRadius: 6)
                    .stroke(Theme.borderSecondary, lineWidth: 1)
            )
            .cornerRadius(6)
            .padding(.horizontal, 10)
            .padding(.vertical, 10)

            Divider().background(Theme.borderPrimary)

            // 项目列表
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(core.projects) { project in
                        projectSection(project)
                    }
                }
            }

            Divider().background(Theme.borderPrimary)

            // 添加项目
            Button {
                openAddProject()
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: "plus")
                        .font(.system(size: 11))
                    Text("添加项目")
                        .font(.system(size: 10))
                }
                .foregroundColor(Theme.accent)
            }
            .buttonStyle(.plain)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(width: 170)
        .background(Theme.bgPrimary)
    }

    private func projectSection(_ project: Project) -> some View {
        let filtered = filteredServices(of: project)
        guard !filtered.isEmpty || searchText.isEmpty else { return AnyView(EmptyView()) }

        return AnyView(VStack(alignment: .leading, spacing: 0) {
            // 项目 label
            HStack {
                Text(project.name.uppercased())
                    .font(.system(size: 9, weight: .medium))
                    .foregroundColor(Theme.textTertiary)
                    .kerning(0.8)
                Spacer()
                Circle()
                    .fill(projectStatusColor(project.overallStatus))
                    .shadow(color: projectStatusColor(project.overallStatus).opacity(0.6),
                            radius: 3)
                    .frame(width: 6, height: 6)
            }
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .padding(.bottom, 3)

            // 服务行
            ForEach(filtered) { service in
                leftServiceRow(service, in: project)
            }
        })
    }

    private func filteredServices(of project: Project) -> [Service] {
        guard !searchText.isEmpty else { return project.services }
        return project.services.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }

    private func leftServiceRow(_ service: Service, in project: Project) -> some View {
        let isSelected = hoveredProjectId == project.id

        return HStack(spacing: 7) {
            Circle()
                .fill(serviceStatusColor(service.status))
                .shadow(color: serviceStatusColor(service.status).opacity(
                    service.status == .running || service.status == .starting ? 0.6 : 0
                ), radius: 3)
                .frame(width: 6, height: 6)

            Text(service.name)
                .font(.system(size: 11))
                .foregroundColor(isSelected ? Theme.textPrimary : Theme.textSecondary)
                .lineLimit(1)

            Spacer()

            Text(statusLabel(service.status))
                .font(.system(size: 9))
                .foregroundColor(serviceStatusColor(service.status))
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(isSelected ? Theme.bgElevated : Color.clear)
        .overlay(alignment: .leading) {
            if isSelected {
                Rectangle()
                    .frame(width: 2)
                    .foregroundColor(Theme.accent)
            }
        }
        .contentShape(Rectangle())
        .onHover { hovered in
            if hovered {
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
        menuBarManager.openMainWindow()
    }

    private func openAddProject() {
        menuBarManager.openSettingsWindow()
    }

    private func serviceStatusColor(_ status: ServiceStatus) -> Color {
        switch status {
        case .stopped:  return Theme.statusStopped
        case .starting: return Theme.statusStarting
        case .running:  return Theme.statusRunning
        case .failed:   return Theme.statusFailed
        }
    }

    private func projectStatusColor(_ status: ProjectStatus) -> Color {
        switch status {
        case .stopped:  return Theme.statusStopped
        case .starting: return Theme.statusStarting
        case .running:  return Theme.statusRunning
        case .failed:   return Theme.statusFailed
        }
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
