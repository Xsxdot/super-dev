import SwiftUI

final class AppDelegate: NSObject, NSApplicationDelegate {
    var menuBarManager: MenuBarManager?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
    }
}

@main
struct SuperDevApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var core = AppCore()

    var body: some Scene {
        Window("SuperDev", id: "main") {
            MainWindowView()
                .environmentObject(core)
        }
        .windowResizability(.contentSize)

        Settings {
            SettingsView()
                .environmentObject(core)
        }
    }
}
