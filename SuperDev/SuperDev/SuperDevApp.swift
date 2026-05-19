import SwiftUI
import AppKit

// AppCore 单例，让 AppDelegate 和 SwiftUI App 共享同一实例
@MainActor
final class AppState {
    static let shared = AppState()
    let core = AppCore()
    var menuBarManager: MenuBarManager?

    private init() {}

    func setupMenuBar() {
        guard menuBarManager == nil else { return }
        menuBarManager = MenuBarManager(core: core)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // 无窗口时仅菜单栏，不占用 Dock
        NSApp.setActivationPolicy(.accessory)
        Task { @MainActor in
            AppState.shared.setupMenuBar()
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        Task { @MainActor in
            AppState.shared.menuBarManager?.openMainWindow()
        }
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return false
    }
}

@main
struct SuperDevApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        // 占位 scene，所有窗口由 MenuBarManager 直接管理。
        // 不用 Settings {} 以避免系统应用菜单出现空白的 "Settings..." 入口。
        WindowGroup {
            EmptyView()
        }
        .defaultSize(width: 0, height: 0)
        .commandsRemoved()
    }
}
