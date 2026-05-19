import AppKit
import SwiftUI

/// Read-only selectable log message text with selection callbacks for filter actions.
struct SelectableLogText: NSViewRepresentable {
    let text: String
    let textColor: NSColor
    let onSelectionChange: (String?, CGRect?) -> Void

    func makeNSView(context: Context) -> WrappingLogTextView {
        let textView = WrappingLogTextView()
        textView.isEditable = false
        textView.isSelectable = true
        textView.drawsBackground = false
        textView.isRichText = false
        textView.textContainerInset = .zero
        textView.isHorizontallyResizable = false
        textView.isVerticallyResizable = true
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.textContainer?.lineFragmentPadding = 0
        textView.textContainer?.widthTracksTextView = true
        textView.textContainer?.lineBreakMode = .byCharWrapping
        textView.textContainer?.containerSize = NSSize(
            width: 0,
            height: CGFloat.greatestFiniteMagnitude
        )
        textView.font = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
        textView.textColor = textColor
        textView.string = text
        textView.delegate = context.coordinator
        textView.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return textView
    }

    func updateNSView(_ textView: WrappingLogTextView, context: Context) {
        let textChanged = textView.string != text
        if textChanged {
            textView.string = text
        }
        textView.textColor = textColor
        context.coordinator.onSelectionChange = onSelectionChange
        if textChanged {
            textView.noteWidthChanged()
        }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView: WrappingLogTextView, context: Context) -> CGSize? {
        let width = proposal.width ?? nsView.bounds.width
        guard width > 0, width.isFinite else { return nil }
        return nsView.sizeForWidth(width)
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(onSelectionChange: onSelectionChange)
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var onSelectionChange: (String?, CGRect?) -> Void

        init(onSelectionChange: @escaping (String?, CGRect?) -> Void) {
            self.onSelectionChange = onSelectionChange
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else {
                onSelectionChange(nil, nil)
                return
            }
            let range = textView.selectedRange()
            guard range.length > 0,
                  let selected = textView.string as NSString?,
                  range.location + range.length <= selected.length else {
                onSelectionChange(nil, nil)
                return
            }
            let text = selected.substring(with: range).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else {
                onSelectionChange(nil, nil)
                return
            }
            let rect = selectionRect(in: textView, range: range)
            onSelectionChange(text, rect)
        }

        private func selectionRect(in textView: NSTextView, range: NSRange) -> CGRect? {
            guard let layoutManager = textView.layoutManager,
                  let textContainer = textView.textContainer else { return nil }
            layoutManager.ensureLayout(for: textContainer)
            var rect = layoutManager.boundingRect(
                forGlyphRange: range,
                in: textContainer
            )
            rect.origin.x += textView.textContainerInset.width
            rect.origin.y += textView.textContainerInset.height
            return rect
        }
    }
}

/// NSTextView that reports wrapped text height to SwiftUI via intrinsicContentSize / sizeThatFits.
final class WrappingLogTextView: NSTextView {
    private var lastLaidOutWidth: CGFloat = 0

    func noteWidthChanged() {
        guard bounds.width > 0 else { return }
        lastLaidOutWidth = 0
        invalidateIntrinsicContentSize()
    }

    func sizeForWidth(_ width: CGFloat) -> CGSize {
        applyContainerWidth(width)
        guard let layoutManager, let textContainer else {
            return CGSize(width: width, height: 14)
        }
        layoutManager.ensureLayout(for: textContainer)
        let used = layoutManager.usedRect(for: textContainer)
        return CGSize(width: width, height: max(ceil(used.height), 14))
    }

    override var intrinsicContentSize: NSSize {
        let width = bounds.width
        guard width > 0 else {
            return NSSize(width: NSView.noIntrinsicMetric, height: NSView.noIntrinsicMetric)
        }
        let size = sizeForWidth(width)
        return NSSize(width: NSView.noIntrinsicMetric, height: size.height)
    }

    override func setFrameSize(_ newSize: NSSize) {
        let oldWidth = bounds.width
        super.setFrameSize(newSize)
        guard newSize.width > 0, abs(newSize.width - oldWidth) > 0.5 else { return }
        applyContainerWidth(newSize.width)
        if abs(newSize.width - lastLaidOutWidth) > 0.5 {
            lastLaidOutWidth = newSize.width
            invalidateIntrinsicContentSize()
        }
    }

    private func applyContainerWidth(_ width: CGFloat) {
        textContainer?.containerSize = NSSize(width: width, height: CGFloat.greatestFiniteMagnitude)
    }
}
