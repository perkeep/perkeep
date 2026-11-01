import SwiftUI

/// AccountView is an account's main screen: whether its server is
/// reachable, what the app may do there, and actions. It checks the
/// server whenever it appears.
struct AccountView: View {
    let accountID: String

    @EnvironmentObject private var accounts: AccountStore

    @State private var checking = false
    @State private var result: CheckResult?
    @State private var checkID = 0 // to ignore results of superseded checks

    /// openTimeout is how long the automatic check when the view appears
    /// waits for the server.
    private static let openTimeout = 10.0

    private var account: Account? { accounts.account(accountID) }

    var body: some View {
        if let account {
            List {
                header(account)
                actions(account)
                if let result, result.ok {
                    permissions(result)
                    storage(result)
                }
                Section {
                    NavigationLink(value: Route.albums(account.id)) {
                        LabeledContent("Albums to Back Up", value: account.albumSummary)
                    }
                    NavigationLink(value: Route.editAccount(account.id)) {
                        Text("Account Settings")
                    }
                }
            }
            .navigationTitle(serverName(account))
            .task(id: account) { await check(timeout: Self.openTimeout) }
            .refreshable { await check(timeout: nil) }
        } else {
            ContentUnavailableView("No Such Account", systemImage: "server.rack")
        }
    }

    private func serverName(_ a: Account) -> String {
        if let n = result?.client?.serverName, !n.isEmpty {
            return n
        }
        return a.name
    }

    @ViewBuilder private func header(_ a: Account) -> some View {
        Section {
            LabeledContent("Server", value: a.location)
            if let key = result?.client?.accessKeyName, !key.isEmpty {
                LabeledContent("Access key", value: key)
            }
            if let owner = result?.ownerName, !owner.isEmpty {
                LabeledContent("Owner", value: owner)
            }
            HStack {
                if checking {
                    ProgressView()
                    Text("Checking…").foregroundStyle(.secondary)
                } else if let r = result {
                    Image(systemName: r.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                        .foregroundStyle(r.ok ? .green : .red)
                    VStack(alignment: .leading) {
                        Text(r.message)
                        Text(r.durationText)
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    @ViewBuilder private func permissions(_ r: CheckResult) -> some View {
        Section {
            if let have = r.client?.permissions.map(Set.init) {
                ForEach(Permission.allCases) { p in
                    Label {
                        HStack {
                            Text(p.title)
                            Spacer()
                            Image(systemName: have.contains(p.rawValue) ? "checkmark" : "xmark")
                                .foregroundStyle(have.contains(p.rawValue) ? .green : .secondary)
                                .accessibilityLabel(have.contains(p.rawValue) ? "Allowed" : "Not allowed")
                        }
                    } icon: {
                        Image(systemName: p.symbol)
                    }
                    .foregroundStyle(have.contains(p.rawValue) ? .primary : .secondary)
                }
            } else {
                Text("This server doesn't say what these credentials allow.")
                    .foregroundStyle(.secondary)
            }
        } header: {
            Text("Permissions")
        }
    }

    @ViewBuilder private func storage(_ r: CheckResult) -> some View {
        Section("Storage") {
            if let st = r.storage, let blobs = st.approxBlobs, let bytes = st.approxBytes {
                LabeledContent("Size", value: "≈ " + ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file))
                LabeledContent("Blobs", value: "≈ " + blobs.formatted())
            } else if r.storage != nil {
                Text("The server hasn't counted its storage yet.")
                    .foregroundStyle(.secondary)
            } else {
                Text("Seeing the server's size needs the Server stats permission.")
                    .foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder private func actions(_ a: Account) -> some View {
        let perms = result?.client?.permissions.map(Set.init)
        Section {
            Button {
                Task { await check(timeout: nil) }
            } label: {
                Label("Check", systemImage: "arrow.clockwise")
            }
            .disabled(checking)
            NavigationLink(value: Route.upload(a.id)) {
                Label("Upload Photos", systemImage: "icloud.and.arrow.up")
            }
            .disabled(perms.map { !$0.contains(Permission.upload.rawValue) } ?? false)
            // Not done yet.
            Label("Query", systemImage: "magnifyingglass")
                .foregroundStyle(.secondary)
        } footer: {
            if let perms, !perms.contains(Permission.upload.rawValue) {
                Text("This access key can't upload. Querying isn't done yet.")
            } else {
                Text("Querying isn't done yet.")
            }
        }
    }

    /// check checks the server, waiting at most timeout seconds, or the
    /// core's default if nil.
    private func check(timeout: Double?) async {
        guard let account else { return }
        checkID += 1
        let id = checkID
        checking = true
        let f = accounts.fields(account)
        let r: CheckResult
        do {
            r = try await accounts.check(f, timeout: timeout)
        } catch {
            r = CheckResult(ok: false, status: 0, message: error.localizedDescription, seconds: 0)
        }
        guard id == checkID else { return }
        result = r
        checking = false
    }
}
