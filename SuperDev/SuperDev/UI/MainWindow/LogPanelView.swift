import SwiftUI

struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let serviceId: UUID?  // nil = show all

    @State private var keyword: String = ""
    @State private var enabledLevels: Set<LogLevel> = [.error, .warn, .info]

    private var filteredLogs: [LogEntry] {
        core.filteredLogs(serviceId: serviceId, levels: enabledLevels,
                          keyword: keyword.isEmpty ? nil : keyword)
    }

    private var stats: (total: Int, folded: Int, errors: Int, warns: Int) {
        let all = core.filteredLogs(serviceId: serviceId)
        let folded = all.filter { $0.repeatCount > 1 }.reduce(0) { $0 + $1.repeatCount - 1 }
        let errors = all.filter { $0.level == .error }.count
        let warns = all.filter { $0.level == .warn }.count
        return (all.count, folded, errors, warns)
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            logList
            Divider()
            statusBar
        }
    }

    private var toolbar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
            TextField("关键词过滤", text: $keyword)
                .textFieldStyle(.plain)

            Divider().frame(height: 16)

            ForEach([LogLevel.error, .warn, .info], id: \.self) { level in
                levelToggle(level)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private func levelToggle(_ level: LogLevel) -> some View {
        Button {
            if enabledLevels.contains(level) { enabledLevels.remove(level) }
            else { enabledLevels.insert(level) }
        } label: {
            Text(level.rawValue)
                .font(.caption)
                .fontWeight(.medium)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(enabledLevels.contains(level) ? levelColor(level).opacity(0.15) : Color.clear)
                .foregroundColor(enabledLevels.contains(level) ? levelColor(level) : .secondary)
                .cornerRadius(4)
        }
        .buttonStyle(.plain)
    }

    private var logList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(filteredLogs) { entry in
                        logRow(entry)
                            .id(entry.id)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
            }
            .background(Color(red: 0.12, green: 0.12, blue: 0.12))
            .onChange(of: filteredLogs.count) { _, _ in
                if let last = filteredLogs.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }
        }
    }

    private func logRow(_ entry: LogEntry) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Text(entry.timestamp.formatted(.dateTime.hour().minute().second()))
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Color(white: 0.45))

            Text("[\(entry.serviceName)]")
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(serviceColor(entry.serviceName))

            Text(entry.level.rawValue)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(levelColor(entry.level))
                .frame(width: 48, alignment: .leading)

            Text(entry.message)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Color(white: 0.85))
                .textSelection(.enabled)
                .lineLimit(nil)

            Spacer()

            if entry.repeatCount > 1 {
                Text("×\(entry.repeatCount)")
                    .font(.caption2)
                    .fontWeight(.bold)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 2)
                    .background(levelColor(entry.level))
                    .foregroundColor(.black)
                    .clipShape(Capsule())
            }
        }
        .padding(.vertical, 2)
        .padding(.horizontal, 4)
        .background(rowBackground(entry.level))
        .cornerRadius(2)
    }

    private var statusBar: some View {
        HStack {
            let s = stats
            Text("共 \(s.total) 条 · 已折叠 \(s.folded) 条")
                .font(.caption)
                .foregroundColor(.secondary)
            Spacer()
            if s.errors > 0 {
                Text("\(s.errors)E").font(.caption).foregroundColor(.red)
            }
            if s.warns > 0 {
                Text("\(s.warns)W").font(.caption).foregroundColor(.yellow)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private func levelColor(_ level: LogLevel) -> Color {
        switch level {
        case .error: return .red
        case .warn:  return .yellow
        case .info:  return Color(red: 0.53, green: 0.93, blue: 0.53)
        case .debug: return .gray
        case .unknown: return .gray
        }
    }

    private func rowBackground(_ level: LogLevel) -> Color {
        switch level {
        case .error: return Color.red.opacity(0.12)
        case .warn:  return Color.yellow.opacity(0.08)
        default:     return Color.clear
        }
    }

    private let serviceColors: [Color] = [
        .blue, .pink, .purple, .orange, .cyan, .mint, .indigo, .teal
    ]
    private func serviceColor(_ name: String) -> Color {
        let idx = abs(name.hashValue) % serviceColors.count
        return serviceColors[idx]
    }
}
