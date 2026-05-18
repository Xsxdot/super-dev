import AppKit
import SwiftUI

@MainActor
final class MenuBarManager {
    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private let core: AppCore

    init(core: AppCore) {
        self.core = core
        setup()
    }

    nonisolated deinit {}

    func updateIcon(status: ProjectStatus) {
        let symbolName: String
        switch status {
        case .stopped:  symbolName = "terminal"
        case .starting: symbolName = "terminal.fill"
        case .running:  symbolName = "play.circle.fill"
        case .failed:   symbolName = "exclamationmark.circle.fill"
        }
        let icon = NSImage(systemSymbolName: symbolName, accessibilityDescription: "SuperDev")
        icon?.isTemplate = true
        statusItem?.button?.image = icon
    }

    private func setup() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let icon = NSImage(systemSymbolName: "terminal", accessibilityDescription: "SuperDev")
        icon?.isTemplate = true
        statusItem?.button?.image = icon
        statusItem?.button?.action = #selector(togglePopover)
        statusItem?.button?.target = self

        let popover = NSPopover()
        popover.contentSize = NSSize(width: 440, height: 360)
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(
            rootView: PopoverView().environmentObject(core)
        )
        self.popover = popover
    }

    @objc private func togglePopover() {
        guard let button = statusItem?.button else { return }
        if popover?.isShown == true {
            popover?.performClose(nil)
        } else {
            popover?.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        }
    }
}
