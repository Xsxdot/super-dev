import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var core: AppCore
    @State private var showAddProject = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            projectList
            Rectangle()
                .fill(Theme.borderPrimary)
                .frame(height: 1)
            addButton
        }
        .background(Theme.bgPrimary)
        .frame(width: 480, height: 320)
        .sheet(isPresented: $showAddProject) {
            AddProjectView().environmentObject(core)
        }
    }

    private var projectList: some View {
        ScrollView {
            VStack(spacing: 1) {
                ForEach(core.projects) { project in
                    projectRow(project)
                }
            }
            .padding(8)
        }
    }

    private func projectRow(_ project: Project) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(project.name)
                    .fontWeight(.medium)
                    .foregroundColor(Theme.textPrimary)
                Text(project.rootPath)
                    .font(.caption)
                    .foregroundColor(Theme.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer()
            Button {
                try? core.reloadConfig(for: project)
            } label: {
                Image(systemName: "arrow.clockwise")
                    .foregroundColor(Theme.textSecondary)
            }
            .buttonStyle(.plain)
            .help("重新加载配置")

            Button(role: .destructive) {
                core.removeProject(project)
            } label: {
                Image(systemName: "trash")
                    .foregroundColor(Theme.statusFailed)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(Theme.bgElevated)
        .cornerRadius(6)
    }

    private var addButton: some View {
        HStack {
            Spacer()
            Button {
                showAddProject = true
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "plus")
                    Text("添加项目")
                }
                .font(.system(size: 12, weight: .medium))
                .foregroundColor(.white)
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
                .background(Theme.accent)
                .cornerRadius(6)
            }
            .buttonStyle(.plain)
            .padding(12)
        }
        .background(Theme.bgPrimary)
    }
}
