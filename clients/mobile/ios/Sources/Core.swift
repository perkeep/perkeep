import Foundation

/// Core is the Swift interface to the Go core (clients/mobile/core).
enum Core {
    struct Response {
        let status: Int
        let body: Data
    }

    /// Error is a failed request to the core.
    struct Error: LocalizedError {
        let status: Int
        let message: String
        var errorDescription: String? { message }
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

    /// send makes a request with an optional JSON body and returns the
    /// response body, throwing if the request failed.
    @discardableResult
    static func send(_ method: String, _ path: String, _ body: (any Encodable)? = nil) throws -> Data {
        let reqBody = try body.map { try JSONEncoder().encode($0) } ?? Data()
        let resp = call(method, path, body: reqBody)
        guard (200..<300).contains(resp.status) else {
            struct ErrorBody: Decodable { let error: String }
            let msg = (try? JSONDecoder().decode(ErrorBody.self, from: resp.body))?.error
                ?? "core error \(resp.status)"
            throw Error(status: resp.status, message: msg)
        }
        return resp.body
    }

    /// json is like send but decodes the JSON response.
    static func json<T: Decodable>(_ method: String, _ path: String, _ body: (any Encodable)? = nil) throws -> T {
        try JSONDecoder().decode(T.self, from: send(method, path, body))
    }

    /// initialize tells the core where to keep its state. It must be called
    /// before other requests.
    static func initialize() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("core", isDirectory: true)
        do {
            try send("POST", "/init", ["dataDir": dir.path])
        } catch {
            // Later requests will fail and show the error.
            NSLog("Perkeep core init: \(error)")
        }
    }
}
