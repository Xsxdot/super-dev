import AppKit
import SwiftUI
import UniformTypeIdentifiers

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
    let panelId: UUID
    let serviceId: UUID?
    var project: Project?

    @State private var chipInput: String = ""
    @State private var nextChipType: FilterChip.ChipType = .include
    @State private var chips: [FilterChip] = []
    @State private var chipLogic: ChipLogic = .or
    @State private var isFollowing: Bool = true
    @State private var newLogCount: Int = 0
    @State private var showRulesSheet = false
    @State private var showSaveRuleSheet = false
    @State private var saveRuleName: String = ""
    @State private var saveRuleStatusMessage: String?
    @State private var activeSelectionEntryId: UUID?
    @State private var activeSelectionText: String?
    @State private var activeSelectionRect: CGRect?

    private var includeChips: [String] {
        chips.filter { $0.type == .include }.map(\.keyword)
    }

    private var excludeChips: [String] {
        chips.filter { $0.type == .exclude }.map(\.keyword)
    }

    private var activeProject: Project? {
        project ?? core.project(forServiceId: serviceId)
    }

    private var bookmark: LogBookmark? {
        core.bookmarks[panelId]
    }

    private enum LogDisplayItem: Identifiable {
        case entry(LogEntry)
        case markerStart(id: UUID, date: Date)
        case markerEnd(id: UUID, date: Date)

        var id: UUID {
            switch self {
            case .entry(let e): return e.id
            case .markerStart(let id, _): return id
            case .markerEnd(let id, _):   return id
            }
        }
    }

    private struct LogDisplay {
        let items: [LogDisplayItem]
        let stats: (total: Int, folded: Int, errors: Int, warns: Int)
    }

    private func makeLogDisplay() -> LogDisplay {
        let logs = core.filteredLogs(
            serviceId: serviceId,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic.logFilterLogic
        )

        var items: [LogDisplayItem] = []
        var folded = 0
        var errors = 0
        var warns = 0

        if let bm = bookmark, let startTime = bm.startTime {
            if bm.isCompleted, let endTime = bm.endTime {
                let before = logs.filter { $0.timestamp < startTime }
                let after  = logs.filter { $0.timestamp > endTime }
                items += before.map { .entry($0) }
                items.append(.markerStart(id: UUID(), date: startTime))
                items += bm.lockedLogs.map { .entry($0) }
                items.append(.markerEnd(id: UUID(), date: endTime))
                items += after.map { .entry($0) }
            } else {
                let before = logs.filter { $0.timestamp < startTime }
                let after  = logs.filter { $0.timestamp >= startTime }
                items += before.map { .entry($0) }
                if !after.isEmpty || bm.isActive {
                    items.append(.markerStart(id: UUID(), date: startTime))
                }
                items += after.map { .entry($0) }
            }
        } else {
            items = logs.map { .entry($0) }
        }

        for item in items {
            if case .entry(let e) = item {
                if e.repeatCount > 1 { folded += e.repeatCount - 1 }
                if e.level == .error { errors += 1 }
                else if e.level == .warn { warns += 1 }
            }
        }

        let entryCount = items.filter { if case .entry = $0 { true } else { false } }.count
        return LogDisplay(items: items, stats: (entryCount, folded, errors, warns))
    }

    var body: some View {
        let display = makeLogDisplay()
        VStack(spacing: 0) {
            toolbar
            Divider()
            if core.viewingRunId != nil {
                historyBanner(info: historyBannerInfo(from: display))
                Divider()
            }
            logList(items: display.items)
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
            ScrollView(.horizontal, showsIndicators: false) {
                chipSearchArea
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Spacer(minLength: 4)
            historyMenu
            rulesButton
            if !chips.isEmpty {
                saveAsRuleButton
            }
            Divider().frame(height: 14)
            bookmarkControl
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

            TextField(chips.isEmpty ? "关键词过滤，回车添加" : "添加关键词…", text: $chipInput)
                .textFieldStyle(.plain)
                .frame(minWidth: 80, maxWidth: 140)
                .onSubmit { addChipFromInput() }
                .onPasteCommand(of: [.plainText]) { _ in
                    if let pasted = NSPasteboard.general.string(forType: .string) {
                        addChipsFromText(pasted, type: nextChipType)
                    }
                }
                .onChange(of: chipInput) { _, newValue in
                    guard newValue.contains(",") || newValue.contains("\n") || newValue.contains("\t") || newValue.contains(";") else { return }
                    let parts = KeywordTokenizer.split(newValue)
                    guard parts.count > 1 else { return }
                    for part in parts.dropLast() {
                        addChip(part, type: nextChipType)
                    }
                    chipInput = parts.last ?? ""
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

            // 项目级规则快捷开关（仅在有项目时显示）
            if let proj = activeProject {
                let rules = core.logRules(for: proj.id)
                if !rules.isEmpty {
                    Divider().frame(height: 14)
                    ForEach(rules) { rule in
                        projectRuleChip(rule, project: proj)
                    }
                }
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

    private func projectRuleChip(_ rule: LogRule, project: Project) -> some View {
        Button {
            toggleProjectRule(rule, project: project)
        } label: {
            HStack(spacing: 3) {
                Text(rule.type == .include ? "↑" : "↓")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundColor(rule.enabled ? (rule.type == .include ? .blue : .green) : .secondary)
                Text(rule.name.isEmpty ? rule.keywords.first ?? "" : rule.name)
                    .font(.system(size: 11))
                    .lineLimit(1)
                    .strikethrough(!rule.enabled, color: .secondary)
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(rule.enabled
                ? (rule.type == .include ? Color.blue.opacity(0.10) : Color.green.opacity(0.10))
                : Color.secondary.opacity(0.08))
            .cornerRadius(4)
        }
        .buttonStyle(.plain)
        .help(rule.enabled ? "点击禁用此规则" : "点击启用此规则")
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
            saveRuleStatusMessage = nil
            showSaveRuleSheet = true
        } label: {
            Image(systemName: "square.and.arrow.down")
        }
        .buttonStyle(.plain)
        .help("将全部临时过滤保存为项目规则")
        .disabled(activeProject == nil)
    }

    private var bookmarkControl: some View {
        Group {
            if let bm = bookmark, bm.isCompleted {
                HStack(spacing: 6) {
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(bm.formattedText, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help("复制标记区间日志")

                    Button {
                        exportBookmark(bm)
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help("导出标记区间日志")

                    Button {
                        core.clearBookmark(panelId: panelId)
                    } label: {
                        Image(systemName: "xmark.circle")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("清除书签")
                }
            } else if let bm = bookmark, bm.isActive {
                HStack(spacing: 4) {
                    Text("\(bm.lockedLogs.count)")
                        .font(.system(size: 9, weight: .bold))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 2)
                        .background(Color.red.opacity(0.15))
                        .cornerRadius(4)
                        .foregroundColor(.red)
                    Button {
                        core.endBookmark(panelId: panelId)
                    } label: {
                        Image(systemName: "stop.circle.fill")
                            .font(.system(size: 14))
                            .foregroundColor(.red)
                    }
                    .buttonStyle(.plain)
                    .help("结束书签标记 (⌘⇧B)")
                    .keyboardShortcut("b", modifiers: [.command, .shift])
                }
            } else {
                Button {
                    core.startBookmark(panelId: panelId)
                } label: {
                    Image(systemName: "record.circle")
                        .font(.system(size: 14))
                        .foregroundColor(.green)
                }
                .buttonStyle(.plain)
                .help("开始书签标记 (⌘⇧B)")
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
        }
    }

    private func exportBookmark(_ bm: LogBookmark) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "superdev-log-\(Int(bm.startTime?.timeIntervalSince1970 ?? 0)).log"
        panel.allowedContentTypes = [.plainText]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            try? bm.formattedText.write(to: url, atomically: true, encoding: .utf8)
        }
    }

    private var saveRuleSheet: some View {
        let proposedRules = LogChipRuleBuilder.makeRulesFromChips(
            name: saveRuleName,
            chips: chips,
            logic: chipLogic
        )
        return VStack(alignment: .leading, spacing: 14) {
            Text("保存为项目规则")
                .font(.headline)
            TextField("规则名称", text: $saveRuleName)
            VStack(alignment: .leading, spacing: 6) {
                Text("关键词逻辑：\(chipLogic.label)")
                    .font(.caption)
                    .foregroundColor(.secondary)
                if !includeChips.isEmpty {
                    Text("包含：\(includeChips.joined(separator: ", "))")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                if !excludeChips.isEmpty {
                    Text("排除：\(excludeChips.joined(separator: ", "))")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                if proposedRules.count > 1 {
                    Text("将创建 \(proposedRules.count) 条规则（包含 / 排除各一条）")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
            if let saveRuleStatusMessage {
                Text(saveRuleStatusMessage)
                    .font(.caption)
                    .foregroundColor(.orange)
            }
            HStack {
                Spacer()
                Button("取消") { showSaveRuleSheet = false }
                Button("保存") {
                    saveChipsAsProjectRules()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 360)
    }

    // MARK: - History banner

    private func historyBanner(info: (startTime: Date?, logCount: Int)) -> some View {
        let (startTime, logCount) = info
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

    /// Uses the already-computed display so the banner count matches what is shown
    /// without triggering a second call to makeLogDisplay().
    private func historyBannerInfo(from display: LogDisplay) -> (startTime: Date?, logCount: Int) {
        let firstEntry: LogEntry? = display.items.compactMap {
            if case .entry(let e) = $0 { return e } else { return nil }
        }.first
        let startTime: Date? = {
            if let runId = core.viewingRunId,
               let summary = core.availableRuns.first(where: { $0.runId == runId }) {
                return summary.startTime
            }
            return firstEntry?.timestamp
        }()
        let entryCount = display.items.filter { if case .entry = $0 { true } else { false } }.count
        return (startTime, entryCount)
    }

    // MARK: - Log list

    private func logList(items: [LogDisplayItem]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(items) { item in
                        switch item {
                        case .entry(let entry):
                            logRow(entry, isBookmarked: isInActiveBookmark(entry))
                                .id(item.id)
                        case .markerStart(_, let date):
                            bookmarkMarkerRow(isStart: true, date: date)
                                .id(item.id)
                        case .markerEnd(_, let date):
                            bookmarkMarkerRow(isStart: false, date: date)
                                .id(item.id)
                        }
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
                    if let last = items.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }
            .onChange(of: items.count) { oldCount, newCount in
                if isFollowing {
                    newLogCount = 0
                    if let last = items.last {
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
                        if let last = items.last {
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
            .onChange(of: chips) { _, _ in
                isFollowing = true
                newLogCount = 0
            }
        }
    }

    private func logRow(_ entry: LogEntry, isBookmarked: Bool = false) -> some View {
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

            logMessageArea(entry)

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
        .background(isBookmarked ? bookmarkRowBackground(entry.level) : rowBackground(entry.level))
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

            if let selected = selectionTextForEntry(entry.id), !selected.isEmpty {
                Button {
                    fillChipInputFromSelection(selected)
                } label: {
                    Label("填入过滤关键词", systemImage: "plus.magnifyingglass")
                }
            }
        }
    }

    @ViewBuilder
    private func logMessageArea(_ entry: LogEntry) -> some View {
        ZStack(alignment: .topLeading) {
            SelectableLogText(
                text: entry.message,
                textColor: NSColor(Theme.textPrimary),
                onSelectionChange: { text, rect in
                    if let text, !text.isEmpty {
                        activeSelectionEntryId = entry.id
                        activeSelectionText = text
                        activeSelectionRect = rect
                    } else if activeSelectionEntryId == entry.id {
                        clearLogSelection()
                    }
                }
            )
            .frame(maxWidth: .infinity, alignment: .leading)

            if activeSelectionEntryId == entry.id,
               let text = activeSelectionText,
               !text.isEmpty,
               let rect = activeSelectionRect {
                Button {
                    fillChipInputFromSelection(text)
                } label: {
                    Image(systemName: "plus.magnifyingglass")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(4)
                        .background(Theme.accent)
                        .clipShape(Circle())
                        .shadow(color: .black.opacity(0.25), radius: 2, y: 1)
                }
                .buttonStyle(.plain)
                .help("填入过滤关键词输入框")
                .offset(x: rect.minX, y: max(0, rect.minY - 22))
            }
        }
    }

    private func selectionTextForEntry(_ entryId: UUID) -> String? {
        activeSelectionEntryId == entryId ? activeSelectionText : nil
    }

    private func clearLogSelection() {
        activeSelectionEntryId = nil
        activeSelectionText = nil
        activeSelectionRect = nil
    }

    private func fillChipInputFromSelection(_ text: String) {
        chipInput = text.trimmingCharacters(in: .whitespacesAndNewlines)
        clearLogSelection()
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
        let parts = KeywordTokenizer.split(chipInput)
        if parts.isEmpty {
            addChip(chipInput, type: nextChipType)
        } else {
            for part in parts {
                addChip(part, type: nextChipType)
            }
        }
        chipInput = ""
    }

    private func addChipsFromText(_ text: String, type: FilterChip.ChipType) {
        for part in KeywordTokenizer.split(text) {
            addChip(part, type: type)
        }
    }

    private func saveChipsAsProjectRules() {
        guard let proj = activeProject else { return }
        let rules = LogChipRuleBuilder.makeRulesFromChips(
            name: saveRuleName,
            chips: chips,
            logic: chipLogic
        )
        var existing = core.logRules(for: proj.id)
        var saved = 0
        var skipped = 0
        for rule in rules {
            if LogChipRuleBuilder.isDuplicate(rule, in: existing) {
                skipped += 1
                continue
            }
            try? core.addLogRule(rule, to: proj)
            existing = core.logRules(for: proj.id)
            saved += 1
        }
        if saved > 0 {
            showSaveRuleSheet = false
            chips = []
            saveRuleStatusMessage = nil
        } else if skipped > 0 {
            saveRuleStatusMessage = "相同规则已存在，未重复保存"
        }
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

    private func toggleProjectRule(_ rule: LogRule, project: Project) {
        let cached = core.logRules(for: project.id)
        var config = LogRulesConfig(rules: cached)
        guard let idx = config.rules.firstIndex(where: { $0.id == rule.id }) else { return }
        config.rules[idx].enabled.toggle()
        do {
            try core.saveLogRules(config, for: project)
        } catch {
            print("[SuperDev] Failed to toggle rule '\(rule.name)': \(error)")
        }
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

    private func isInActiveBookmark(_ entry: LogEntry) -> Bool {
        guard let bm = bookmark else { return false }
        if bm.isActive, let start = bm.startTime {
            return entry.timestamp >= start
        }
        if bm.isCompleted, let start = bm.startTime, let end = bm.endTime {
            return entry.timestamp >= start && entry.timestamp <= end
        }
        return false
    }

    private func bookmarkRowBackground(_ level: LogLevel) -> Color {
        switch level {
        case .error: return Color.red.opacity(0.18)
        case .warn:  return Color.yellow.opacity(0.12)
        default:     return Color(red: 0.12, green: 0.10, blue: 0.04)
        }
    }

    private func bookmarkMarkerRow(isStart: Bool, date: Date) -> some View {
        let label = isStart ? "▶ 开始标记" : "■ 结束标记"
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm:ss"
        formatter.locale = Locale(identifier: "en_US_POSIX")
        let timeStr = formatter.string(from: date)
        return HStack {
            Spacer()
            Text("\(label)  \(timeStr)")
                .font(.system(size: 10, weight: .bold))
                .foregroundColor(.white)
            Spacer()
        }
        .padding(.vertical, 4)
        .background(Color(red: 0.47, green: 0.22, blue: 0.04))
    }

    private let serviceColors: [Color] = [
        .blue, .pink, .purple, .orange, .cyan, .mint, .indigo, .teal
    ]
    private func serviceColor(_ name: String) -> Color {
        let idx = abs(name.hashValue) % serviceColors.count
        return serviceColors[idx]
    }
}
