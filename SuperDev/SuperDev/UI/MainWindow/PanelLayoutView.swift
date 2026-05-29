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
import Foundation
import SwiftUI
import UniformTypeIdentifiers

struct PanelLayoutView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout
    @Binding var focusedPanelId: UUID?

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
                LeafPanelView(layout: node, focusedPanelId: $focusedPanelId, onClose: onClose)
                    .environmentObject(core)
            )
        case .split(_, let axis, _, _, _):
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

private enum DropEdge: Equatable {
    case left, right, top, bottom, center
}

/// 根据落点划分拖放区域：中央替换当前面板，四边分栏。
private func panelDropEdge(at location: CGPoint, size: CGSize) -> DropEdge {
    let w = size.width
    let h = size.height
    guard w > 0, h > 0 else { return .center }

    let innerW = w * 0.6
    let innerH = h * 0.6
    let innerRect = CGRect(x: (w - innerW) / 2, y: (h - innerH) / 2, width: innerW, height: innerH)
    if innerRect.contains(location) {
        return .center
    }

    let edgeFraction: CGFloat = 0.20
    if location.x < w * edgeFraction { return .left }
    if location.x > w * (1 - edgeFraction) { return .right }
    if location.y < h * edgeFraction { return .top }
    if location.y > h * (1 - edgeFraction) { return .bottom }
    return .center
}

private struct ServicePanelDropDelegate: DropDelegate {
    let panelSize: CGSize
    @Binding var dropHighlight: DropEdge?
    let onDrop: (UUID, DropEdge) -> Void

    func validateDrop(info: DropInfo) -> Bool {
        info.hasItemsConforming(to: [.plainText])
    }

    func dropEntered(info: DropInfo) {
        dropHighlight = panelDropEdge(at: info.location, size: panelSize)
    }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        dropHighlight = panelDropEdge(at: info.location, size: panelSize)
        return DropProposal(operation: .copy)
    }

    func dropExited(info: DropInfo) {
        dropHighlight = nil
    }

    func performDrop(info: DropInfo) -> Bool {
        let edge = panelDropEdge(at: info.location, size: panelSize)
        dropHighlight = nil
        let providers = info.itemProviders(for: [.plainText])
        guard let provider = providers.first else { return false }
        provider.loadObject(ofClass: NSString.self) { object, _ in
            guard let uuidString = object as? String,
                  let serviceId = UUID(uuidString: uuidString) else { return }
            DispatchQueue.main.async {
                onDrop(serviceId, edge)
            }
        }
        return true
    }
}

private struct LeafPanelView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout
    @Binding var focusedPanelId: UUID?
    let onClose: (() -> Void)?
    @State private var panelSize: CGSize = .zero
    @State private var dropHighlight: DropEdge?

    private var panelId: UUID {
        if case .leaf(let id, _, _) = layout { return id }
        return UUID()
    }

    private var serviceId: UUID? {
        if case .leaf(_, let sid, _) = layout { return sid }
        return nil
    }

    private var projectId: UUID? {
        if case .leaf(_, _, let pid) = layout { return pid }
        return nil
    }

    private var project: Project? {
        if let projectId, let p = core.project(id: projectId) { return p }
        return core.project(forServiceId: serviceId)
    }

    var body: some View {
        VStack(spacing: 0) {
            panelHeader
            LogPanelView(
                panelId: panelId,
                serviceId: serviceId,
                projectId: projectId,
                project: project
            )
            .environmentObject(core)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .contentShape(Rectangle())
        .onTapGesture {
            MainRunLoop.deferred { focusedPanelId = panelId }
        }
        .onGeometryChange(for: CGSize.self) { proxy in proxy.size } action: { _, size in
            guard size != panelSize else { return }
            MainRunLoop.deferred { panelSize = size }
        }
        .onDrop(
            of: [.plainText],
            delegate: ServicePanelDropDelegate(
                panelSize: panelSize,
                dropHighlight: $dropHighlight,
                onDrop: { serviceId, edge in
                    scheduleDrop(serviceId: serviceId, edge: edge)
                }
            )
        )
        .overlay {
            if let dropHighlight, panelSize.width > 0, panelSize.height > 0 {
                dropHighlightOverlay(edge: dropHighlight, size: panelSize)
                    .allowsHitTesting(false)
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
        if let sid = serviceId {
            for project in core.projects {
                if let svc = project.services.first(where: { $0.id == sid }) {
                    return svc.name
                }
            }
        }
        if let project {
            return "\(project.name) · 全部服务"
        }
        return "未选择"
    }

    private func scheduleDrop(serviceId: UUID, edge: DropEdge) {
        MainRunLoop.deferred {
            handleDrop(serviceId: serviceId, edge: edge)
        }
    }

    @ViewBuilder
    private func dropHighlightOverlay(edge: DropEdge, size: CGSize) -> some View {
        let rect = dropHighlightRect(edge: edge, size: size)
        RoundedRectangle(cornerRadius: 4)
            .fill(Color.accentColor.opacity(0.25))
            .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.accentColor, lineWidth: 2))
            .frame(width: rect.width, height: rect.height)
            .position(x: rect.midX, y: rect.midY)
    }

    private func dropHighlightRect(edge: DropEdge, size: CGSize) -> CGRect {
        let w = size.width
        let h = size.height
        let edgeFraction: CGFloat = 0.20
        switch edge {
        case .left:
            return CGRect(x: 0, y: 0, width: w * edgeFraction, height: h)
        case .right:
            return CGRect(x: w * (1 - edgeFraction), y: 0, width: w * edgeFraction, height: h)
        case .top:
            return CGRect(x: 0, y: 0, width: w, height: h * edgeFraction)
        case .bottom:
            return CGRect(x: 0, y: h * (1 - edgeFraction), width: w, height: h * edgeFraction)
        case .center:
            let innerW = w * 0.6
            let innerH = h * 0.6
            return CGRect(x: (w - innerW) / 2, y: (h - innerH) / 2, width: innerW, height: innerH)
        }
    }

    private func handleDrop(serviceId droppedId: UUID, edge: DropEdge) {
        guard case .leaf(let id, _, _) = layout else { return }
        let droppedProjectId = core.project(forServiceId: droppedId)?.id
        switch edge {
        case .left:
            layout.splitLeaf(
                id: id, axis: .horizontal, newServiceId: droppedId, newProjectId: droppedProjectId, newSide: .first
            )
        case .right:
            layout.splitLeaf(
                id: id, axis: .horizontal, newServiceId: droppedId, newProjectId: droppedProjectId, newSide: .second
            )
        case .top:
            layout.splitLeaf(
                id: id, axis: .vertical, newServiceId: droppedId, newProjectId: droppedProjectId, newSide: .first
            )
        case .bottom:
            layout.splitLeaf(
                id: id, axis: .vertical, newServiceId: droppedId, newProjectId: droppedProjectId, newSide: .second
            )
        case .center:
            layout.replaceScope(panelId: id, serviceId: droppedId, projectId: droppedProjectId)
            focusedPanelId = id
            core.returnToLiveLogs()
        }
    }
}
