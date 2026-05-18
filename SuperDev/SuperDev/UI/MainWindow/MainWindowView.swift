import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore

    var body: some View {
        Text("Main Window - TODO")
            .frame(minWidth: 400, minHeight: 300)
    }
}
