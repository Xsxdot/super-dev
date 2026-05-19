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
        .commands {
            // 隐藏不需要的系统菜单项；保留 Edit（剪切/拷贝/粘贴）等默认命令。
            CommandGroup(replacing: .appSettings) { }
            CommandGroup(replacing: .newItem) { }
            CommandGroup(replacing: .saveItem) { }
            CommandGroup(replacing: .importExport) { }
            StandardEditCommands()
        }
    }
}

/// 显式注册编辑命令，确保 NSHostingController 窗口中的文本控件能响应 Cmd+C/V/X。
struct StandardEditCommands: Commands {
    var body: some Commands {
        CommandGroup(replacing: .undoRedo) {
            Button("撤销") {
                NSApp.sendAction(Selector(("undo:")), to: nil, from: nil)
            }
            .keyboardShortcut("z", modifiers: .command)

            Button("重做") {
                NSApp.sendAction(Selector(("redo:")), to: nil, from: nil)
            }
            .keyboardShortcut("z", modifiers: [.command, .shift])
        }

        CommandGroup(replacing: .pasteboard) {
            Button("剪切") {
                NSApp.sendAction(#selector(NSText.cut(_:)), to: nil, from: nil)
            }
            .keyboardShortcut("x", modifiers: .command)

            Button("拷贝") {
                NSApp.sendAction(#selector(NSText.copy(_:)), to: nil, from: nil)
            }
            .keyboardShortcut("c", modifiers: .command)

            Button("粘贴") {
                NSApp.sendAction(#selector(NSText.paste(_:)), to: nil, from: nil)
            }
            .keyboardShortcut("v", modifiers: .command)

            Divider()

            Button("全选") {
                NSApp.sendAction(#selector(NSText.selectAll(_:)), to: nil, from: nil)
            }
            .keyboardShortcut("a", modifiers: .command)
        }
    }
}
