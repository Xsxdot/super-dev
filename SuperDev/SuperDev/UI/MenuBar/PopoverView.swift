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
        .frame(minWidth: hoveredProject == nil ? 170 : 430, minHeight: 300)
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

            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(height: 1)

            // 项目列表
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(core.projects) { project in
                        projectSection(project)
                    }
                }
            }

            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(height: 1)

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

    @ViewBuilder
    private func projectSection(_ project: Project) -> some View {
        let filtered = filteredServices(of: project)
        if !filtered.isEmpty || searchText.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
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
            }
        }
    }

    private func filteredServices(of project: Project) -> [Service] {
        guard !searchText.isEmpty else { return project.services }
        return project.services.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }

    private func leftServiceRow(_ service: Service, in project: Project) -> some View {
        let isProjectHovered = hoveredProjectId == project.id

        return HStack(spacing: 7) {
            Circle()
                .fill(serviceStatusColor(service.status))
                .shadow(color: serviceStatusColor(service.status).opacity(
                    service.status == .running || service.status == .starting ? 0.6 : 0
                ), radius: 3)
                .frame(width: 6, height: 6)

            Text(service.name)
                .font(.system(size: 11))
                .foregroundColor(isProjectHovered ? Theme.textPrimary : Theme.textSecondary)
                .lineLimit(1)

            Spacer()

            Text(statusLabel(service.status))
                .font(.system(size: 9))
                .foregroundColor(serviceStatusColor(service.status))
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(isProjectHovered ? Theme.bgElevated : Color.clear)
        .overlay(alignment: .leading) {
            if isProjectHovered {
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
        VStack(spacing: 0) {
            servicePanelHeader(for: project)
            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(height: 1)
            servicePanelToolbar(for: project)
            Rectangle()
                .fill(Theme.bgElevated)
                .frame(height: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    let required = project.services.filter { $0.required }
                    let optional = project.services.filter { !$0.required }
                    if !required.isEmpty {
                        serviceGroupLabel("必须启动")
                        ForEach(required) { service in
                            serviceRow(service, in: project)
                        }
                    }
                    if !optional.isEmpty {
                        serviceGroupLabel("可选")
                        ForEach(optional) { service in
                            serviceRow(service, in: project)
                        }
                    }
                }
            }
            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(height: 1)
            servicePanelFooter()
        }
        .frame(width: 260)
        .background(Theme.bgSecondary)
    }

    private func servicePanelHeader(for project: Project) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .center) {
                Text(project.name)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Theme.textPrimary)
                Spacer()
                Button("全停") { core.stopAll(project: project) }
                    .buttonStyle(.plain)
                    .font(.system(size: 10))
                    .foregroundColor(Theme.textSecondary)
                    .padding(.horizontal, 9)
                    .padding(.vertical, 3)
                    .background(Theme.bgElevated)
                    .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.borderSecondary, lineWidth: 1))
                    .cornerRadius(5)
                Button("▶ 启动选中") {
                    let toStart = project.services.filter { selectedServiceIds.contains($0.id) }
                    core.startSelected(services: toStart, in: project)
                }
                .buttonStyle(.plain)
                .font(.system(size: 10, weight: .medium))
                .foregroundColor(.white)
                .padding(.horizontal, 9)
                .padding(.vertical, 3)
                .background(Theme.accent)
                .cornerRadius(5)
            }
            HStack(spacing: 5) {
                let runningCount  = project.services.filter { $0.status == .running }.count
                let startingCount = project.services.filter { $0.status == .starting }.count
                let stoppedCount  = project.services.filter { $0.status == .stopped || $0.status == .failed }.count
                if runningCount > 0  { statusBadge("● \(runningCount) 运行中", color: Theme.statusRunning) }
                if startingCount > 0 { statusBadge("● \(startingCount) 启动中", color: Theme.statusStarting) }
                if stoppedCount > 0  { statusBadge("● \(stoppedCount) 停止", color: Theme.statusStopped) }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private func statusBadge(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.system(size: 9))
            .foregroundColor(color)
            .padding(.horizontal, 7)
            .padding(.vertical, 1)
            .background(color.opacity(0.1))
            .overlay(RoundedRectangle(cornerRadius: 4).stroke(color.opacity(0.2), lineWidth: 1))
            .cornerRadius(4)
    }

    private func servicePanelToolbar(for project: Project) -> some View {
        HStack(spacing: 10) {
            Button {
                let allSelected = project.services.allSatisfy { selectedServiceIds.contains($0.id) }
                if allSelected {
                    selectedServiceIds.removeAll()
                } else {
                    project.services.forEach { selectedServiceIds.insert($0.id) }
                }
            } label: {
                let allSelected = project.services.allSatisfy { selectedServiceIds.contains($0.id) }
                let someSelected = project.services.contains { selectedServiceIds.contains($0.id) }
                ZStack {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(allSelected ? Theme.accent : Color.clear)
                        .overlay(
                            RoundedRectangle(cornerRadius: 2)
                                .stroke(allSelected || someSelected ? Theme.accent : Theme.borderSecondary, lineWidth: 1)
                        )
                        .frame(width: 13, height: 13)
                    if allSelected {
                        RoundedRectangle(cornerRadius: 1.5)
                            .fill(Color.white)
                            .frame(width: 8, height: 8)
                    } else if someSelected {
                        Rectangle()
                            .fill(Theme.accent)
                            .frame(width: 8, height: 1.5)
                            .cornerRadius(1)
                    }
                }
            }
            .buttonStyle(.plain)
            Text("全选")
                .font(.system(size: 10))
                .foregroundColor(Theme.textSecondary)
            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(width: 1, height: 12)
            Button("反选") { toggleInvert(for: project) }
                .buttonStyle(.plain)
                .font(.system(size: 10))
                .foregroundColor(Theme.textSecondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
        .background(Theme.bgToolbar)
    }

    private func serviceGroupLabel(_ title: String) -> some View {
        Text(title)
            .font(.system(size: 9, weight: .medium))
            .foregroundColor(Theme.textTertiary)
            .kerning(0.6)
            .textCase(.uppercase)
            .padding(.horizontal, 12)
            .padding(.top, 8)
            .padding(.bottom, 3)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func serviceRow(_ service: Service, in project: Project) -> some View {
        HStack(spacing: 8) {
            // 自绘 checkbox
            Button {
                if selectedServiceIds.contains(service.id) {
                    selectedServiceIds.remove(service.id)
                } else {
                    selectedServiceIds.insert(service.id)
                }
            } label: {
                let checked = selectedServiceIds.contains(service.id)
                ZStack {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(checked ? Theme.accent : Color.clear)
                        .overlay(
                            RoundedRectangle(cornerRadius: 2)
                                .stroke(checked ? Theme.accent : Theme.borderSecondary, lineWidth: 1)
                        )
                        .frame(width: 13, height: 13)
                    if checked {
                        RoundedRectangle(cornerRadius: 1.5)
                            .fill(Color.white)
                            .frame(width: 8, height: 8)
                    }
                }
            }
            .buttonStyle(.plain)

            // 状态点（运行/启动中加 glow）
            let statusColor = serviceStatusColor(service.status)
            let hasGlow = service.status == .running || service.status == .starting
            Circle()
                .fill(statusColor)
                .shadow(color: hasGlow ? statusColor.opacity(0.6) : .clear, radius: 3)
                .frame(width: 7, height: 7)

            Text(service.name)
                .font(.system(size: 11))
                .foregroundColor(service.status == .stopped ? Theme.textTertiary : Theme.textPrimary)
                .lineLimit(1)

            Spacer()

            Text(statusLabel(service.status))
                .font(.system(size: 9))
                .foregroundColor(serviceStatusColor(service.status))
                .help(failureTooltip(for: service))

            // 启停按钮（18×18 圆角方块）
            Button {
                if service.status.isActive {
                    core.stop(service, in: project)
                } else {
                    core.start(service, in: project)
                }
            } label: {
                let isActive = service.status.isActive
                let btnColor = isActive ? serviceStatusColor(service.status) : Theme.bgElevated
                ZStack {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(btnColor)
                        .overlay(
                            RoundedRectangle(cornerRadius: 3)
                                .stroke(isActive ? btnColor : Theme.borderSecondary, lineWidth: 1)
                        )
                        .frame(width: 18, height: 18)
                    Image(systemName: isActive ? "stop.fill" : "play.fill")
                        .font(.system(size: 7, weight: .bold))
                        .foregroundColor(isActive ? .black : Theme.textSecondary)
                }
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    private func servicePanelFooter() -> some View {
        HStack {
            Spacer()
            Button {
                openMainWindow()
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "text.alignleft")
                        .font(.system(size: 10))
                    Text("查看日志")
                        .font(.system(size: 10))
                }
                .foregroundColor(Theme.accent)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    // MARK: - Helpers

    private var hoveredProject: Project? {
        guard let id = hoveredProjectId else { return nil }
        return core.projects.first { $0.id == id }
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

    private func failureTooltip(for service: Service) -> String {
        guard service.status == .failed,
              let entry = core.lastErrorLog(for: service.id) else { return "" }
        let msg = entry.message
        return msg.count > 120 ? String(msg.prefix(120)) + "…" : msg
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
