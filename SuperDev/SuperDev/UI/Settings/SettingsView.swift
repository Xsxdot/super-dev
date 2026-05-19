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
        .frame(width: 480)
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
        VStack(alignment: .leading, spacing: 0) {
            // 项目标题行
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

            // 服务可见性列表
            if !project.services.isEmpty {
                Divider()
                    .padding(.horizontal, 8)
                VStack(spacing: 0) {
                    ForEach(project.services) { service in
                        serviceVisibilityRow(service)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.bottom, 8)
            }
        }
        .background(Theme.bgElevated)
        .cornerRadius(6)
    }

    private func serviceVisibilityRow(_ service: Service) -> some View {
        let isHidden = core.isHidden(service)
        return HStack(spacing: 8) {
            Image(systemName: service.required ? "lock.fill" : "circle.fill")
                .font(.system(size: 7))
                .foregroundColor(service.required ? Theme.accent : Theme.textTertiary)
            Text(service.name)
                .font(.system(size: 11))
                .foregroundColor(isHidden ? Theme.textTertiary : Theme.textPrimary)
            Spacer()
            Button {
                core.toggleHidden(service)
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: isHidden ? "eye.slash" : "eye")
                        .font(.system(size: 9))
                    Text(isHidden ? "已隐藏" : "显示")
                        .font(.system(size: 9))
                }
                .foregroundColor(isHidden ? Theme.textTertiary : Theme.textSecondary)
            }
            .buttonStyle(.plain)
            .help(isHidden ? "点击以在菜单栏中显示此服务" : "点击以在菜单栏中隐藏此服务")
        }
        .padding(.vertical, 4)
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
