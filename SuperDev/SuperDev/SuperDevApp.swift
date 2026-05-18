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
        NSApp.setActivationPolicy(.accessory)
        Task { @MainActor in
            AppState.shared.setupMenuBar()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return false
    }
}

@main
struct SuperDevApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        // 必须有一个 scene；全部窗口由 MenuBarManager 直接管理
        Settings {
            EmptyView()
        }
    }
}
