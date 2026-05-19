import Foundation

struct RunSummary: Identifiable, Equatable {
    var id: UUID { runId }
    let runId: UUID
    let startTime: Date
    let logCount: Int
    let serviceNames: [String]
}
