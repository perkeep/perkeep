import SwiftUI

@main
struct PerkeepApp: App {
    @StateObject private var accounts = AccountStore()
    @StateObject private var router = Router()
    @StateObject private var uploads = UploadCenter()

    init() {
        Core.initialize()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(accounts)
                .environmentObject(router)
                .environmentObject(uploads)
        }
    }
}

/// UploadCenter holds each account's UploadModel, for the app's life, so
/// a run goes on while the user moves between screens.
@MainActor
final class UploadCenter: ObservableObject {
    private var models: [String: UploadModel] = [:]

    func model(for accountID: String, store: AccountStore) -> UploadModel {
        if let m = models[accountID] {
            return m
        }
        let m = UploadModel(accountID: accountID, store: store)
        models[accountID] = m
        return m
    }
}

/// Route is a screen in the app's navigation stack.
enum Route: Hashable {
    case account(String) // account ID
    case accounts
    case addAccount(AccessInfo?) // prefilled from an access URL, if any
    case editAccount(String) // account ID
    case albums(String)      // account ID
    case upload(String)      // account ID
    case gallery(String)     // account ID
}

/// Router holds the navigation stack.
@MainActor
final class Router: ObservableObject {
    @Published var path: [Route] = []

    init() {
        path = Self.initialPath()
    }

    /// initialPath opens the screen named by the PKScreen launch argument
    /// (accounts, add, account, edit, or albums; the last three for the
    /// current account), or the Add Server form for the access URL in the
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
        case "account" where !current.isEmpty: return [.account(current)]
        case "upload" where !current.isEmpty: return [.account(current), .upload(current)]
        case "gallery" where !current.isEmpty: return [.account(current), .gallery(current)]
        case "edit" where !current.isEmpty: return [.accounts, .editAccount(current)]
        case "albums" where !current.isEmpty: return [.accounts, .editAccount(current), .albums(current)]
        default: return []
        }
    }
}

struct ContentView: View {
    @EnvironmentObject private var accounts: AccountStore
    @EnvironmentObject private var router: Router
    @EnvironmentObject private var uploads: UploadCenter

    var body: some View {
        NavigationStack(path: $router.path) {
            home
            .navigationDestination(for: Route.self) { route in
                switch route {
                case .account(let id): AccountView(accountID: id)
                case .accounts: AccountsView()
                case .addAccount(let ai): AccountEditView(accountID: nil, prefill: ai)
                case .editAccount(let id): AccountEditView(accountID: id)
                case .albums(let id): AlbumsView(accountID: id)
                case .upload(let id): UploadView(model: uploads.model(for: id, store: accounts))
                case .gallery(let id): GalleryView(model: uploads.model(for: id, store: accounts))
                }
            }
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

    /// home is the root screen: a welcome with no accounts, the account's
    /// screen with one, or a list of them with more.
    @ViewBuilder private var home: some View {
        let all = accounts.list.accounts
        switch all.count {
        case 0:
            WelcomeView()
        case 1:
            AccountView(accountID: all[0].id)
                .toolbar { manageButton }
        default:
            List(all) { a in
                NavigationLink(value: Route.account(a.id)) {
                    VStack(alignment: .leading) {
                        Text(a.name)
                        Text(a.location).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Perkeep")
            .toolbar { manageButton }
        }
    }

    private var manageButton: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            NavigationLink(value: Route.accounts) {
                Image(systemName: "server.rack")
            }
            .accessibilityLabel("Accounts")
        }
    }
}

/// WelcomeView is the home screen before any server is added.
struct WelcomeView: View {
    @EnvironmentObject private var router: Router

    var body: some View {
        VStack(spacing: 16) {
            Spacer()
            Image("Keepy")
                .resizable()
                .scaledToFit()
                .frame(maxWidth: 220)
                .accessibilityHidden(true)
            Text("Welcome to Perkeep")
                .font(.largeTitle.bold())
                .multilineTextAlignment(.center)
            Text("Keep your photos on your own Perkeep server. To start, add your server.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
            Button {
                router.path = [.accounts, .addAccount(nil)]
            } label: {
                Label("Add a Server", systemImage: "plus")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .padding(.top)
            Spacer()
            Spacer()
        }
        .padding(.horizontal, 32)
    }
}
