import SwiftUI
import VisionKit

/// QRScannerView scans the camera for a QR code of a perkeep:// URL and
/// passes it to onScan.
struct QRScannerView: View {
    let onScan: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Group {
                if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                    Scanner { s in
                        onScan(s)
                        dismiss()
                    }
                    .ignoresSafeArea()
                    .overlay(alignment: .bottom) {
                        Text("Scan the QR code on your server's Client access page.")
                            .font(.callout)
                            .padding(10)
                            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
                            .padding()
                    }
                } else {
                    ContentUnavailableView(
                        "Camera Unavailable", systemImage: "camera",
                        description: Text(DataScannerViewController.isSupported
                            ? "Allow Perkeep to use the camera in Settings."
                            : "This device can't scan QR codes. Paste the access link instead."))
                }
            }
            .navigationTitle("Scan QR Code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
    }

    private struct Scanner: UIViewControllerRepresentable {
        let onScan: (String) -> Void

        func makeUIViewController(context: Context) -> DataScannerViewController {
            let vc = DataScannerViewController(
                recognizedDataTypes: [.barcode(symbologies: [.qr])],
                qualityLevel: .balanced,
                isHighlightingEnabled: true)
            vc.delegate = context.coordinator
            try? vc.startScanning()
            return vc
        }

        func updateUIViewController(_ vc: DataScannerViewController, context: Context) {}

        func makeCoordinator() -> Coordinator { Coordinator(onScan: onScan) }

        final class Coordinator: NSObject, DataScannerViewControllerDelegate {
            let onScan: (String) -> Void
            private var done = false

            init(onScan: @escaping (String) -> Void) { self.onScan = onScan }

            func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
                for item in items {
                    guard case .barcode(let b) = item, let s = b.payloadStringValue,
                          s.lowercased().hasPrefix("perkeep://"), !done else { continue }
                    done = true
                    scanner.stopScanning()
                    onScan(s)
                    return
                }
            }
        }
    }
}
