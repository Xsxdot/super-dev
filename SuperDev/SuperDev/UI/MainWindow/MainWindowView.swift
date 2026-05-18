import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var selectedProjectId: UUID?
    @State private var selectedServiceId: UUID?

    var body: some View {
        NavigationSplitView {
            SidebarView(
                selectedProjectId: $selectedProjectId,
                selectedServiceId: $selectedServiceId
            )
        } detail: {
            LogPanelView(serviceId: selectedServiceId)
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
    }
}
