import Foundation

/// Splits pasted or typed text into individual keywords.
enum KeywordTokenizer {
    private static let separators = CharacterSet(charactersIn: ",\n\t;")

    static func split(_ text: String) -> [String] {
        text.components(separatedBy: separators)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }
}

/// Builds project-level rules from temporary filter chips.
enum LogChipRuleBuilder {
    static func makeRulesFromChips(name: String, chips: [FilterChip], logic: ChipLogic) -> [LogRule] {
        let baseName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedName = baseName.isEmpty ? "快捷过滤" : baseName
        let ruleLogic: LogRule.RuleLogic = logic == .and ? .and : .or

        let includeKeywords = chips.filter { $0.type == .include }.map(\.keyword)
        let excludeKeywords = chips.filter { $0.type == .exclude }.map(\.keyword)

        var rules: [LogRule] = []
        let hasBoth = !includeKeywords.isEmpty && !excludeKeywords.isEmpty

        if !includeKeywords.isEmpty {
            let ruleName = hasBoth ? "\(resolvedName) (包含)" : resolvedName
            rules.append(LogRule(
                name: ruleName,
                type: .include,
                keywords: includeKeywords,
                logic: ruleLogic,
                enabled: true
            ))
        }
        if !excludeKeywords.isEmpty {
            let ruleName = hasBoth ? "\(resolvedName) (排除)" : resolvedName
            rules.append(LogRule(
                name: ruleName,
                type: .exclude,
                keywords: excludeKeywords,
                logic: ruleLogic,
                enabled: true
            ))
        }
        return rules
    }

    static func isDuplicate(_ rule: LogRule, in existing: [LogRule]) -> Bool {
        let newKeywords = Set(rule.keywords.map { $0.lowercased() })
        return existing.contains { existing in
            existing.type == rule.type
                && existing.logic == rule.logic
                && Set(existing.keywords.map { $0.lowercased() }) == newKeywords
        }
    }
}
