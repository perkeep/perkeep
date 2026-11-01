import SwiftUI

/// AccountEditView adds an account (if accountID is nil) or edits one.
/// When adding, prefill, if set, is a scanned or opened access URL.
struct AccountEditView: View {
    let accountID: String?
    var prefill: AccessInfo? = nil

    @EnvironmentObject private var accounts: AccountStore
    @EnvironmentObject private var router: Router

    @State private var f = ServerFields()
    @State private var loaded = false
    @State private var error: String?
    @State private var confirmDelete = false
    @State private var scanning = false
    @State private var checking = false
    @State private var checkResult: CheckResult?

    private var account: Account? { accountID.flatMap(accounts.account) }
    private var isTailcat: Bool { !f.tailcat.isEmpty }

    var body: some View {
        Form {
            if accountID == nil {
                Section {
                    Button {
                        scanning = true
                    } label: {
                        Label("Scan QR Code", systemImage: "qrcode.viewfinder")
                    }
                    Button(action: pasteAccessLink) {
                        Label("Paste Access Link", systemImage: "doc.on.clipboard")
                    }
                } footer: {
                    Text("Your server's Client access page, in its web UI's menu, shows a QR code and link for each access key.")
                }
            }
            Section {
                if isTailcat {
                    LabeledContent("Connection", value: "tailcat")
                } else {
                    field("URL") {
                        TextField("URL", text: $f.server, prompt: Text("perkeep.example.com"))
                            .keyboardType(.URL)
                            .textContentType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                }
                field("Name") {
                    TextField("Name", text: $f.name, prompt: Text("Optional"))
                }
                if !isTailcat && !f.certfp.isEmpty {
                    LabeledContent("Certificate", value: "Pinned " + f.certfp.prefix(12) + "…")
                }
            } header: {
                Text("Server")
            } footer: {
                if isTailcat {
                    Text("Connects through the server's tailcat listener, an encrypted tunnel that works without a VPN.")
                }
            }
            Section {
                if !isTailcat {
                    field("Username") {
                        TextField("Username", text: $f.username, prompt: Text("Optional"))
                            .textContentType(.username)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                }
                field("Password") {
                    SecureField("Password", text: $f.password, prompt: Text("Optional"))
                        .textContentType(.password)
                }
            } header: {
                Text("Sign In")
            } footer: {
                Text("A password or an access key. It's kept in the iOS Keychain.")
            }
            Section {
                Button(action: check) {
                    HStack {
                        Text("Check Server")
                        Spacer()
                        if checking {
                            ProgressView()
                        }
                    }
                }
                .disabled(checking || (!isTailcat && f.server.trimmingCharacters(in: .whitespaces).isEmpty))
                if let r = checkResult {
                    Label {
                        VStack(alignment: .leading) {
                            Text(r.message)
                            Text(checkDetail(r)).font(.caption).foregroundStyle(.secondary)
                        }
                    } icon: {
                        Image(systemName: r.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                            .foregroundStyle(r.ok ? .green : .red)
                    }
                }
            }
            if let account {
                Section("Photos") {
                    NavigationLink(value: Route.albums(account.id)) {
                        LabeledContent("Albums to Back Up", value: account.albumSummary)
                    }
                }
                Section {
                    Button("Delete Account", role: .destructive) {
                        confirmDelete = true
                    }
                }
            } else {
                Section {
                } footer: {
                    Text("New accounts back up the Camera Roll, except any WhatsApp album. You can change this after adding the server.")
                }
            }
        }
        .navigationTitle(accountID == nil ? "Add Server" : (account?.name ?? "Account"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button(accountID == nil ? "Add" : "Save", action: save)
                    .disabled(!isTailcat && f.server.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .onAppear(perform: load)
        .onChange(of: f) { checkResult = nil }
        .sheet(isPresented: $scanning) {
            QRScannerView(onScan: useAccessURL)
        }
        .alert("Error", isPresented: Binding(get: { error != nil }, set: { if !$0 { error = nil } })) {
            Button("OK") {}
        } message: {
            Text(error ?? "")
        }
        .confirmationDialog("Delete \(account?.name ?? "this account")?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete", role: .destructive) {
                if let accountID {
                    accounts.delete(accountID)
                    router.path.removeLast()
                }
            }
        } message: {
            Text("This removes the account from this device. Nothing is deleted from the server.")
        }
    }

    /// field is a form row with a label on the left and an editable value
    /// on the right, like in Settings.
    private func field(_ label: String, @ViewBuilder _ content: () -> some View) -> some View {
        LabeledContent(label) {
            content().multilineTextAlignment(.trailing)
        }
    }

    private func checkDetail(_ r: CheckResult) -> String {
        var parts: [String] = []
        if let owner = r.ownerName, !owner.isEmpty {
            parts.append("Owner: \(owner)")
        }
        if r.status != 0 {
            parts.append("HTTP \(r.status)")
        }
        parts.append(String(format: "%.1fs", r.seconds))
        return parts.joined(separator: " · ")
    }

    private func load() {
        guard !loaded else { return }
        loaded = true
        if let account {
            f = accounts.fields(account)
        } else if let prefill {
            f = ServerFields(prefill)
            check()
        }
    }

    private func pasteAccessLink() {
        guard let s = UIPasteboard.general.string else {
            error = "The clipboard is empty. Copy the link from your server's Client access page first."
            return
        }
        useAccessURL(s)
    }

    private func useAccessURL(_ s: String) {
        do {
            f = ServerFields(try AccessInfo.parse(s))
            check()
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func check() {
        checking = true
        checkResult = nil
        let fields = f
        Task {
            defer { checking = false }
            do {
                let r = try await accounts.check(fields)
                if fields == f {
                    checkResult = r
                }
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func save() {
        do {
            if let accountID {
                try accounts.update(accountID, f)
            } else if let existing = accounts.existing(matching: f) {
                // Scanning a server again, such as for a new access key,
                // updates its account rather than adding a duplicate.
                try accounts.update(existing.id, f)
            } else {
                _ = try accounts.add(f)
            }
            router.path.removeLast()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
