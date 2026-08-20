import CryptoKit
import Darwin
import Dispatch
import Foundation

@testable import TunManagerFeed

/// A publisher on a real unix socket, for the one thing a fake transport cannot
/// show: what happens across a genuine close and a genuine reconnect.
///
/// It speaks the exchange the Go side does — a hello carrying its public key,
/// then a signature over whatever nonce it is challenged with — and it signs
/// with CryptoKit, so a client that believes it has been through the real thing.
final class LocalPublisher: @unchecked Sendable {
    let path: String
    let key: String

    private let listener: Int32
    private let identity = Curve25519.Signing.PrivateKey()
    private let state = NSLock()
    private var connections = 0
    private var live: [Int32] = []

    /// How many clients have been accepted, which is what a reconnect adds one
    /// to.
    var accepted: Int {
        state.lock()
        defer { state.unlock() }
        return connections
    }

    init(version: String = "v0.6.0") throws {
        // Short, because sun_path is 104 bytes and a temporary directory under
        // /var/folders eats most of them.
        path = "/tmp/tmf-\(UInt32.random(in: 0..<1_000_000)).sock"
        key = identity.publicKey.rawRepresentation.base64EncodedString()

        unlink(path)
        listener = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let bytes = Array(path.utf8)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.baseAddress!.copyMemory(from: bytes, byteCount: bytes.count)
        }
        let bound = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bound == 0, Darwin.listen(listener, 8) == 0 else {
            throw ConnectFailure(code: errno)
        }

        // Threads rather than a dispatch queue: accept(2) and read(2) block,
        // and blocking work handed to the pool while the rest of the suite is
        // running in parallel is how a queue runs out of threads and this
        // publisher stops answering the door.
        Thread.detachNewThread { [self] in serve(version: version) }
    }

    func stop() {
        state.lock()
        let open = live
        live = []
        state.unlock()
        for descriptor in open { Darwin.close(descriptor) }
        Darwin.close(listener)
        unlink(path)
    }

    private func serve(version: String) {
        while true {
            let accepted = Darwin.accept(listener, nil, nil)
            guard accepted >= 0 else {
                // A signal interrupted the wait; the listener is still there.
                // Giving up here would leave a socket nobody accepts on, which
                // a client reads as a publisher that refuses it.
                if errno == EINTR { continue }
                return
            }
            state.lock()
            connections += 1
            live.append(accepted)
            state.unlock()
            Thread.detachNewThread { [self] in talk(on: accepted, version: version) }
        }
    }

    private func talk(on descriptor: Int32, version: String) {
        let hello = #"{"type":"hello","schema":2,"version":"\#(version)","pubkey":"\#(key)"}"# + "\n"
        write(descriptor, hello)

        var buffer = Data()
        var chunk = [UInt8](repeating: 0, count: 4096)
        while true {
            let read = Darwin.read(descriptor, &chunk, chunk.count)
            if read < 0 && errno == EINTR { continue }
            guard read > 0 else { return }
            buffer.append(contentsOf: chunk[0..<read])
            while let end = buffer.firstIndex(of: UInt8(ascii: "\n")) {
                let line = buffer[buffer.startIndex..<end]
                buffer = buffer[buffer.index(after: end)...]
                answer(line, on: descriptor, version: version)
            }
        }
    }

    private func answer(_ line: Data, on descriptor: Int32, version: String) {
        guard let json = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
            json["type"] as? String == "challenge",
            let encoded = json["nonce"] as? String,
            let nonce = Data(base64Encoded: encoded)
        else {
            return
        }
        let message = PublisherProof.signedMessage(
            schema: 2, version: version, path: path, nonce: nonce)
        // try!: signing a message with a key made three lines up. A failure here
        // would be CryptoKit having stopped working, not this program.
        let signature = try! identity.signature(for: message)
        write(
            descriptor,
            #"{"type":"auth","nonce":"\#(encoded)","signature":"\#(signature.base64EncodedString())"}"#
                + "\n")
    }

    private func write(_ descriptor: Int32, _ text: String) {
        var bytes = Array(text.utf8)
        _ = Darwin.write(descriptor, &bytes, bytes.count)
    }
}
