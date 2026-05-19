import XCTest
import SwiftUI
@testable import SuperDev

final class PanelLayoutTests: XCTestCase {

    // MARK: - id

    func test_leaf_hasStableId() {
        let id = UUID()
        let leaf = PanelLayout.leaf(id: id, serviceId: nil)
        XCTAssertEqual(leaf.id, id)
    }

    // MARK: - replacing a leaf with a split

    func test_splitLeaf_horizontal_right_producesCorrectTree() {
        let leafId = UUID()
        let serviceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.splitLeaf(id: leafId, axis: .horizontal, newServiceId: serviceId, newSide: .second)
        guard case .split(_, let axis, let ratio, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.5, accuracy: 0.001)
        if case .leaf(let fId, _) = first { XCTAssertEqual(fId, leafId) } else { XCTFail("first should be original leaf") }
        if case .leaf(_, let sid) = second { XCTAssertEqual(sid, serviceId) } else { XCTFail("second should be new leaf") }
    }

    func test_splitLeaf_horizontal_left_newPanelIsFirst() {
        let leafId = UUID()
        let serviceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.splitLeaf(id: leafId, axis: .horizontal, newServiceId: serviceId, newSide: .first)
        guard case .split(_, _, _, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        if case .leaf(_, let sid) = first { XCTAssertEqual(sid, serviceId) } else { XCTFail("first should be new leaf") }
        if case .leaf(let fId, _) = second { XCTAssertEqual(fId, leafId) } else { XCTFail("second should be original leaf") }
    }

    // MARK: - replaceService

    func test_replaceService_updatesLeafServiceId() {
        let leafId = UUID()
        let newServiceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.replaceService(panelId: leafId, newServiceId: newServiceId)
        if case .leaf(_, let sid) = layout {
            XCTAssertEqual(sid, newServiceId)
        } else {
            XCTFail("Should remain a leaf")
        }
    }

    // MARK: - removeLeaf

    func test_removeLeaf_fromSplit_promotesOtherChild() {
        let leftId = UUID()
        let rightId = UUID()
        let leftLeaf = PanelLayout.leaf(id: leftId, serviceId: nil)
        let rightLeaf = PanelLayout.leaf(id: rightId, serviceId: nil)
        var layout = PanelLayout.split(id: UUID(), axis: .horizontal, ratio: 0.5, first: leftLeaf, second: rightLeaf)
        layout.removeLeaf(id: leftId)
        if case .leaf(let id, _) = layout { XCTAssertEqual(id, rightId) } else { XCTFail("right child should be promoted") }
    }

    func test_removeLeaf_deepNested_promotesCorrectly() {
        // Tree: split(split(leafA, leafB), leafC)
        let leafAId = UUID()
        let leafBId = UUID()
        let leafCId = UUID()
        let leafA = PanelLayout.leaf(id: leafAId, serviceId: nil)
        let leafB = PanelLayout.leaf(id: leafBId, serviceId: nil)
        let leafC = PanelLayout.leaf(id: leafCId, serviceId: nil)
        let innerSplit = PanelLayout.split(id: UUID(), axis: .horizontal, ratio: 0.5, first: leafA, second: leafB)
        var layout = PanelLayout.split(id: UUID(), axis: .vertical, ratio: 0.5, first: innerSplit, second: leafC)

        // Remove leafA — inner split should collapse to leafB
        layout.removeLeaf(id: leafAId)

        // Root should still be a split(leafB, leafC)
        guard case .split(_, _, _, let first, let second) = layout else {
            XCTFail("Root should remain a split"); return
        }
        if case .leaf(let id, _) = first { XCTAssertEqual(id, leafBId) } else { XCTFail("first should be leafB") }
        if case .leaf(let id, _) = second { XCTAssertEqual(id, leafCId) } else { XCTFail("second should be leafC") }
    }

    func test_removeLeaf_onlyLeaf_doesNothing() {
        let leafId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.removeLeaf(id: leafId)
        if case .leaf(let id, _) = layout { XCTAssertEqual(id, leafId) } else { XCTFail("leaf should remain") }
    }

    // MARK: - Codable round-trip

    func test_codable_roundTrip_singleLeaf() throws {
        let serviceId = UUID()
        let original = PanelLayout.leaf(id: UUID(), serviceId: serviceId)
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
        if case .leaf(_, let decodedServiceId) = decoded {
            XCTAssertEqual(serviceId, decodedServiceId)
        } else {
            XCTFail("Decoded layout should be a leaf")
        }
    }

    func test_codable_roundTrip_split() throws {
        let original = PanelLayout.split(
            id: UUID(), axis: .horizontal, ratio: 0.4,
            first: .leaf(id: UUID(), serviceId: nil),
            second: .leaf(id: UUID(), serviceId: UUID())
        )
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
        guard case .split(_, let axis, let ratio, _, _) = decoded else { XCTFail(); return }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.4, accuracy: 0.001)
    }
}
