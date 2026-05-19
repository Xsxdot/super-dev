import SwiftUI

enum Theme {
    // MARK: - Backgrounds
    static let bgPrimary    = Color(hex: "#0d1117")   // 左侧面板底色
    static let bgSecondary  = Color(hex: "#010409")   // 右侧面板底色
    static let bgElevated   = Color(hex: "#161b22")   // 选中行 / hover 背景
    static let bgToolbar    = Color(hex: "#0d1117")   // toolbar 行背景

    // MARK: - Borders
    static let borderPrimary   = Color(hex: "#21262d")
    static let borderSecondary = Color(hex: "#30363d")

    // MARK: - Text
    static let textPrimary   = Color(hex: "#e6edf3")
    static let textSecondary = Color(hex: "#8b949e")
    static let textTertiary  = Color(hex: "#6e7681")

    // MARK: - Accent
    static let accent = Color(hex: "#1f6feb")

    // MARK: - Status
    static let statusRunning  = Color(hex: "#3fb950")
    static let statusStarting = Color(hex: "#d29922")
    static let statusFailed   = Color(hex: "#f85149")
    static let statusStopped  = Color(hex: "#6e7681")
}

extension Color {
    init(hex: String) {
        let hex = hex.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
        var int: UInt64 = 0
        Scanner(string: hex).scanHexInt64(&int)
        let r = Double((int >> 16) & 0xFF) / 255
        let g = Double((int >> 8)  & 0xFF) / 255
        let b = Double(int         & 0xFF) / 255
        self.init(red: r, green: g, blue: b)
    }
}
