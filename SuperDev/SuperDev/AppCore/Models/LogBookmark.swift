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
    /// 绑定的服务，只锁定该服务的日志；nil 表示不过滤（未选择服务时不应开启书签）
    let serviceId: UUID?
    var startTime: Date?
    var endTime: Date?
    var lockedLogs: [LogEntry] = []

    init(panelId: UUID, serviceId: UUID?) {
        self.panelId = panelId
        self.serviceId = serviceId
    }

    /// 已开始且尚未结束。
    var isActive: Bool { startTime != nil && endTime == nil }

    /// 已开始且已结束。
    var isCompleted: Bool { startTime != nil && endTime != nil }

    /// 仅当 isActive 且 entry 属于绑定服务时追加。
    mutating func appendLog(_ entry: LogEntry) {
        guard isActive else { return }
        if let sid = serviceId, entry.serviceId != sid { return }
        lockedLogs.append(entry)
    }

    /// 格式化所有锁定日志为纯文本，用于复制/导出。
    var formattedText: String {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm:ss"
        formatter.locale = Locale(identifier: "en_US_POSIX")
        return lockedLogs.map { entry in
            let time = formatter.string(from: entry.timestamp)
            return "\(time) [\(entry.serviceName)] \(entry.level.rawValue) \(entry.message)"
        }.joined(separator: "\n")
    }
}
