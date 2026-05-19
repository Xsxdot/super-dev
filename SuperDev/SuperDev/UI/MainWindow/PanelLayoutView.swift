// PanelLayoutView 递归渲染 PanelLayout 树。
//
// 职责：
//   - leaf 节点渲染 LogPanelView + 拖放覆盖层
//   - split 节点渲染 HSplitView 或 VSplitView，递归渲染子节点
//   - 处理从侧边栏拖入服务 UUID 的 drop 事件
//
// 边界：
//   - 不直接修改 AppCore，所有布局变更通过 binding 回调完成
//   - drop zone 逻辑纯 SwiftUI，不依赖 AppKit
import SwiftUI

struct PanelLayoutView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout

    var body: some View {
        layoutView(node: $layout, onClose: nil)
            .focusable(false)
    }

    // 使用 AnyView 打破递归 opaque return type 的编译限制
    // onClose: 父节点提供的关闭回调；nil 表示当前节点是根节点，不可关闭
    private func layoutView(node: Binding<PanelLayout>, onClose: (() -> Void)?) -> AnyView {
        switch node.wrappedValue {
        case .leaf:
            return AnyView(
                LeafPanelView(layout: node, onClose: onClose)
                    .environmentObject(core)
            )
        case .split(_, let axis, _, _, _):
            // 子节点关闭时，将父 split 节点替换为另一个孩子（兄弟提升）
            let closeFirst: () -> Void = {
                if case .split(_, _, _, _, let sibling) = node.wrappedValue {
                    node.wrappedValue = sibling
                }
            }
            let closeSecond: () -> Void = {
                if case .split(_, _, _, let sibling, _) = node.wrappedValue {
                    node.wrappedValue = sibling
                }
            }
            if axis == .horizontal {
                return AnyView(
                    HSplitView {
                        layoutView(node: firstBinding(node), onClose: closeFirst)
                        layoutView(node: secondBinding(node), onClose: closeSecond)
                    }
                )
            } else {
                return AnyView(
                    VSplitView {
                        layoutView(node: firstBinding(node), onClose: closeFirst)
                        layoutView(node: secondBinding(node), onClose: closeSecond)
                    }
                )
            }
        }
    }

    private func firstBinding(_ node: Binding<PanelLayout>) -> Binding<PanelLayout> {
        Binding(
            get: {
                if case .split(_, _, _, let f, _) = node.wrappedValue { return f }
                return node.wrappedValue
            },
            set: { newFirst in
                if case .split(let id, let axis, let ratio, _, let s) = node.wrappedValue {
                    node.wrappedValue = .split(id: id, axis: axis, ratio: ratio, first: newFirst, second: s)
                }
            }
        )
    }

    private func secondBinding(_ node: Binding<PanelLayout>) -> Binding<PanelLayout> {
        Binding(
            get: {
                if case .split(_, _, _, _, let s) = node.wrappedValue { return s }
                return node.wrappedValue
            },
            set: { newSecond in
                if case .split(let id, let axis, let ratio, let f, _) = node.wrappedValue {
                    node.wrappedValue = .split(id: id, axis: axis, ratio: ratio, first: f, second: newSecond)
                }
            }
        )
    }
}

// MARK: - LeafPanelView

// DropEdge 显式枚举所有情况，center 不再用 Optional nil 表示，避免 nil==nil 误高亮
private enum DropEdge: Equatable {
    case left, right, top, bottom, center
}

private struct LeafPanelView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout
    /// 父节点提供的关闭回调；nil 时隐藏关闭按钮（单面板根节点不可关闭）
    let onClose: (() -> Void)?
    @State private var dropHighlight: DropEdge? = nil

    private var panelId: UUID {
        if case .leaf(let id, _) = layout { return id }
        return UUID()
    }

    private var serviceId: UUID? {
        if case .leaf(_, let sid) = layout { return sid }
        return nil
    }

    private var project: Project? {
        core.project(forServiceId: serviceId)
    }

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                panelHeader
                LogPanelView(panelId: panelId, serviceId: serviceId, project: project)
                    .environmentObject(core)
            }

            // 拖放覆盖层
            GeometryReader { geo in
                let w = geo.size.width
                let h = geo.size.height
                let edgeFraction: CGFloat = 0.20

                ZStack {
                    // 左边缘
                    dropZone(edge: .left)
                        .frame(width: w * edgeFraction, height: h)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    // 右边缘
                    dropZone(edge: .right)
                        .frame(width: w * edgeFraction, height: h)
                        .frame(maxWidth: .infinity, alignment: .trailing)
                    // 上边缘
                    dropZone(edge: .top)
                        .frame(width: w, height: h * edgeFraction)
                        .frame(maxHeight: .infinity, alignment: .top)
                    // 下边缘
                    dropZone(edge: .bottom)
                        .frame(width: w, height: h * edgeFraction)
                        .frame(maxHeight: .infinity, alignment: .bottom)
                    // 中心（替换服务）
                    dropZone(edge: .center)
                        .frame(width: w * 0.6, height: h * 0.6)
                }
            }
        }
    }

    private var panelHeader: some View {
        HStack {
            Text(headerTitle)
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(.secondary)
                .lineLimit(1)
            Spacer()
            if let onClose {
                Button(action: onClose) {
                    Image(systemName: "xmark")
                        .font(.system(size: 9))
                }
                .buttonStyle(.plain)
                .help("关闭此面板")
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private var headerTitle: String {
        guard let sid = serviceId else { return "未选择" }
        for project in core.projects {
            if let svc = project.services.first(where: { $0.id == sid }) {
                return svc.name
            }
        }
        return "未选择"
    }

    @ViewBuilder
    private func dropZone(edge: DropEdge) -> some View {
        let isHighlighted = dropHighlight == edge
        // contentShape 让 drop 可命中，allowsHitTesting 只在高亮时开启，平时让点击穿透
        Color.clear
            .contentShape(Rectangle())
            .allowsHitTesting(isHighlighted)
            .overlay(
                isHighlighted
                    ? RoundedRectangle(cornerRadius: 4)
                        .fill(Color.accentColor.opacity(0.25))
                        .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.accentColor, lineWidth: 2))
                        .allowsHitTesting(false)
                    : nil
            )
            .dropDestination(for: String.self) { items, _ in
                guard let uuidString = items.first,
                      let droppedServiceId = UUID(uuidString: uuidString) else { return false }
                handleDrop(serviceId: droppedServiceId, edge: edge)
                dropHighlight = nil
                return true
            } isTargeted: { targeted in
                if targeted {
                    dropHighlight = edge
                } else if dropHighlight == edge {
                    dropHighlight = nil
                }
            }
    }

    private func handleDrop(serviceId droppedId: UUID, edge: DropEdge) {
        guard case .leaf(let id, _) = layout else { return }
        switch edge {
        case .left:
            layout.splitLeaf(id: id, axis: .horizontal, newServiceId: droppedId, newSide: .first)
        case .right:
            layout.splitLeaf(id: id, axis: .horizontal, newServiceId: droppedId, newSide: .second)
        case .top:
            layout.splitLeaf(id: id, axis: .vertical, newServiceId: droppedId, newSide: .first)
        case .bottom:
            layout.splitLeaf(id: id, axis: .vertical, newServiceId: droppedId, newSide: .second)
        case .center:
            layout.replaceService(panelId: id, newServiceId: droppedId)
        }
    }
}
