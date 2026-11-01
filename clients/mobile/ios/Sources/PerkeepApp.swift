import SwiftUI

@main
struct PerkeepApp: App {
    @StateObject private var accounts = AccountStore()
    @StateObject private var router = Router()

    init() {
        Core.initialize()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(accounts)
                .environmentObject(router)
        }
    }
}

/// Route is a screen in the app's navigation stack.
enum Route: Hashable {
    case accounts
    case addAccount(AccessInfo?) // prefilled from an access URL, if any
    case editAccount(String) // account ID
    case albums(String)      // account ID
}

/// Router holds the navigation stack.
@MainActor
final class Router: ObservableObject {
    @Published var path: [Route] = []

    init() {
        path = Self.initialPath()
    }

    /// initialPath opens the screen named by the PKScreen launch argument
    /// (accounts, add, edit, or albums; the last two for the current
    /// account), or the Add Server form for the access URL in the
    /// PKOpenURL launch argument. They're for testing in the simulator,
    /// which can't tap, not even the confirmation that opening a URL with
    /// simctl openurl shows:
    ///
    ///	xcrun simctl launch booted org.perkeep.mobile -PKScreen albums
    private static func initialPath() -> [Route] {
        if let u = UserDefaults.standard.string(forKey: "PKOpenURL"), let ai = try? AccessInfo.parse(u) {
            return [.accounts, .addAccount(ai)]
        }
        let current = (try? Core.json("GET", "/accounts") as AccountStore.List)?.current ?? ""
        switch UserDefaults.standard.string(forKey: "PKScreen") {
        case "accounts": return [.accounts]
        case "add": return [.accounts, .addAccount(nil)]
        case "edit" where !current.isEmpty: return [.accounts, .editAccount(current)]
        case "albums" where !current.isEmpty: return [.accounts, .editAccount(current), .albums(current)]
        default: return []
        }
    }
}

struct ContentView: View {
    @EnvironmentObject private var accounts: AccountStore
    @EnvironmentObject private var router: Router
    @State private var hello: [String: String] = [:]

    var body: some View {
        NavigationStack(path: $router.path) {
            List {
                Section("Server") {
                    if let a = accounts.current {
                        VStack(alignment: .leading) {
                            Text(a.name).font(.headline)
                            Text(a.location).font(.subheadline).foregroundStyle(.secondary)
                        }
                        LabeledContent("Backing up", value: a.albumSummary)
                    } else {
                        Text("No Perkeep server yet.")
                            .foregroundStyle(.secondary)
                    }
                    NavigationLink(value: Route.accounts) {
                        Label(accounts.list.accounts.isEmpty ? "Add a Server" : "Accounts",
                              systemImage: "server.rack")
                    }
                }
                Section("Go core") {
                    ForEach(["goVersion", "goos", "goarch", "build"], id: \.self) { k in
                        LabeledContent(k, value: hello[k] ?? "")
                            .font(.footnote.monospaced())
                    }
                }
            }
            .navigationTitle("Perkeep")
            .navigationDestination(for: Route.self) { route in
                switch route {
                case .accounts: AccountsView()
                case .addAccount(let ai): AccountEditView(accountID: nil, prefill: ai)
                case .editAccount(let id): AccountEditView(accountID: id)
                case .albums(let id): AlbumsView(accountID: id)
                }
            }
        }
        .onAppear {
            accounts.reload()
            hello = (try? Core.json("GET", "/hello")) ?? [:]
        }
        .onOpenURL { url in
            // A perkeep://access link, such as from the server's Client
            // access page or a QR code scanned with the Camera app.
            do {
                let ai = try AccessInfo.parse(url.absoluteString)
                router.path = [.accounts, .addAccount(ai)]
            } catch {
                accounts.lastError = error.localizedDescription
            }
        }
        .alert("Error", isPresented: Binding(
            get: { accounts.lastError != nil },
            set: { if !$0 { accounts.lastError = nil } }
        )) {
            Button("OK") {}
        } message: {
            Text(accounts.lastError ?? "")
        }
    }
}
