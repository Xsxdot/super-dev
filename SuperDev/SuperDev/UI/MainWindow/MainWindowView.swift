import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var selectedProjectId: UUID?
    @State private var selectedServiceId: UUID?

    private var selectedProject: Project? {
        if let id = selectedProjectId {
            return core.projects.first { $0.id == id }
        }
        if let sid = selectedServiceId {
            return core.projects.first { $0.services.contains { $0.id == sid } }
        }
        return core.projects.first
    }

    var body: some View {
        NavigationSplitView {
            SidebarView(
                selectedProjectId: $selectedProjectId,
                selectedServiceId: $selectedServiceId
            )
        } detail: {
            LogPanelView(
                serviceId: selectedServiceId,
                project: selectedProject
            )
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
        .onAppear {
            if selectedServiceId == nil {
                selectedServiceId = core.projects.first?.services.first?.id
            }
        }
    }
}
