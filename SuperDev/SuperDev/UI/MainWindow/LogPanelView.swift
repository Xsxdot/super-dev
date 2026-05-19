import SwiftUI

struct FilterChip: Identifiable, Equatable {
    let id: UUID
    var keyword: String
    var type: ChipType

    enum ChipType {
        case include
        case exclude
    }

    init(id: UUID = UUID(), keyword: String, type: ChipType = .include) {
        self.id = id
        self.keyword = keyword
        self.type = type
    }
}

enum ChipLogic {
    case and
    case or

    var logFilterLogic: LogFilter.ChipLogic {
        switch self {
        case .and: return .and
        case .or: return .or
        }
    }

    mutating func toggle() {
        self = self == .and ? .or : .and
    }

    var label: String {
        self == .and ? "AND" : "OR"
    }
}

struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let serviceId: UUID?
    var project: Project?

    @State private var chipInput: String = ""
    @State private var nextChipType: FilterChip.ChipType = .include
    @State private var chips: [FilterChip] = []
    @State private var chipLogic: ChipLogic = .or
    @State private var enabledLevels: Set<LogLevel> = [.error, .warn, .info]
    @State private var isFollowing: Bool = true
    @State private var newLogCount: Int = 0
    @State private var showRulesSheet = false
    @State private var showSaveRuleSheet = false
    @State private var saveRuleName: String = ""
    @State private var saveRuleType: LogRule.RuleType = .exclude

    private var includeChips: [String] {
        chips.filter { $0.type == .include }.map(\.keyword)
    }

    private var excludeChips: [String] {
        chips.filter { $0.type == .exclude }.map(\.keyword)
    }

    private var activeProject: Project? {
        project ?? core.project(forServiceId: serviceId)
    }

    private struct LogDisplay {
        let logs: [LogEntry]
        let stats: (total: Int, folded: Int, errors: Int, warns: Int)
    }

    private func makeLogDisplay() -> LogDisplay {
        let logs = core.filteredLogs(
            serviceId: serviceId,
            levels: enabledLevels,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic.logFilterLogic
        )
        var folded = 0
        var errors = 0
        var warns = 0
        for entry in logs {
            if entry.repeatCount > 1 { folded += entry.repeatCount - 1 }
            if entry.level == .error { errors += 1 }
            else if entry.level == .warn { warns += 1 }
        }
        return LogDisplay(logs: logs, stats: (logs.count, folded, errors, warns))
    }

    var body: some View {
        let display = makeLogDisplay()
        VStack(spacing: 0) {
            toolbar
            Divider()
            if core.viewingRunId != nil {
                historyBanner
                Divider()
            }
            logList(logs: display.logs)
            Divider()
            statusBar(stats: display.stats)
        }
        .sheet(isPresented: $showRulesSheet) {
            if let proj = activeProject {
                LogRulesView(project: proj)
                    .environmentObject(core)
            }
        }
        .sheet(isPresented: $showSaveRuleSheet) {
            saveRuleSheet
        }
        .onAppear { core.refreshAvailableRuns() }
    }

    // MARK: - Toolbar

    private var toolbar: some View {
        HStack(spacing: 8) {
            chipSearchArea
            Divider().frame(height: 16)
            ForEach([LogLevel.error, .warn, .info], id: \.self) { level in
                levelToggle(level)
            }
            Spacer(minLength: 4)
            historyMenu
            rulesButton
            if !chips.isEmpty {
                saveAsRuleButton
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private var chipSearchArea: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
                .font(.system(size: 11))

            Picker("", selection: $nextChipType) {
                Text("包含").tag(FilterChip.ChipType.include)
                Text("排除").tag(FilterChip.ChipType.exclude)
            }
            .pickerStyle(.segmented)
            .frame(width: 88)
            .labelsHidden()

            ForEach(chips) { chip in
                chipView(chip)
            }

            if !chips.isEmpty {
                Button(chipLogic.label) { chipLogic.toggle() }
                    .font(.system(size: 10, weight: .semibold))
                    .padding(.horizontal, 5)
                    .padding(.vertical, 2)
                    .background(Color.secondary.opacity(0.15))
                    .cornerRadius(4)
                    .buttonStyle(.plain)
                    .help("切换关键词之间的 AND / OR 逻辑")
            }

            TextField(chips.isEmpty ? "关键词过滤，回车添加" : "添加关键词…", text: $chipInput)
                .textFieldStyle(.plain)
                .frame(minWidth: 80, maxWidth: 140)
                .onSubmit { addChipFromInput() }
                .onChange(of: chipInput) { _, newValue in
                    if newValue.contains(",") {
                        let parts = newValue.split(separator: ",")
                        for part in parts.dropLast() {
                            addChip(String(part), type: nextChipType)
                        }
                        chipInput = String(parts.last ?? "")
                    }
                }

            if chips.isEmpty {
                Button {
                    addChipFromInput()
                } label: {
                    Image(systemName: "plus.circle")
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("添加关键词")
            }
        }
    }

    private func chipView(_ chip: FilterChip) -> some View {
        HStack(spacing: 3) {
            Button {
                toggleChipType(chip.id)
            } label: {
                Text(chip.type == .include ? "+" : "−")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundColor(chip.type == .include ? .blue : .orange)
            }
            .buttonStyle(.plain)

            Text(chip.keyword)
                .font(.system(size: 11))
                .lineLimit(1)

            // 竖线分隔
            Rectangle()
                .fill(Color.secondary.opacity(0.3))
                .frame(width: 1, height: 10)

            Button {
                saveChipToProjectRule(chip)
            } label: {
                Image(systemName: "square.and.arrow.up")
                    .font(.system(size: 8))
                    .foregroundColor(.green)
            }
            .buttonStyle(.plain)
            .help("保存到项目规则")
            .disabled(activeProject == nil)

            Button {
                chips.removeAll { $0.id == chip.id }
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 3)
        .background(chip.type == .include ? Color.blue.opacity(0.12) : Color.orange.opacity(0.12))
        .cornerRadius(4)
    }

    private var historyMenu: some View {
        Menu {
            Button("实时日志") {
                core.returnToLiveLogs()
            }
            .disabled(core.viewingRunId == nil)

            if !core.availableRuns.isEmpty {
                Divider()
                ForEach(core.availableRuns) { run in
                    Button(historyRunLabel(run)) {
                        core.loadHistoryRun(run.runId)
                    }
                }
            } else {
                Text("暂无历史记录")
            }
        } label: {
            Label("历史", systemImage: "clock.arrow.circlepath")
                .font(.caption)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .onAppear { core.refreshAvailableRuns() }
    }

    private var rulesButton: some View {
        Button {
            showRulesSheet = true
        } label: {
            Image(systemName: "slider.horizontal.3")
        }
        .buttonStyle(.plain)
        .help("持久化过滤规则")
        .disabled(activeProject == nil)
    }

    private var saveAsRuleButton: some View {
        Button {
            saveRuleName = chips.map(\.keyword).joined(separator: ", ")
            saveRuleType = excludeChips.isEmpty ? .include : .exclude
            showSaveRuleSheet = true
        } label: {
            Image(systemName: "square.and.arrow.down")
        }
        .buttonStyle(.plain)
        .help("保存为持久化规则")
        .disabled(activeProject == nil)
    }

    private var saveRuleSheet: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("保存为规则")
                .font(.headline)
            TextField("规则名称", text: $saveRuleName)
            Picker("类型", selection: $saveRuleType) {
                Text("排除").tag(LogRule.RuleType.exclude)
                Text("包含").tag(LogRule.RuleType.include)
            }
            .pickerStyle(.segmented)
            Text("关键词：\(chips.map(\.keyword).joined(separator: ", "))")
                .font(.caption)
                .foregroundColor(.secondary)
            HStack {
                Spacer()
                Button("取消") { showSaveRuleSheet = false }
                Button("保存") {
                    guard let proj = activeProject else { return }
                    let rule = LogRule(
                        name: saveRuleName.isEmpty ? "快捷过滤" : saveRuleName,
                        type: saveRuleType,
                        keywords: chips.map(\.keyword),
                        logic: chipLogic == .and ? .and : .or,
                        enabled: true
                    )
                    try? core.addLogRule(rule, to: proj)
                    showSaveRuleSheet = false
                    chips = []
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 320)
    }

    // MARK: - History banner

    private var historyBanner: some View {
        let (startTime, logCount) = historyBannerInfo
        return HStack(spacing: 8) {
            Image(systemName: "clock")
                .foregroundColor(.orange)
            if let startTime {
                Text("查看历史记录：\(startTime.formatted(.dateTime.month().day().hour().minute())) · \(logCount) 条")
                    .font(.system(size: 12))
            } else {
                Text("查看历史记录 · \(logCount) 条")
                    .font(.system(size: 12))
            }
            Spacer()
            Button("返回实时") {
                core.returnToLiveLogs()
            }
            .font(.system(size: 12, weight: .medium))
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color.yellow.opacity(0.15))
    }

    /// Uses the same filters as the log list so the banner count matches what is shown.
    private var historyBannerInfo: (startTime: Date?, logCount: Int) {
        let displayed = makeLogDisplay()
        let startTime: Date? = {
            if let runId = core.viewingRunId,
               let summary = core.availableRuns.first(where: { $0.runId == runId }) {
                return summary.startTime
            }
            return displayed.logs.first?.timestamp
        }()
        return (startTime, displayed.logs.count)
    }

    // MARK: - Log list

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

    private func logList(logs: [LogEntry]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(logs) { entry in
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
                    if let last = logs.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }
            .onChange(of: logs.count) { oldCount, newCount in
                if isFollowing {
                    newLogCount = 0
                    if let last = logs.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                } else {
                    newLogCount += max(0, newCount - oldCount)
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if !isFollowing && newLogCount > 0 {
                    Button {
                        isFollowing = true
                        newLogCount = 0
                        if let last = logs.last {
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
            .onChange(of: enabledLevels) { _, _ in
                isFollowing = true
                newLogCount = 0
            }
            .onChange(of: chips) { _, _ in
                isFollowing = true
                newLogCount = 0
            }
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

    private func statusBar(stats s: (total: Int, folded: Int, errors: Int, warns: Int)) -> some View {
        HStack {
            let modeLabel = core.viewingRunId != nil ? "历史" : "实时"
            Text("\(modeLabel) · 显示 \(s.total) 条 · 已折叠 \(s.folded) 条")
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

    // MARK: - Helpers

    private func addChipFromInput() {
        addChip(chipInput, type: nextChipType)
        chipInput = ""
    }

    private func addChip(_ text: String, type: FilterChip.ChipType = .include) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        guard !chips.contains(where: { $0.keyword.caseInsensitiveCompare(trimmed) == .orderedSame }) else { return }
        chips.append(FilterChip(keyword: trimmed, type: type))
    }

    private func toggleChipType(_ id: UUID) {
        guard let idx = chips.firstIndex(where: { $0.id == id }) else { return }
        chips[idx].type = chips[idx].type == .include ? .exclude : .include
    }

    private func saveChipToProjectRule(_ chip: FilterChip) {
        guard let proj = activeProject else { return }
        let ruleType: LogRule.RuleType = chip.type == .include ? .include : .exclude
        let rule = LogRule(
            name: chip.keyword,
            type: ruleType,
            keywords: [chip.keyword],
            logic: .or,
            enabled: true
        )
        try? core.addLogRule(rule, to: proj)
    }

    private func historyRunLabel(_ run: RunSummary) -> String {
        let time = run.startTime.formatted(.dateTime.month().day().hour().minute())
        return "\(time) · \(run.logCount) 条"
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
