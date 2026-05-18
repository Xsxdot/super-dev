import Foundation

// Persists the list of project root paths added by the user (UserDefaults).
// Config content lives in each project's .superdev/config.yaml — this only stores paths.
final class ProjectStore {
    private let key = "superdev.project_paths"
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    func loadPaths() -> [String] {
        defaults.stringArray(forKey: key) ?? []
    }

    func addPath(_ path: String) {
        var paths = loadPaths()
        guard !paths.contains(path) else { return }
        paths.append(path)
        defaults.set(paths, forKey: key)
    }

    func removePath(_ path: String) {
        var paths = loadPaths()
        paths.removeAll { $0 == path }
        defaults.set(paths, forKey: key)
    }
}
