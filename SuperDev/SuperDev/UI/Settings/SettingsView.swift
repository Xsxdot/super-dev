import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var core: AppCore
    @State private var showAddProject = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            List {
                ForEach(core.projects) { project in
                    HStack {
                        VStack(alignment: .leading) {
                            Text(project.name).fontWeight(.medium)
                            Text(project.rootPath)
                                .font(.caption)
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                        Spacer()
                        Button {
                            try? core.reloadConfig(for: project)
                        } label: {
                            Image(systemName: "arrow.clockwise")
                        }
                        .buttonStyle(.plain)
                        .help("重新加载配置")

                        Button(role: .destructive) {
                            core.removeProject(project)
                        } label: {
                            Image(systemName: "trash")
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.vertical, 4)
                }
            }

            Divider()

            HStack {
                Spacer()
                Button {
                    showAddProject = true
                } label: {
                    Label("添加项目", systemImage: "plus")
                }
                .buttonStyle(.borderedProminent)
                .padding(12)
            }
        }
        .frame(width: 480, height: 320)
        .sheet(isPresented: $showAddProject) {
            AddProjectView().environmentObject(core)
        }
    }
}
