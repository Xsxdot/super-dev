import XCTest
import SwiftUI
@testable import SuperDev

final class PanelLayoutTests: XCTestCase {

    // MARK: - id

    func test_leaf_hasStableId() {
        let id = UUID()
        let leaf = PanelLayout.leaf(id: id, serviceId: nil, projectId: nil)
        XCTAssertEqual(leaf.id, id)
    }

    // MARK: - replacing a leaf with a split

    func test_splitLeaf_horizontal_right_producesCorrectTree() {
        let leafId = UUID()
        let serviceId = UUID()
        let projectId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil, projectId: nil)
        layout.splitLeaf(
            id: leafId, axis: .horizontal, newServiceId: serviceId, newProjectId: projectId, newSide: .second
        )
        guard case .split(_, let axis, let ratio, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.5, accuracy: 0.001)
        if case .leaf(let fId, _, _) = first { XCTAssertEqual(fId, leafId) } else { XCTFail("first should be original leaf") }
        if case .leaf(_, let sid, let pid) = second {
            XCTAssertEqual(sid, serviceId)
            XCTAssertEqual(pid, projectId)
        } else { XCTFail("second should be new leaf") }
    }

    func test_splitLeaf_horizontal_left_newPanelIsFirst() {
        let leafId = UUID()
        let serviceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil, projectId: nil)
        layout.splitLeaf(
            id: leafId, axis: .horizontal, newServiceId: serviceId, newProjectId: nil, newSide: .first
        )
        guard case .split(_, _, _, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        if case .leaf(_, let sid, _) = first { XCTAssertEqual(sid, serviceId) } else { XCTFail("first should be new leaf") }
        if case .leaf(let fId, _, _) = second { XCTAssertEqual(fId, leafId) } else { XCTFail("second should be original leaf") }
    }

    // MARK: - replaceService

    func test_replaceService_updatesLeafServiceId() {
        let leafId = UUID()
        let newServiceId = UUID()
        let projectId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil, projectId: projectId)
        layout.replaceService(panelId: leafId, newServiceId: newServiceId)
        if case .leaf(_, let sid, let pid) = layout {
            XCTAssertEqual(sid, newServiceId)
            XCTAssertEqual(pid, projectId)
        } else {
            XCTFail("Should remain a leaf")
        }
    }

    // MARK: - replaceScope

    func test_replaceScope_updatesServiceAndProject() {
        let leafId = UUID()
        let serviceId = UUID()
        let projectId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil, projectId: nil)
        layout.replaceScope(panelId: leafId, serviceId: serviceId, projectId: projectId)
        if case .leaf(_, let sid, let pid) = layout {
            XCTAssertEqual(sid, serviceId)
            XCTAssertEqual(pid, projectId)
        } else {
            XCTFail("Should remain a leaf")
        }
    }

    func test_replaceScope_projectOnly_clearsService() {
        let leafId = UUID()
        let projectId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: UUID(), projectId: nil)
        layout.replaceScope(panelId: leafId, serviceId: nil, projectId: projectId)
        if case .leaf(_, let sid, let pid) = layout {
            XCTAssertNil(sid)
            XCTAssertEqual(pid, projectId)
        } else {
            XCTFail("Should remain a leaf")
        }
    }

    func test_leafScope_findsNestedLeaf() {
        let leafAId = UUID()
        let leafBId = UUID()
        let layout = PanelLayout.split(
            id: UUID(), axis: .horizontal, ratio: 0.5,
            first: .leaf(id: leafAId, serviceId: UUID(), projectId: UUID()),
            second: .leaf(id: leafBId, serviceId: nil, projectId: UUID())
        )
        XCTAssertNotNil(layout.leafScope(panelId: leafBId))
        XCTAssertNil(layout.leafScope(panelId: UUID()))
    }

    // MARK: - removeLeaf

    func test_removeLeaf_fromSplit_promotesOtherChild() {
        let leftId = UUID()
        let rightId = UUID()
        let leftLeaf = PanelLayout.leaf(id: leftId, serviceId: nil, projectId: nil)
        let rightLeaf = PanelLayout.leaf(id: rightId, serviceId: nil, projectId: nil)
        var layout = PanelLayout.split(id: UUID(), axis: .horizontal, ratio: 0.5, first: leftLeaf, second: rightLeaf)
        layout.removeLeaf(id: leftId)
        if case .leaf(let id, _, _) = layout { XCTAssertEqual(id, rightId) } else { XCTFail("right child should be promoted") }
    }

    func test_removeLeaf_deepNested_promotesCorrectly() {
        let leafAId = UUID()
        let leafBId = UUID()
        let leafCId = UUID()
        let leafA = PanelLayout.leaf(id: leafAId, serviceId: nil, projectId: nil)
        let leafB = PanelLayout.leaf(id: leafBId, serviceId: nil, projectId: nil)
        let leafC = PanelLayout.leaf(id: leafCId, serviceId: nil, projectId: nil)
        let innerSplit = PanelLayout.split(id: UUID(), axis: .horizontal, ratio: 0.5, first: leafA, second: leafB)
        var layout = PanelLayout.split(id: UUID(), axis: .vertical, ratio: 0.5, first: innerSplit, second: leafC)

        layout.removeLeaf(id: leafAId)

        guard case .split(_, _, _, let first, let second) = layout else {
            XCTFail("Root should remain a split"); return
        }
        if case .leaf(let id, _, _) = first { XCTAssertEqual(id, leafBId) } else { XCTFail("first should be leafB") }
        if case .leaf(let id, _, _) = second { XCTAssertEqual(id, leafCId) } else { XCTFail("second should be leafC") }
    }

    func test_removeLeaf_onlyLeaf_doesNothing() {
        let leafId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil, projectId: nil)
        layout.removeLeaf(id: leafId)
        if case .leaf(let id, _, _) = layout { XCTAssertEqual(id, leafId) } else { XCTFail("leaf should remain") }
    }

    // MARK: - Codable round-trip

    func test_codable_roundTrip_singleLeaf() throws {
        let serviceId = UUID()
        let projectId = UUID()
        let original = PanelLayout.leaf(id: UUID(), serviceId: serviceId, projectId: projectId)
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
        if case .leaf(_, let decodedServiceId, let decodedProjectId) = decoded {
            XCTAssertEqual(serviceId, decodedServiceId)
            XCTAssertEqual(projectId, decodedProjectId)
        } else {
            XCTFail("Decoded layout should be a leaf")
        }
    }

    func test_codable_decodesLegacyLeafWithoutProjectId() throws {
        let serviceId = UUID()
        let id = UUID()
        let json = """
        {"type":"leaf","id":"\(id.uuidString)","serviceId":"\(serviceId.uuidString)"}
        """
        let data = json.data(using: .utf8)!
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        if case .leaf(_, let sid, let pid) = decoded {
            XCTAssertEqual(sid, serviceId)
            XCTAssertNil(pid)
        } else {
            XCTFail("Decoded layout should be a leaf")
        }
    }

    func test_codable_roundTrip_split() throws {
        let original = PanelLayout.split(
            id: UUID(), axis: .horizontal, ratio: 0.4,
            first: .leaf(id: UUID(), serviceId: nil, projectId: nil),
            second: .leaf(id: UUID(), serviceId: UUID(), projectId: UUID())
        )
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
        guard case .split(_, let axis, let ratio, _, _) = decoded else { XCTFail(); return }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.4, accuracy: 0.001)
    }
}
