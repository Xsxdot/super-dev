// MainWindowView 持有面板布局状态并管理持久化。
//
// 职责：
//   - 维护 PanelLayout 状态树
//   - 在布局变更时写入 UserDefaults
//   - 渲染 SidebarView + PanelLayoutView
//
// 边界：
//   - 不持有服务选择状态（已移入各叶子面板）
//   - 不直接渲染 LogPanelView（由 PanelLayoutView 负责）
import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var layout: PanelLayout = Self.loadLayout()

    var body: some View {
        NavigationSplitView {
            SidebarView()
        } detail: {
            PanelLayoutView(layout: $layout)
                .onChange(of: layout) { _, newLayout in
                    Self.saveLayout(newLayout)
                    pruneOrphanBookmarks(layout: newLayout)
                }
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
    }

    // MARK: - Persistence

    private static let layoutKey = "superdev.panel_layout"

    private static func loadLayout() -> PanelLayout {
        guard let data = UserDefaults.standard.data(forKey: layoutKey),
              let decoded = try? JSONDecoder().decode(PanelLayout.self, from: data) else {
            return .leaf(id: UUID(), serviceId: nil)
        }
        return decoded
    }

    private static func saveLayout(_ layout: PanelLayout) {
        if let data = try? JSONEncoder().encode(layout) {
            UserDefaults.standard.set(data, forKey: layoutKey)
        }
    }

    // 当面板被关闭时，清理对应的孤立书签
    private func pruneOrphanBookmarks(layout: PanelLayout) {
        let activeIds = Set(layout.allLeafIds)
        for panelId in core.bookmarks.keys where !activeIds.contains(panelId) {
            core.clearBookmark(panelId: panelId)
        }
    }
}
