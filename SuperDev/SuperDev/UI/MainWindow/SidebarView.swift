import AppKit
import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout
    @Binding var focusedPanelId: UUID?

    @State private var hoveredServiceId: UUID?

    var body: some View {
        // ScrollView 替代 List：macOS 上 List 会吞掉 onDrag，导致侧边栏拖不动。
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 2) {
                ForEach(core.projects) { project in
                    projectHeader(project: project)
                        .padding(.horizontal, 8)
                        .padding(.top, 10)
                    ForEach(project.services) { service in
                        serviceRow(service: service, project: project)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 2)
                    }
                }
            }
            .padding(.bottom, 8)
        }
        .frame(minWidth: 160, maxWidth: 200)
    }

    @ViewBuilder
    private func projectHeader(project: Project) -> some View {
        let selected = isProjectSelected(project)
        Text(project.name)
            .font(.system(size: 12, weight: .semibold))
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .padding(.vertical, 2)
            .background(selected ? Theme.accent.opacity(0.15) : Color.clear)
            .cornerRadius(4)
            .onTapGesture {
                selectProject(project)
            }
    }

    @ViewBuilder
    private func serviceRow(service: Service, project: Project) -> some View {
        let status = serviceStatus(service.id, in: project.id) ?? service.status
        let isHovered = hoveredServiceId == service.id
        let selected = isServiceSelected(service)

        ZStack(alignment: .leading) {
            HStack(spacing: 8) {
                Circle()
                    .fill(serviceStatusColor(status))
                    .frame(width: 7, height: 7)
                Text(service.name)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            if isHovered {
                serviceControlOverlay(service: service, project: project, status: status)
                    .transition(
                        .asymmetric(
                            insertion: .move(edge: .trailing).combined(with: .opacity),
                            removal: .move(edge: .trailing).combined(with: .opacity)
                        )
                    )
                    .zIndex(1)
            }
        }
        .contentShape(Rectangle())
        .background(selected ? Theme.accent.opacity(0.12) : Color.clear)
        .cornerRadius(4)
        .simultaneousGesture(TapGesture().onEnded {
            selectService(service, in: project)
        })
        .onDrag {
            NSItemProvider(object: service.id.uuidString as NSString)
        }
        .onHover { hovering in
            MainRunLoop.deferred {
                if hovering {
                    hoveredServiceId = service.id
                } else if hoveredServiceId == service.id {
                    hoveredServiceId = nil
                }
            }
        }
        .animation(.easeOut(duration: 0.18), value: hoveredServiceId)
    }

    private func targetPanelId() -> UUID? {
        if let focused = focusedPanelId, layout.allLeafIds.contains(focused) {
            return focused
        }
        return layout.allLeafIds.first
    }

    private func selectService(_ service: Service, in project: Project) {
        guard let panelId = targetPanelId() else { return }
        MainRunLoop.deferred {
            layout.replaceScope(panelId: panelId, serviceId: service.id, projectId: project.id)
            focusedPanelId = panelId
            core.returnToLiveLogs()
        }
    }

    private func selectProject(_ project: Project) {
        guard let panelId = targetPanelId() else { return }
        MainRunLoop.deferred {
            layout.replaceScope(panelId: panelId, serviceId: nil, projectId: project.id)
            focusedPanelId = panelId
            core.returnToLiveLogs()
        }
    }

    private func isServiceSelected(_ service: Service) -> Bool {
        guard let panelId = focusedPanelId ?? layout.allLeafIds.first,
              let scope = layout.leafScope(panelId: panelId) else { return false }
        return scope.serviceId == service.id
    }

    private func isProjectSelected(_ project: Project) -> Bool {
        guard let panelId = focusedPanelId ?? layout.allLeafIds.first,
              let scope = layout.leafScope(panelId: panelId) else { return false }
        return scope.serviceId == nil && scope.projectId == project.id
    }

    @ViewBuilder
    private func serviceControlOverlay(service: Service, project: Project, status: ServiceStatus) -> some View {
        HStack(spacing: 4) {
            if status.isActive {
                controlButton(icon: "arrow.clockwise", help: "重启") {
                    core.restart(service, in: project)
                }
            }

            if status.isActive {
                controlButton(icon: "stop.fill", help: "停止", filled: true, tint: serviceStatusColor(status)) {
                    core.stop(service, in: project)
                }
            } else {
                controlButton(icon: "play.fill", help: "启动") {
                    core.start(service, in: project)
                }
            }
        }
        .padding(.leading, 24)
        .padding(.trailing, 4)
        .padding(.vertical, 2)
        .background(
            LinearGradient(
                colors: [.clear, Theme.bgElevated.opacity(0.92), Theme.bgElevated.opacity(0.96)],
                startPoint: .leading,
                endPoint: .trailing
            )
        )
        .frame(maxWidth: .infinity, alignment: .trailing)
    }

    private func controlButton(
        icon: String,
        help: String,
        filled: Bool = false,
        tint: Color? = nil,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            let btnColor = filled ? (tint ?? Theme.bgElevated) : Theme.bgElevated
            ZStack {
                RoundedRectangle(cornerRadius: 3)
                    .fill(btnColor)
                    .overlay(
                        RoundedRectangle(cornerRadius: 3)
                            .stroke(filled ? btnColor : Theme.borderSecondary, lineWidth: 1)
                    )
                    .frame(width: 18, height: 18)
                Image(systemName: icon)
                    .font(.system(size: 7, weight: .bold))
                    .foregroundColor(filled ? .black : Theme.textSecondary)
            }
        }
        .buttonStyle(.plain)
        .help(help)
    }

    private func serviceStatus(_ serviceId: UUID, in projectId: UUID) -> ServiceStatus? {
        core.projects
            .first(where: { $0.id == projectId })?
            .services
            .first(where: { $0.id == serviceId })?
            .status
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
