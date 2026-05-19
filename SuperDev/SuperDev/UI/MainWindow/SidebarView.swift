import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @Binding var selectedServiceId: UUID?

    var body: some View {
        List(selection: $selectedServiceId) {
            ForEach(core.projects) { project in
                Section(project.name) {
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
