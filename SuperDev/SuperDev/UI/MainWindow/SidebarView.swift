import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @Binding var selectedProjectId: UUID?
    @Binding var selectedServiceId: UUID?  // nil = All Logs

    var body: some View {
        List(selection: $selectedServiceId) {
            ForEach(core.projects) { project in
                Section(project.name) {
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
