import Foundation

struct LogRule: Codable, Identifiable, Equatable {
    let id: UUID
    var name: String
    var type: RuleType
    var keywords: [String]
    var logic: RuleLogic
    var enabled: Bool

    enum RuleType: String, Codable, CaseIterable {
        case include
        case exclude
    }

    enum RuleLogic: String, Codable, CaseIterable {
        case and
        case or
    }

    init(
        id: UUID = UUID(),
        name: String = "",
        type: RuleType = .exclude,
        keywords: [String] = [],
        logic: RuleLogic = .or,
        enabled: Bool = true
    ) {
        self.id = id
        self.name = name
        self.type = type
        self.keywords = keywords
        self.logic = logic
        self.enabled = enabled
    }
}

/// Project-level log filter rules stored in config.yaml.
/// Log retention is global (UserDefaults); only `rules` are persisted per project.
struct LogRulesConfig: Codable, Equatable {
    var rules: [LogRule]

    init(rules: [LogRule] = []) {
        self.rules = rules
    }
}
