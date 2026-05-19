// MainWindowView 持有面板布局状态。
//
// 职责：
//   - 维护 PanelLayout 状态树（每次启动重置为单面板）
//   - 渲染 SidebarView + PanelLayoutView
//
// 边界：
//   - 不持有服务选择状态（已移入各叶子面板）
//   - 不直接渲染 LogPanelView（由 PanelLayoutView 负责）
import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var layout: PanelLayout = .leaf(id: UUID(), serviceId: nil, projectId: nil)
    @State private var focusedPanelId: UUID?

    var body: some View {
        NavigationSplitView {
            SidebarView(layout: $layout, focusedPanelId: $focusedPanelId)
        } detail: {
            PanelLayoutView(layout: $layout, focusedPanelId: $focusedPanelId)
                .onChange(of: layout) { _, newLayout in
                    pruneOrphanBookmarks(layout: newLayout)
                    ensureFocusedPanel(in: newLayout)
                }
                .onAppear {
                    ensureFocusedPanel(in: layout)
                }
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
    }

    // 当面板被关闭时，清理对应的孤立书签
    private func pruneOrphanBookmarks(layout: PanelLayout) {
        let activeIds = Set(layout.allLeafIds)
        for panelId in core.bookmarks.keys where !activeIds.contains(panelId) {
            core.clearBookmark(panelId: panelId)
        }
    }

    private func ensureFocusedPanel(in layout: PanelLayout) {
        let ids = layout.allLeafIds
        guard !ids.isEmpty else { return }
        if let focused = focusedPanelId, ids.contains(focused) { return }
        focusedPanelId = ids.first
    }
}
