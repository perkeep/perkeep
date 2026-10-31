import SwiftUI

@main
struct PerkeepApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}

struct ContentView: View {
    @State private var hello: [String: String] = [:]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Perkeep").font(.largeTitle).bold()
            Text(hello["message"] ?? "…")
            ForEach(["goVersion", "goos", "goarch", "build"], id: \.self) { k in
                LabeledContent(k, value: hello[k] ?? "")
                    .font(.footnote.monospaced())
            }
        }
        .padding()
        .onAppear {
            let resp = Core.call("GET", "/hello")
            hello = (try? JSONDecoder().decode([String: String].self, from: resp.body))
                ?? ["message": "core error \(resp.status)"]
        }
    }
}
