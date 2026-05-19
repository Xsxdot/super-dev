// PanelLayout 描述日志面板的递归分割树。
//
// 职责：
//   - 表示单个叶子面板（leaf）或水平/垂直分割（split）
//   - 提供不可变树的变异辅助方法
//
// 边界：
//   - 纯数据模型，不持有 SwiftUI 状态
//   - 持久化由调用方负责（编码为 JSON 写入 UserDefaults）
import Foundation
import SwiftUI

indirect enum PanelLayout: Codable, Identifiable, Equatable {
    case leaf(id: UUID, serviceId: UUID?)
    case split(id: UUID, axis: Axis, ratio: CGFloat, first: PanelLayout, second: PanelLayout)

    var id: UUID {
        switch self {
        case .leaf(let id, _): return id
        case .split(let id, _, _, _, _): return id
        }
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case type, id, serviceId, axis, ratio, first, second
    }

    private enum LayoutType: String, Codable {
        case leaf, split
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let type = try c.decode(LayoutType.self, forKey: .type)
        let id = try c.decode(UUID.self, forKey: .id)
        switch type {
        case .leaf:
            let serviceId = try c.decodeIfPresent(UUID.self, forKey: .serviceId)
            self = .leaf(id: id, serviceId: serviceId)
        case .split:
            let axis = try c.decode(Axis.self, forKey: .axis)
            let ratio = try c.decode(CGFloat.self, forKey: .ratio)
            let first = try c.decode(PanelLayout.self, forKey: .first)
            let second = try c.decode(PanelLayout.self, forKey: .second)
            self = .split(id: id, axis: axis, ratio: ratio, first: first, second: second)
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .leaf(let id, let serviceId):
            try c.encode(LayoutType.leaf, forKey: .type)
            try c.encode(id, forKey: .id)
            try c.encodeIfPresent(serviceId, forKey: .serviceId)
        case .split(let id, let axis, let ratio, let first, let second):
            try c.encode(LayoutType.split, forKey: .type)
            try c.encode(id, forKey: .id)
            try c.encode(axis, forKey: .axis)
            try c.encode(ratio, forKey: .ratio)
            try c.encode(first, forKey: .first)
            try c.encode(second, forKey: .second)
        }
    }

    // MARK: - Tree mutations

    /// 把 id 对应的叶子替换为一个分割节点，原叶子保留在 originalSide，新面板在另一侧。
    mutating func splitLeaf(id leafId: UUID, axis: Axis, newServiceId: UUID?, newSide: SplitSide) {
        switch self {
        case .leaf(let id, let serviceId):
            guard id == leafId else { return }
            let newLeaf = PanelLayout.leaf(id: UUID(), serviceId: newServiceId)
            let original = PanelLayout.leaf(id: id, serviceId: serviceId)
            switch newSide {
            case .first:
                self = .split(id: UUID(), axis: axis, ratio: 0.5, first: newLeaf, second: original)
            case .second:
                self = .split(id: UUID(), axis: axis, ratio: 0.5, first: original, second: newLeaf)
            }
        case .split(let id, let axis2, let ratio, var first, var second):
            // UUIDs are unique so at most one branch will mutate; both are visited for simplicity.
            first.splitLeaf(id: leafId, axis: axis, newServiceId: newServiceId, newSide: newSide)
            second.splitLeaf(id: leafId, axis: axis, newServiceId: newServiceId, newSide: newSide)
            self = .split(id: id, axis: axis2, ratio: ratio, first: first, second: second)
        }
    }

    /// 把 id 对应的叶子的服务替换为 newServiceId（不分割）。
    mutating func replaceService(panelId: UUID, newServiceId: UUID?) {
        switch self {
        case .leaf(let id, _):
            if id == panelId { self = .leaf(id: id, serviceId: newServiceId) }
        case .split(let id, let axis, let ratio, var first, var second):
            first.replaceService(panelId: panelId, newServiceId: newServiceId)
            second.replaceService(panelId: panelId, newServiceId: newServiceId)
            self = .split(id: id, axis: axis, ratio: ratio, first: first, second: second)
        }
    }

    /// 从树中移除指定叶子，兄弟节点提升替代父节点。
    /// 如果根节点本身是目标叶子则不做任何事（至少保留一个面板）。
    mutating func removeLeaf(id leafId: UUID) {
        guard case .split(_, _, _, let first, let second) = self else { return }
        if case .leaf(let fId, _) = first, fId == leafId {
            self = second; return
        }
        if case .leaf(let sId, _) = second, sId == leafId {
            self = first; return
        }
        if case .split(let id, let axis, let ratio, var f, var s) = self {
            f.removeLeaf(id: leafId)
            s.removeLeaf(id: leafId)
            self = .split(id: id, axis: axis, ratio: ratio, first: f, second: s)
        }
    }

    // MARK: - Query

    /// 收集所有叶子节点的 panelId。
    var allLeafIds: [UUID] {
        switch self {
        case .leaf(let id, _): return [id]
        case .split(_, _, _, let first, let second): return first.allLeafIds + second.allLeafIds
        }
    }
}

enum SplitSide: String, Codable {
    case first, second
}

// Axis Codable 扩展——SwiftUI.Axis 默认不遵循 Codable，此处补充支持。
extension Axis: @retroactive Codable {
    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(Int.self)
        // SwiftUI.Axis is @frozen with .horizontal == 0, .vertical == 1 (stable, matches RawValue)
        self = raw == 0 ? .horizontal : .vertical
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(self == .horizontal ? 0 : 1)
    }
}
