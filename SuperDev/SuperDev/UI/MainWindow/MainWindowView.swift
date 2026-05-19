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
    @State private var layout: PanelLayout = .leaf(id: UUID(), serviceId: nil)

    var body: some View {
        NavigationSplitView {
            SidebarView()
        } detail: {
            PanelLayoutView(layout: $layout)
                .onChange(of: layout) { _, newLayout in
                    pruneOrphanBookmarks(layout: newLayout)
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
}
