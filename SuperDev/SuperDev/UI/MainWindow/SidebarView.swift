import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @State private var hoveredServiceId: UUID?

    var body: some View {
        List {
            ForEach(core.projects) { project in
                Section(project.name) {
                    ForEach(project.services) { service in
                        serviceRow(service: service, project: project)
                            .draggable(service.id.uuidString)
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .frame(minWidth: 160, maxWidth: 200)
    }

    @ViewBuilder
    private func serviceRow(service: Service, project: Project) -> some View {
        let status = serviceStatus(service.id, in: project.id) ?? service.status
        let isHovered = hoveredServiceId == service.id

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
        .onHover { hovering in
            if hovering {
                hoveredServiceId = service.id
            } else if hoveredServiceId == service.id {
                hoveredServiceId = nil
            }
        }
        .animation(.easeOut(duration: 0.18), value: hoveredServiceId)
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
