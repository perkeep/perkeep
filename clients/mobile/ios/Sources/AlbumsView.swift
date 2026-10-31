import Photos
import SwiftUI

/// AlbumsView chooses which of the device's photo albums an account backs
/// up.
struct AlbumsView: View {
    let accountID: String

    @EnvironmentObject private var accounts: AccountStore

    @State private var access: PHAuthorizationStatus?
    @State private var albums: [PhotoAlbums.Info] = []
    @State private var states: AlbumStates?
    @State private var error: String?

    var body: some View {
        List {
            switch access {
            case nil:
                ProgressView()
            case .authorized?, .limited?:
                albumSections
            default:
                Section {
                    Text("Perkeep needs access to your photos to back them up.")
                    Button("Open Settings") {
                        if let url = URL(string: UIApplication.openSettingsURLString) {
                            UIApplication.shared.open(url)
                        }
                    }
                }
            }
        }
        .navigationTitle("Albums")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            access = await PhotoAlbums.requestAccess()
            albums = PhotoAlbums.list()
            refresh()
        }
        .alert("Error", isPresented: Binding(get: { error != nil }, set: { if !$0 { error = nil } })) {
            Button("OK") {}
        } message: {
            Text(error ?? "")
        }
    }

    @ViewBuilder private var albumSections: some View {
        let entries = states?.albums ?? []
        Section {
            ForEach(entries.filter(\.album.cameraRoll), id: \.album.id, content: row)
        } footer: {
            Text("Photos in included albums are backed up, except those also in an excluded album.")
        }
        let others = entries.filter { !$0.album.cameraRoll }
        if !others.isEmpty {
            Section("Albums") {
                ForEach(others, id: \.album.id, content: row)
            }
        }
        if let u = states?.unmatched, !u.include.isEmpty || !u.exclude.isEmpty {
            Section {
            } footer: {
                VStack(alignment: .leading) {
                    if !u.include.isEmpty {
                        Text("Also included, if present: " + u.include.map(\.title).joined(separator: ", "))
                    }
                    if !u.exclude.isEmpty {
                        Text("Also excluded, if present: " + u.exclude.map(\.title).joined(separator: ", "))
                    }
                }
            }
        }
    }

    private func row(_ e: AlbumStates.Entry) -> some View {
        Picker(selection: Binding(get: { e.state }, set: { set(e.album, $0) })) {
            Text("Include").tag(AlbumState.include)
            Text("Exclude").tag(AlbumState.exclude)
            Text("—").tag(AlbumState.none)
        } label: {
            VStack(alignment: .leading) {
                Text(e.album.title)
                if let n = albums.first(where: { $0.album.id == e.album.id })?.count {
                    Text("\(n) items").font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .pickerStyle(.menu)
    }

    private func refresh() {
        do {
            states = try accounts.albumStates(accountID, albums: albums.map(\.album))
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func set(_ album: Album, _ state: AlbumState) {
        do {
            try accounts.setAlbum(accountID, album, state)
        } catch {
            self.error = error.localizedDescription
        }
        refresh()
    }
}
