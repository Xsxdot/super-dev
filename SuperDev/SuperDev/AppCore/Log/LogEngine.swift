// Package LogEngine provides log line parsing and deduplication for SuperDev.
//
// 职责：
//   - 将进程输出的原始行解析为 LogEntry（含级别检测）
//   - 对相邻重复日志进行折叠（dedup），减少界面噪音
//   - 对日志行进行规范化，去除时间戳等可变部分，供 dedup 对比使用
//
// 边界：
//   - 不持有状态（无缓冲区）；调用方负责维护 entries 列表
//   - 不直接操作 DB，持久化由 LogStore 负责
//   - 所有公开方法均无副作用，便于单元测试
//   - 显式标注 nonisolated 以避免 SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor 的隐式注入，
//     该类为纯计算类，无需 actor 隔离，可从任意并发上下文调用
import Foundation

// LogEngine 负责日志行的解析、规范化与去重折叠。
//
// 无状态设计：每个 LogEngine 仅关联一个 runId，
// 所有解析/去重逻辑均为纯函数，不持有内部缓冲区。
// 使用 nonisolated 显式声明不需要 MainActor 隔离，确保可从任意并发上下文安全调用。
final class LogEngine: @unchecked Sendable {

    // 当前运行 ID，写入每条 LogEntry，用于按 run 过滤日志。
    nonisolated let runId: UUID

    // init 创建一个与指定 runId 关联的 LogEngine。
    //
    // 参数：
    //   - runId: 本次进程运行的唯一标识
    nonisolated init(runId: UUID) {
        self.runId = runId
    }

    // nonisolated deinit 避免 SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor 在 Xcode 26 beta
    // 中将 deinit 隐式绑定到 MainActor，导致释放时尝试 task-local executor hop 而崩溃。
    nonisolated deinit {}

    // parseLine 将一条原始日志行解析为 LogEntry。
    //
    // 参数：
    //   - line:        原始日志行文本
    //   - serviceId:   所属服务的 UUID
    //   - serviceName: 所属服务名称（用于展示和过滤）
    //
    // 返回：填充好所有字段的 LogEntry（repeatCount 默认为 1）
    nonisolated func parseLine(_ line: String, serviceId: UUID, serviceName: String) -> LogEntry {
        let level = detectLevel(in: line)
        let normalized = normalize(line)
        return LogEntry(
            serviceId: serviceId,
            serviceName: serviceName,
            level: level,
            message: line,
            normalizedMessage: normalized,
            runId: runId
        )
    }

    // ingest 将 entry 写入 entries 列表，若末尾条目与其来自同一服务且规范化消息相同，
    // 则折叠（repeatCount +1）而非追加新条目，以减少界面重复噪音。
    //
    // 参数：
    //   - entry:   待写入的日志条目
    //   - entries: 当前日志列表（inout，可能被修改）
    nonisolated func ingest(_ entry: LogEntry, into entries: inout [LogEntry]) {
        if var last = entries.last,
           last.serviceId == entry.serviceId,
           last.normalizedMessage == entry.normalizedMessage {
            // 相邻重复：折叠到最后一条，更新时间戳和计数
            last.repeatCount += 1
            last.timestamp = entry.timestamp
            entries[entries.count - 1] = last
        } else {
            entries.append(entry)
        }
    }

    // normalize 对原始日志行进行规范化，剥离可变部分（时间戳、数字 ID、IP 等），
    // 以便不同时刻产生的相同语义日志能命中去重逻辑。
    //
    // 参数：
    //   - line: 原始日志行文本
    //
    // 返回：规范化后的字符串
    nonisolated func normalize(_ line: String) -> String {
        var result = line

        // 剥离行首 HH:MM:SS[.fff] 时间戳（如 "10:23:01 ERROR ..."）
        result = result.replacingOccurrences(
            of: #"^\d{2}:\d{2}:\d{2}(\.\d+)?\s*"#,
            with: "",
            options: .regularExpression
        )

        // 剥离行首 ISO 日期时间前缀（如 "2024-01-01 10:00:00 ERROR ..."）
        result = result.replacingOccurrences(
            of: #"^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?\s*"#,
            with: "",
            options: .regularExpression
        )

        // 将 key=123 形式的数字 ID 替换为通配符（如 uid=42 → uid=*）
        result = result.replacingOccurrences(
            of: #"=\d+"#,
            with: "=*",
            options: .regularExpression
        )

        // 将 IPv4:port 替换为 *:*（避免端口/地址变化导致去重失效）
        result = result.replacingOccurrences(
            of: #"\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+"#,
            with: "*:*",
            options: .regularExpression
        )

        return result.trimmingCharacters(in: .whitespaces)
    }

    // MARK: - Private

    // detectLevel 通过关键字匹配推断日志级别。
    // 匹配顺序：error > warn > debug > info（默认）。
    // 默认返回 .info 而非 .unknown，以减少典型开发日志中的噪音标注。
    nonisolated private func detectLevel(in line: String) -> LogLevel {
        let upper = line.uppercased()

        if upper.contains("ERROR") || upper.contains("FATAL") || upper.contains("CRITICAL") {
            return .error
        }
        if upper.contains("WARN") || upper.contains("WARNING") {
            return .warn
        }
        if upper.contains("DEBUG") || upper.contains("TRACE") {
            return .debug
        }

        // 默认为 info：避免将普通输出标记为 unknown 造成过多视觉噪音
        return .info
    }
}
