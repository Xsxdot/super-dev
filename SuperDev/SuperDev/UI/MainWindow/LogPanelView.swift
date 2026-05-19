import SwiftUI

struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let serviceId: UUID?  // nil = show all

    @State private var keyword: String = ""
    @State private var enabledLevels: Set<LogLevel> = [.error, .warn, .info]
    @State private var isFollowing: Bool = true
    @State private var newLogCount: Int = 0

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
            .background(Theme.bgSecondary)
            .onScrollGeometryChange(for: CGFloat.self) { geo in
                geo.contentSize.height - geo.containerSize.height - geo.contentOffset.y
            } action: { _, distanceFromBottom in
                let wasFollowing = isFollowing
                isFollowing = distanceFromBottom < 50
                if isFollowing {
                    newLogCount = 0
                }
                if isFollowing && !wasFollowing {
                    if let last = filteredLogs.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }
            .onChange(of: filteredLogs.count) { _, _ in
                if isFollowing {
                    newLogCount = 0
                    if let last = filteredLogs.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                } else {
                    newLogCount += 1
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if !isFollowing && newLogCount > 0 {
                    Button {
                        isFollowing = true
                        newLogCount = 0
                        if let last = filteredLogs.last {
                            withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                        }
                    } label: {
                        Text("↓ \(newLogCount) 条新日志")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(.white)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(Theme.accent)
                            .clipShape(Capsule())
                            .shadow(color: .black.opacity(0.3), radius: 4, y: 2)
                    }
                    .buttonStyle(.plain)
                    .padding(.trailing, 12)
                    .padding(.bottom, 8)
                    .transition(.opacity.combined(with: .scale(scale: 0.9)))
                }
            }
            .animation(.easeInOut(duration: 0.2), value: !isFollowing && newLogCount > 0)
        }
    }

    private func logRow(_ entry: LogEntry) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Text(entry.timestamp.formatted(.dateTime.hour().minute().second()))
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Theme.textTertiary)

            Text("[\(entry.serviceName)]")
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(serviceColor(entry.serviceName))

            Text(entry.level.rawValue)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(levelColor(entry.level))
                .frame(width: 48, alignment: .leading)

            Text(entry.message)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Theme.textPrimary)
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
        .contextMenu {
            Button {
                let formatted = "\(entry.timestamp.formatted(.dateTime.hour().minute().second())) [\(entry.serviceName)] \(entry.level.rawValue) \(entry.message)"
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(formatted, forType: .string)
            } label: {
                Label("复制此行", systemImage: "doc.on.doc")
            }

            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(entry.message, forType: .string)
            } label: {
                Label("复制消息", systemImage: "text.quote")
            }
        }
    }

    private var statusBar: some View {
        HStack {
            let s = stats
            Text("共 \(s.total) 条 · 已折叠 \(s.folded) 条")
                .font(.caption)
                .foregroundColor(.secondary)
            Spacer()
            if s.errors > 0 {
                statusBadge("● \(s.errors) 错误", color: Theme.statusFailed)
            }
            if s.warns > 0 {
                statusBadge("● \(s.warns) 警告", color: Theme.statusStarting)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private func statusBadge(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.system(size: 9))
            .foregroundColor(color)
            .padding(.horizontal, 7)
            .padding(.vertical, 1)
            .background(color.opacity(0.1))
            .overlay(RoundedRectangle(cornerRadius: 4).stroke(color.opacity(0.2), lineWidth: 1))
            .cornerRadius(4)
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
