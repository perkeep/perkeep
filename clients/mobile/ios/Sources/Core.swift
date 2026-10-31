import Foundation

/// Core is the Swift interface to the Go core (clients/mobile/core).
enum Core {
    struct Response {
        let status: Int
        let body: Data
    }

    /// call makes an in-process HTTP-style request to the Go core.
    static func call(_ method: String, _ path: String, body: Data = Data()) -> Response {
        var out: UnsafeMutableRawPointer?
        var outLen: Int32 = 0
        // cgo declares the parameters as non-const, but the core doesn't
        // write through them.
        let status = method.withCString { m in
            path.withCString { p in
                body.withUnsafeBytes { buf in
                    PKCall(
                        UnsafeMutablePointer(mutating: m),
                        UnsafeMutablePointer(mutating: p),
                        UnsafeMutableRawPointer(mutating: buf.baseAddress),
                        Int32(buf.count),
                        &out,
                        &outLen)
                }
            }
        }
        defer { PKFree(out) }
        let data = out.map { Data(bytes: $0, count: Int(outLen)) } ?? Data()
        return Response(status: Int(status), body: data)
    }
}
