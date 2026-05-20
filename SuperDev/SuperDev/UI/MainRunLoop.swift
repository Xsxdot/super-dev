import Foundation

/// Defers work to the next main-runloop turn so AppKit is not inside a layout / constraint pass.
enum MainRunLoop {
    static func deferred(_ work: @escaping @MainActor () -> Void) {
        DispatchQueue.main.async(execute: work)
    }
}
