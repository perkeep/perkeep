import SwiftUI

/// AccountEditView adds an account (if accountID is nil) or edits one.
struct AccountEditView: View {
    let accountID: String?

    @EnvironmentObject private var accounts: AccountStore
    @EnvironmentObject private var router: Router

    @State private var name = ""
    @State private var server = ""
    @State private var username = ""
    @State private var password = "" // new password; empty keeps the saved one
    @State private var hasPassword = false
    @State private var loaded = false
    @State private var error: String?
    @State private var confirmDelete = false

    private var account: Account? { accountID.flatMap(accounts.account) }

    var body: some View {
        Form {
            Section {
                field("URL") {
                    TextField("URL", text: $server, prompt: Text("perkeep.example.com"))
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                field("Name") {
                    TextField("Name", text: $name, prompt: Text("Optional"))
                }
            } header: {
                Text("Server")
            }
            Section {
                field("Username") {
                    TextField("Username", text: $username, prompt: Text("Optional"))
                        .textContentType(.username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                field("Password") {
                    SecureField("Password", text: $password,
                                prompt: Text(hasPassword ? "Saved" : "Optional"))
                        .textContentType(.password)
                }
            } header: {
                Text("Sign In")
            } footer: {
                Text(hasPassword
                     ? "Leave the password blank to keep the saved one. Passwords are kept in the iOS Keychain."
                     : "Leave blank if the server doesn't need a password. Passwords are kept in the iOS Keychain.")
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
                    .disabled(server.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .onAppear(perform: load)
        .alert("Couldn't Save", isPresented: Binding(get: { error != nil }, set: { if !$0 { error = nil } })) {
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

    private func load() {
        guard !loaded else { return }
        loaded = true
        guard let account else { return }
        name = account.name
        server = account.server
        username = account.username
        hasPassword = Keychain.password(account: account.id) != nil
    }

    private func save() {
        do {
            if let accountID {
                try accounts.update(accountID, name: name, server: server, username: username,
                                    password: password.isEmpty ? nil : password)
            } else {
                _ = try accounts.add(name: name, server: server, username: username, password: password)
            }
            router.path.removeLast()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
