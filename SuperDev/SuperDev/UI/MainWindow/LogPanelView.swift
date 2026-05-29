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
    let projectId: UUID?
    var project: Project?

    @State private var chipInput: String = ""
    @State private var nextChipType: FilterChip.ChipType = .include
    @State private var chips: [FilterChip] = []
    @State private var chipLogic: ChipLogic = .or
    @State private var isFollowing: Bool = true
    @State private var newLogCount: Int = 0
    /// Drives `.scrollPosition`; kept in sync with the last visible row when following.
    @State private var scrollAnchorId: UUID?
    /// Ignore scroll-geometry follow resets briefly after programmatic scroll-to-bottom.
    @State private var suppressFollowGeometryUntil: Date = .distantPast
    /// History runs for this panel's current project (not shared globally).
    @State private var panelHistoryRuns: [RunSummary] = []
    @State private var historyLoadTask: Task<Void, Never>?
    @State private var showRulesSheet = false
    @State private var showSaveRuleSheet = false
    @State private var saveRuleName: String = ""
    @State private var saveRuleStatusMessage: String?
    @State private var activeSelectionEntryId: UUID?
    @State private var activeSelectionText: String?
    @State private var activeSelectionRect: CGRect?
    /// 缓存的展示数据，只在依赖变化时重算，避免每次 body 调用都遍历所有日志
    @State private var cachedDisplay: LogDisplay = LogDisplay(items: [], stats: (0, 0, 0, 0))
    @State private var displayRefreshTask: Task<Void, Never>?

    private var includeChips: [String] {
        chips.filter { $0.type == .include }.map(\.keyword)
    }

    private var excludeChips: [String] {
        chips.filter { $0.type == .exclude }.map(\.keyword)
    }

    private var activeProject: Project? {
        if let project { return project }
        if let projectId, let p = core.project(id: projectId) { return p }
        return core.project(forServiceId: serviceId)
    }

    private var effectiveProjectId: UUID? {
        project?.id ?? projectId ?? activeProject?.id
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
            projectId: serviceId == nil ? effectiveProjectId : nil,
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
        VStack(spacing: 0) {
            toolbar
            Divider()
            if core.viewingRunId != nil {
                historyBanner(info: historyBannerInfo(from: cachedDisplay))
                Divider()
            }
            logList(items: cachedDisplay.items)
            Divider()
            statusBar(stats: cachedDisplay.stats)
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
        .onAppear {
            loadPanelHistoryRuns()
            isFollowing = true
            refreshDisplayImmediately()
        }
        .onChange(of: cachedDisplay.items.last?.id) { _, newId in
            guard isFollowing, let newId else { return }
            MainRunLoop.deferred {
                suppressFollowGeometryUntil = Date().addingTimeInterval(0.25)
                scrollAnchorId = newId
            }
        }
        .onChange(of: core.logSourceRevision) { _, _ in scheduleDisplayRefresh() }
        .onChange(of: core.bookmarks[panelId]?.lockedLogs.count) { _, _ in scheduleDisplayRefresh() }
        .onChange(of: core.bookmarks[panelId]?.isCompleted) { _, _ in scheduleDisplayRefresh() }
        .onChange(of: chips) { _, _ in refreshDisplayImmediately() }
        .onChange(of: chipLogic) { _, _ in refreshDisplayImmediately() }
        .onChange(of: serviceId) { _, _ in
            loadPanelHistoryRuns()
            isFollowing = true
            refreshDisplayImmediately()
        }
        .onChange(of: projectId) { _, _ in
            loadPanelHistoryRuns()
            isFollowing = true
            refreshDisplayImmediately()
        }
        .onChange(of: core.viewingRunId) { _, _ in
            isFollowing = true
            refreshDisplayImmediately()
        }
        .onChange(of: core.logRulesByProjectId) { _, _ in scheduleDisplayRefresh() }
        .onDisappear {
            displayRefreshTask?.cancel()
            historyLoadTask?.cancel()
        }
    }

    private func loadPanelHistoryRuns() {
        historyLoadTask?.cancel()
        let sid = serviceId
        historyLoadTask = Task {
            let runs = await core.fetchHistoryRuns(serviceId: sid)
            guard !Task.isCancelled else { return }
            panelHistoryRuns = runs
        }
    }

    /// Coalesce bursty log updates (e.g. multiple panels × high log rate) into one refresh per frame window.
    private func scheduleDisplayRefresh() {
        displayRefreshTask?.cancel()
        displayRefreshTask = Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(32))
            guard !Task.isCancelled else { return }
            cachedDisplay = makeLogDisplay()
            pinToBottomIfFollowing()
        }
    }

    private func refreshDisplayImmediately() {
        displayRefreshTask?.cancel()
        MainRunLoop.deferred {
            cachedDisplay = makeLogDisplay()
            pinToBottomIfFollowing()
        }
    }

    private func pinToBottomIfFollowing() {
        guard isFollowing, let lastId = cachedDisplay.items.last?.id else { return }
        suppressFollowGeometryUntil = Date().addingTimeInterval(0.4)
        newLogCount = 0
        scrollAnchorId = lastId
        scheduleScrollRetries(lastId: lastId)
    }

    private func applyScrollFollowState(distanceFromBottom: CGFloat, items: [LogDisplayItem]) {
        guard Date() >= suppressFollowGeometryUntil else { return }
        let wasFollowing = isFollowing
        isFollowing = distanceFromBottom < 50
        if isFollowing {
            newLogCount = 0
            if !wasFollowing, let last = items.last {
                scrollAnchorId = last.id
            }
        }
    }

    private func applyItemsCountChange(oldCount: Int, newCount: Int, items: [LogDisplayItem]) {
        if isFollowing {
            newLogCount = 0
            if let last = items.last {
                suppressFollowGeometryUntil = Date().addingTimeInterval(0.25)
                scrollAnchorId = last.id
                if oldCount == 0, newCount > 0 {
                    scheduleScrollRetries(lastId: last.id)
                }
            }
        } else {
            newLogCount += max(0, newCount - oldCount)
        }
    }

    private func scheduleScrollRetries(lastId: UUID) {
        Task { @MainActor in
            for delayMs in [50, 120, 250] {
                try? await Task.sleep(for: .milliseconds(delayMs))
                guard isFollowing else { return }
                scrollAnchorId = cachedDisplay.items.last?.id ?? lastId
            }
        }
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

            if serviceId == nil {
                Text("请先选择服务")
            } else if !panelHistoryRuns.isEmpty {
                Divider()
                ForEach(panelHistoryRuns) { run in
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
        .onAppear { loadPanelHistoryRuns() }
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

    private var syncToggle: some View {
        let inSync = core.syncGroupPanelIds.contains(panelId)
        return Button {
            core.toggleSyncGroup(panelId: panelId, serviceId: serviceId)
        } label: {
            HStack(spacing: 3) {
                Image(systemName: inSync ? "checkmark.square.fill" : "square")
                    .font(.system(size: 11))
                    .foregroundColor(inSync ? Theme.accent : .secondary)
                Text("同步")
                    .font(.system(size: 11))
                    .foregroundColor(inSync ? Theme.accent : .secondary)
            }
        }
        .buttonStyle(.plain)
        .disabled(serviceId == nil)
        .help(serviceId == nil ? "请先选择服务再加入同步组" : (inSync ? "退出同步录制组" : "加入同步录制组"))
    }

    private var bookmarkControl: some View {
        let inSync = core.syncGroupPanelIds.contains(panelId)
        return HStack(spacing: 6) {
            syncToggle
            Group {
            if let bm = bookmark, bm.isCompleted {
                HStack(spacing: 6) {
                    Button {
                        let text = inSync
                            ? core.syncBookmarkFormattedText()
                            : bm.formattedText
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help(inSync ? "复制所有同步分栏日志（按服务分块）" : "复制标记区间日志")

                    Button {
                        if inSync {
                            exportSyncBookmark()
                        } else {
                            exportBookmark(bm)
                        }
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help(inSync ? "导出所有同步分栏日志（按服务分块）" : "导出标记区间日志")

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
                        if core.syncGroupIsRecording && inSync {
                            core.endSyncBookmark()
                        } else {
                            core.endBookmark(panelId: panelId)
                        }
                    } label: {
                        Image(systemName: "stop.circle.fill")
                            .font(.system(size: 14))
                            .foregroundColor(.red)
                    }
                    .buttonStyle(.plain)
                    .help(core.syncGroupIsRecording && inSync ? "同步结束所有分栏书签 (⌘⇧B)" : "结束书签标记 (⌘⇧B)")
                    .keyboardShortcut("b", modifiers: [.command, .shift])
                }
            } else {
                Button {
                    if inSync {
                        core.startSyncBookmark()
                    } else {
                        core.startBookmark(panelId: panelId, serviceId: serviceId)
                    }
                } label: {
                    Image(systemName: "record.circle")
                        .font(.system(size: 14))
                        .foregroundColor(inSync ? .blue : .green)
                }
                .buttonStyle(.plain)
                .help(inSync ? "同步开始所有分栏书签 (⌘⇧B)" : "开始书签标记 (⌘⇧B)")
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
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

    private func exportSyncBookmark() {
        let text = core.syncBookmarkFormattedText()
        guard !text.isEmpty else { return }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "superdev-sync-\(Int(Date().timeIntervalSince1970)).log"
        panel.allowedContentTypes = [.plainText]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task.detached {
                try? text.write(to: url, atomically: true, encoding: .utf8)
            }
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
               let summary = panelHistoryRuns.first(where: { $0.runId == runId }) {
                return summary.startTime
            }
            return firstEntry?.timestamp
        }()
        let entryCount = display.items.filter { if case .entry = $0 { true } else { false } }.count
        return (startTime, entryCount)
    }

    // MARK: - Log list

    private func logList(items: [LogDisplayItem]) -> some View {
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
        .scrollPosition(id: $scrollAnchorId, anchor: .bottom)
        .defaultScrollAnchor(.bottom)
        .background(Theme.bgSecondary)
        .onScrollGeometryChange(for: CGFloat.self) { geo in
            geo.contentSize.height - geo.containerSize.height - geo.contentOffset.y
        } action: { _, distanceFromBottom in
            MainRunLoop.deferred {
                applyScrollFollowState(distanceFromBottom: distanceFromBottom, items: items)
            }
        }
        .onChange(of: items.count) { oldCount, newCount in
            MainRunLoop.deferred {
                applyItemsCountChange(oldCount: oldCount, newCount: newCount, items: items)
            }
        }
        .overlay(alignment: .bottomTrailing) {
            if !isFollowing && newLogCount > 0 {
                Button {
                    isFollowing = true
                    newLogCount = 0
                    if let last = items.last {
                        suppressFollowGeometryUntil = Date().addingTimeInterval(0.4)
                        scrollAnchorId = last.id
                        scheduleScrollRetries(lastId: last.id)
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
            pinToBottomIfFollowing()
        }
        .onAppear {
            isFollowing = true
            newLogCount = 0
            if let last = items.last {
                scrollAnchorId = last.id
                scheduleScrollRetries(lastId: last.id)
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
                .layoutPriority(1)
                .frame(maxWidth: .infinity, alignment: .leading)

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
            .fixedSize(horizontal: false, vertical: true)

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
