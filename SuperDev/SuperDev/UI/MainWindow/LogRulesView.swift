import SwiftUI

struct LogRulesView: View {
    @EnvironmentObject var core: AppCore
    @Environment(\.dismiss) private var dismiss

    let project: Project

    @State private var config: LogRulesConfig = LogRulesConfig()
    @State private var editingRule: LogRule?
    @State private var draftRule: LogRule?
    @State private var showEditor = false

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            rulesList
            Divider()
            footer
        }
        .frame(width: 520, height: 400)
        .onAppear { config = core.logRules(for: project) }
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("日志过滤规则")
                    .font(.headline)
                Text("\(project.name) · 日志保留天数在「设置」中全局配置")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            Spacer()
            Button("完成") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
        .padding()
    }

    private var rulesList: some View {
        List {
            if config.rules.isEmpty {
                Text("暂无规则。添加排除规则可过滤心跳包等背景噪音。")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            ForEach($config.rules) { $rule in
                ruleRow(rule: $rule)
            }
            .onDelete { indexSet in
                config.rules.remove(atOffsets: indexSet)
                persist()
            }
        }
    }

    private func ruleRow(rule: Binding<LogRule>) -> some View {
        HStack(spacing: 10) {
            Toggle("", isOn: rule.enabled)
                .labelsHidden()
                .onChange(of: rule.wrappedValue.enabled) { _, _ in persist() }

            VStack(alignment: .leading, spacing: 2) {
                Text(rule.wrappedValue.name.isEmpty ? "未命名规则" : rule.wrappedValue.name)
                    .font(.system(size: 13, weight: .medium))
                HStack(spacing: 6) {
                    typeBadge(rule.wrappedValue.type)
                    Text(keywordsPreview(rule.wrappedValue.keywords))
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
            }

            Spacer()

            Button {
                editingRule = rule.wrappedValue
                draftRule = nil
                showEditor = true
            } label: {
                Image(systemName: "pencil")
            }
            .buttonStyle(.plain)
        }
        .padding(.vertical, 2)
    }

    private func typeBadge(_ type: LogRule.RuleType) -> some View {
        Text(type == .include ? "包含" : "排除")
            .font(.system(size: 10, weight: .medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(type == .include ? Color.blue.opacity(0.15) : Color.orange.opacity(0.15))
            .foregroundColor(type == .include ? .blue : .orange)
            .cornerRadius(4)
    }

    private func keywordsPreview(_ keywords: [String]) -> String {
        let preview = keywords.prefix(3).joined(separator: ", ")
        if keywords.count > 3 { return preview + "…" }
        return preview.isEmpty ? "无关键词" : preview
    }

    private var footer: some View {
        HStack {
            Button {
                draftRule = LogRule(name: "新规则", type: .exclude, keywords: [], logic: .or)
                editingRule = nil
                showEditor = true
            } label: {
                Label("添加规则", systemImage: "plus")
            }
            Spacer()
        }
        .padding()
        .sheet(isPresented: $showEditor) {
            if let binding = bindingForEditingRule() {
                LogRuleEditorView(
                    rule: binding,
                    isNew: draftRule != nil
                ) {
                    if draftRule != nil {
                        config.rules.append(binding.wrappedValue)
                    }
                    persist()
                    closeEditor()
                } onCancel: {
                    closeEditor()
                }
            }
        }
    }

    private func bindingForEditingRule() -> Binding<LogRule>? {
        // Must read `self.draftRule` in get — `if let draftRule` would capture a stale copy.
        if draftRule != nil {
            return Binding(
                get: {
                    self.draftRule ?? LogRule(name: "新规则", type: .exclude, keywords: [], logic: .or)
                },
                set: { self.draftRule = $0 }
            )
        }
        guard let editingRule,
              let idx = config.rules.firstIndex(where: { $0.id == editingRule.id }) else { return nil }
        return $config.rules[idx]
    }

    private func closeEditor() {
        showEditor = false
        editingRule = nil
        draftRule = nil
    }

    private func persist() {
        try? core.saveLogRules(config, for: project)
    }
}

// MARK: - Rule editor

private struct LogRuleEditorView: View {
    @Binding var rule: LogRule
    let isNew: Bool
    let onSave: () -> Void
    let onCancel: () -> Void

    @State private var keywordInput: String = ""
    @State private var keywords: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(isNew ? "添加规则" : "编辑规则")
                .font(.headline)

            TextField("规则名称", text: $rule.name)

            Picker("类型", selection: $rule.type) {
                Text("排除").tag(LogRule.RuleType.exclude)
                Text("包含").tag(LogRule.RuleType.include)
            }
            .pickerStyle(.segmented)

            Picker("关键词逻辑", selection: $rule.logic) {
                Text("OR（任一匹配）").tag(LogRule.RuleLogic.or)
                Text("AND（全部匹配）").tag(LogRule.RuleLogic.and)
            }
            .pickerStyle(.segmented)

            VStack(alignment: .leading, spacing: 8) {
                Text("关键词")
                    .font(.caption)
                    .foregroundColor(.secondary)
                chipInputArea
            }

            HStack {
                Spacer()
                Button("取消", action: onCancel)
                Button("保存", action: {
                    commitPendingKeywordIfNeeded()
                    rule.keywords = keywords
                    onSave()
                })
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 400)
        .onAppear { keywords = rule.keywords }
    }

    private var chipInputArea: some View {
        VStack(alignment: .leading, spacing: 6) {
            FlowLayout(spacing: 6) {
                ForEach(keywords, id: \.self) { kw in
                    HStack(spacing: 4) {
                        Text(kw)
                            .font(.caption)
                        Button {
                            keywords.removeAll { $0 == kw }
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 8))
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.secondary.opacity(0.15))
                    .cornerRadius(4)
                }
            }
            TextField("输入关键词，回车添加", text: $keywordInput)
                .textFieldStyle(.roundedBorder)
                .onSubmit { addKeyword() }
        }
    }

    private func addKeyword() {
        let trimmed = keywordInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !keywords.contains(trimmed) else {
            keywordInput = ""
            return
        }
        keywords.append(trimmed)
        keywordInput = ""
    }

    /// Commits text still in the input field when the user saves without pressing Return.
    private func commitPendingKeywordIfNeeded() {
        let trimmed = keywordInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !keywords.contains(trimmed) else { return }
        keywords.append(trimmed)
        keywordInput = ""
    }
}

/// Simple horizontal flow layout for keyword chips.
private struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = arrange(proposal: proposal, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = arrange(proposal: proposal, subviews: subviews)
        for (index, position) in result.positions.enumerated() {
            subviews[index].place(at: CGPoint(x: bounds.minX + position.x, y: bounds.minY + position.y), proposal: .unspecified)
        }
    }

    private func arrange(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [CGPoint]) {
        let maxWidth = proposal.width ?? .infinity
        var positions: [CGPoint] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x + size.width > maxWidth, x > 0 {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            positions.append(CGPoint(x: x, y: y))
            rowHeight = max(rowHeight, size.height)
            x += size.width + spacing
        }

        return (CGSize(width: maxWidth, height: y + rowHeight), positions)
    }
}
