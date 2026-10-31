import SwiftUI

/// AccountsView lists the Perkeep servers. Tapping one makes it current;
/// its info button edits it.
struct AccountsView: View {
    @EnvironmentObject private var accounts: AccountStore
    @EnvironmentObject private var router: Router

    var body: some View {
        List {
            Section {
                ForEach(accounts.list.accounts) { a in
                    HStack {
                        Button {
                            accounts.select(a.id)
                        } label: {
                            HStack {
                                Image(systemName: a.id == accounts.list.current ? "checkmark.circle.fill" : "circle")
                                    .foregroundStyle(a.id == accounts.list.current ? Color.accentColor : .secondary)
                                VStack(alignment: .leading) {
                                    Text(a.name)
                                    Text(a.server).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        Button {
                            router.path.append(.editAccount(a.id))
                        } label: {
                            Image(systemName: "info.circle")
                        }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Edit \(a.name)")
                    }
                }
                .onDelete { offsets in
                    for i in offsets {
                        accounts.delete(accounts.list.accounts[i].id)
                    }
                }
            } footer: {
                if accounts.list.accounts.count > 1 {
                    Text("Photos are backed up to the checked server.")
                }
            }
            Section {
                Button {
                    router.path.append(.addAccount)
                } label: {
                    Label("Add Server", systemImage: "plus")
                }
            }
        }
        .navigationTitle("Accounts")
        .toolbar {
            if !accounts.list.accounts.isEmpty {
                EditButton()
            }
        }
    }
}
