import AppKit
import SwiftUI
import Combine

@MainActor
final class MenuBarManager: ObservableObject {
    nonisolated let objectWillChange = ObservableObjectPublisher()

    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private var mainWindow: NSWindow?
    private var settingsWindow: NSWindow?
    private var rightClickMenu: NSMenu?
    private let windowDelegate = AppWindowDelegate()
    private let core: AppCore

    init(core: AppCore) {
        self.core = core
        windowDelegate.manager = self
        setup()
    }

    nonisolated deinit {}

    func updateIcon(status: ProjectStatus) {
        statusItem?.button?.image = makeMenuBarIcon(status: status)
    }

    func openMainWindow() {
        popover?.performClose(nil)
        if mainWindow == nil {
            let vc = NSHostingController(rootView: MainWindowView().environmentObject(core))
            let window = NSWindow(contentViewController: vc)
            window.title = "SuperDev"
            window.setContentSize(NSSize(width: 900, height: 560))
            window.styleMask = [.titled, .closable, .resizable, .miniaturizable]
            window.minSize = NSSize(width: 800, height: 500)
            window.center()
            window.isReleasedWhenClosed = false
            window.delegate = windowDelegate
            mainWindow = window
        }
        showInDock()
        NSApp.activate(ignoringOtherApps: true)
        mainWindow?.makeKeyAndOrderFront(nil)
    }

    func openSettingsWindow() {
        popover?.performClose(nil)
        if settingsWindow == nil {
            let vc = NSHostingController(rootView: SettingsView().environmentObject(core))
            let window = NSWindow(contentViewController: vc)
            window.title = "设置"
            window.setContentSize(NSSize(width: 480, height: 420))
            window.styleMask = [.titled, .closable, .resizable]
            window.minSize = NSSize(width: 480, height: 300)
            window.center()
            window.isReleasedWhenClosed = false
            window.delegate = windowDelegate
            settingsWindow = window
        }
        showInDock()
        NSApp.activate(ignoringOtherApps: true)
        settingsWindow?.makeKeyAndOrderFront(nil)
    }

    /// 根据主窗口/设置窗口是否仍在前台或最小化，同步 Dock 显示策略。
    func syncActivationPolicyWithWindows() {
        if shouldShowInDock {
            showInDock()
        } else {
            hideFromDock()
        }
    }

    private var shouldShowInDock: Bool {
        [mainWindow, settingsWindow].compactMap { $0 }.contains { window in
            window.isVisible || window.isMiniaturized
        }
    }

    private func showInDock() {
        guard NSApp.activationPolicy() != .regular else { return }
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func hideFromDock() {
        guard NSApp.activationPolicy() != .accessory else { return }
        NSApp.setActivationPolicy(.accessory)
    }

    // 按 superdev-logo-v5-launch.svg 绘制彩色菜单栏图标
    // SVG 画布 512x512，缩放至 22pt（菜单栏舒适尺寸）
    private func makeMenuBarIcon(status: ProjectStatus) -> NSImage {
        let size = NSSize(width: 22, height: 22)
        let image = NSImage(size: size, flipped: false) { rect in
            let s = rect.height / 512.0

            // 白色圆角矩形背景
            NSColor.white.setFill()
            NSBezierPath(roundedRect: NSRect(x: 64*s, y: 64*s, width: 384*s, height: 384*s),
                         xRadius: 92*s, yRadius: 92*s).fill()

            // 深色播放三角形
            NSColor(red: 0x0B/255.0, green: 0x12/255.0, blue: 0x20/255.0, alpha: 1).setFill()
            let tri = NSBezierPath()
            tri.move(to:  NSPoint(x: 192*s, y: (512-162)*s))
            tri.line(to:  NSPoint(x: 350*s, y: (512-256)*s))
            tri.line(to:  NSPoint(x: 192*s, y: (512-350)*s))
            tri.close()
            tri.fill()

            // 绿色代码行（透明度递减）
            let green = NSColor(red: 0x32/255.0, green: 0xD7/255.0, blue: 0x9B/255.0, alpha: 1)
            let lines: [(x: CGFloat, y: CGFloat, w: CGFloat, alpha: CGFloat)] = [
                (138, 512-189-34, 72, 1.00),
                (138, 512-239-34, 96, 0.72),
                (138, 512-289-34, 72, 0.44),
            ]
            for line in lines {
                green.withAlphaComponent(line.alpha).setFill()
                let r = NSRect(x: line.x*s, y: line.y*s, width: line.w*s, height: 34*s)
                NSBezierPath(roundedRect: r, xRadius: 17*s, yRadius: 17*s).fill()
            }

            // failed 状态：右下角红色小圆点
            if status == .failed {
                NSColor.systemRed.setFill()
                NSBezierPath(ovalIn: NSRect(x: rect.width-5, y: 0, width: 5, height: 5)).fill()
            }

            return true
        }
        // 彩色图标不设 isTemplate，保留颜色
        return image
    }

    private func setup() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem?.button?.image = makeMenuBarIcon(status: .stopped)
        statusItem?.button?.action = #selector(togglePopover)
        statusItem?.button?.target = self
        statusItem?.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])

        let menu = NSMenu()
        let quitItem = NSMenuItem(title: "退出 SuperDev", action: #selector(quitApp), keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)
        statusItem?.menu = nil
        self.rightClickMenu = menu

        let popover = NSPopover()
        popover.contentSize = NSSize(width: 440, height: 360)
        popover.behavior = .transient
        popover.appearance = NSAppearance(named: .darkAqua)
        let hostingController = NSHostingController(
            rootView: PopoverView()
                .environmentObject(core)
                .environmentObject(self)
        )
        // 移除 NSPopover 默认的 NSVisualEffectView 材质背景，让 SwiftUI 背景色完全接管
        hostingController.view.wantsLayer = true
        hostingController.view.layer?.backgroundColor = NSColor(red: 0x0d/255.0, green: 0x11/255.0, blue: 0x17/255.0, alpha: 1).cgColor
        popover.contentViewController = hostingController
        self.popover = popover
    }

    @objc private func togglePopover() {
        guard let button = statusItem?.button else { return }
        let event = NSApp.currentEvent
        // 右键点击 → 显示退出菜单
        if event?.type == .rightMouseUp {
            if popover?.isShown == true { popover?.performClose(nil) }
            if let menu = rightClickMenu {
                statusItem?.menu = menu
                statusItem?.button?.performClick(nil)
                statusItem?.menu = nil
            }
            return
        }
        // 左键点击 → 切换 popover
        if popover?.isShown == true {
            popover?.performClose(nil)
        } else {
            popover?.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            // NSPopover 的 NSVisualEffectView 在 show 后才挂载，此处禁用其材质以让 SwiftUI 背景色完全接管
            disablePopoverVisualEffect()
        }
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
    }

    private func disablePopoverVisualEffect() {
        guard let popoverWindow = popover?.contentViewController?.view.window else { return }
        func disableEffectViews(in view: NSView) {
            if let effectView = view as? NSVisualEffectView {
                effectView.material = .windowBackground
                effectView.blendingMode = .withinWindow
                effectView.state = .inactive
            }
            view.subviews.forEach { disableEffectViews(in: $0) }
        }
        if let contentView = popoverWindow.contentView {
            disableEffectViews(in: contentView)
        }
    }
}

@MainActor
private final class AppWindowDelegate: NSObject, NSWindowDelegate {
    weak var manager: MenuBarManager?

    func windowDidClose(_ notification: Notification) {
        manager?.syncActivationPolicyWithWindows()
    }
}
