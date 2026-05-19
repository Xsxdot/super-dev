// LogBookmark 保存单个面板的日志标记区间。
//
// 职责：
//   - 记录开始/结束时间戳
//   - 独立于主缓冲区锁定日志条目
//   - 提供格式化文本用于复制/导出
//
// 边界：
//   - 纯内存，不持久化到磁盘
//   - 每个面板最多一个活跃书签
import Foundation

struct LogBookmark {
    let panelId: UUID
    var startTime: Date?
    var endTime: Date?
    var lockedLogs: [LogEntry] = []

    init(panelId: UUID) {
        self.panelId = panelId
    }

    /// 已开始且尚未结束。
    var isActive: Bool { startTime != nil && endTime == nil }

    /// 已开始且已结束。
    var isCompleted: Bool { startTime != nil && endTime != nil }

    /// 仅当 isActive 时追加日志；其余状态忽略。
    mutating func appendLog(_ entry: LogEntry) {
        guard isActive else { return }
        lockedLogs.append(entry)
    }

    /// 格式化所有锁定日志为纯文本，用于复制/导出。
    var formattedText: String {
        lockedLogs.map { entry in
            let time = entry.timestamp.formatted(.dateTime.hour().minute().second())
            return "\(time) [\(entry.serviceName)] \(entry.level.rawValue) \(entry.message)"
        }.joined(separator: "\n")
    }
}
