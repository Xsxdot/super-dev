import Foundation

/// Immutable snapshot for background log filtering (Sendable).
struct LogRulesSnapshot: Sendable {
    let serviceIdToProjectId: [UUID: UUID]
    /// Stable fallback when persisted logs reference service UUIDs from a prior config load.
    let serviceNameToProjectId: [String: UUID]
    let rulesByProjectId: [UUID: [LogRule]]
}

/// Stateless rule engine for log filtering. Defined as enum to prevent instantiation.
enum LogFilter {

    /// Returns whether an entry should be shown after applying enabled rules.
    static func passes(_ entry: LogEntry, rules: [LogRule]) -> Bool {
        let enabled = rules.filter(\.enabled)
        let excludes = enabled.filter { $0.type == .exclude }
        let includes = enabled.filter { $0.type == .include }

        for rule in excludes where matches(entry, rule: rule) {
            return false
        }

        if !includes.isEmpty {
            let anyMatch = includes.contains { matches(entry, rule: $0) }
            if !anyMatch { return false }
        }

        return true
    }

    static func apply(rules: [LogRule], to entries: [LogEntry]) -> [LogEntry] {
        entries.filter { passes($0, rules: rules) }
    }

    static func filterEntries(_ entries: [LogEntry], snapshot: LogRulesSnapshot) -> [LogEntry] {
        entries.filter { entry in
            let rules = rulesForEntry(entry, snapshot: snapshot)
            return passes(entry, rules: rules)
        }
    }

    static func rulesForEntry(_ entry: LogEntry, snapshot: LogRulesSnapshot) -> [LogRule] {
        let projectId = snapshot.serviceIdToProjectId[entry.serviceId]
            ?? snapshot.serviceNameToProjectId[entry.serviceName]
        guard let projectId else { return [] }
        return snapshot.rulesByProjectId[projectId] ?? []
    }

    // MARK: - Chip filtering (temporary UI filters)

    enum ChipType {
        case include
        case exclude
    }

    enum ChipLogic: Hashable {
        case and
        case or
    }

    static func passes(
        _ entry: LogEntry,
        includeChips: [String],
        excludeChips: [String],
        logic: ChipLogic
    ) -> Bool {
        let text = entry.message

        if !excludeChips.isEmpty {
            let excluded: Bool
            switch logic {
            case .and:
                excluded = excludeChips.allSatisfy { text.localizedCaseInsensitiveContains($0) }
            case .or:
                excluded = excludeChips.contains { text.localizedCaseInsensitiveContains($0) }
            }
            if excluded { return false }
        }

        if !includeChips.isEmpty {
            let included: Bool
            switch logic {
            case .and:
                included = includeChips.allSatisfy { text.localizedCaseInsensitiveContains($0) }
            case .or:
                included = includeChips.contains { text.localizedCaseInsensitiveContains($0) }
            }
            if !included { return false }
        }

        return true
    }

    // MARK: - Private

    private static func matches(_ entry: LogEntry, rule: LogRule) -> Bool {
        let keywords = rule.keywords.filter { !$0.isEmpty }
        guard !keywords.isEmpty else { return false }
        let text = entry.message

        switch rule.logic {
        case .and:
            return keywords.allSatisfy { text.localizedCaseInsensitiveContains($0) }
        case .or:
            return keywords.contains { text.localizedCaseInsensitiveContains($0) }
        }
    }
}
