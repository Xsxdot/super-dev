import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var selectedServiceId: UUID?

    private var selectedProject: Project? {
        if let sid = selectedServiceId {
            return core.projects.first { $0.services.contains { $0.id == sid } }
        }
        return core.projects.first
    }

    var body: some View {
        NavigationSplitView {
            SidebarView(
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
            autoSelectFirstServiceIfNeeded()
        }
        .onChange(of: core.projects) { _, _ in
            let allServiceIds = core.projects.flatMap(\.services).map(\.id)
            if let sel = selectedServiceId, !allServiceIds.contains(sel) {
                selectedServiceId = nil
            }
            autoSelectFirstServiceIfNeeded()
        }
    }

    private func autoSelectFirstServiceIfNeeded() {
        if selectedServiceId == nil {
            selectedServiceId = core.projects.first?.services.first?.id
        }
    }
}
