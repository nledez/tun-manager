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
    /// What this publisher does when challenged. Each case is somebody's idea
    /// of how to be taken for tun-manager.
    enum Behaviour: Sendable, Equatable {
        /// Signs what it was asked, for the socket it is listening on.
        case honest
        /// Answers every challenge with one pair it captured earlier, which is
        /// what a recording of a genuine session gives somebody.
        case replaying(nonce: String, signature: String)
        /// Signs the challenge for a different socket path: what a relay gets
        /// back when it forwards the question to the real publisher.
        case relaying(path: String)
        /// Says hello and never answers anything.
        case silent
    }

    let path: String
    let key: String
    /// Changed between connections by a test that wants the same socket to
    /// start misbehaving, which is what taking one over looks like from here.
    var behaviour: Behaviour {
        get { state.lock(); defer { state.unlock() }; return manner }
        set { state.lock(); manner = newValue; state.unlock() }
    }
    /// Sent right after the hello, before any answer, so a test can ask what
    /// happened to it. Attackers send their view early for the same reason the
    /// real one does: it is the first thing a menu would draw.
    let sayingFirst: String?

    private let listener: Int32
    private let identity = Curve25519.Signing.PrivateKey()
    private let state = NSLock()
    private var connections = 0
    /// Set by stop(), which is the only reason to give up on the listener.
    private var stopped = false
    private var manner: Behaviour = .honest
    /// The last answer this publisher gave, which is what somebody recording
    /// the socket would have.
    private var answered: (nonce: String, signature: String)?
    private var live: [Int32] = []

    /// The last (nonce, signature) pair sent, for a test that wants to replay
    /// one.
    var lastAuth: (nonce: String, signature: String)? {
        state.lock()
        defer { state.unlock() }
        return answered
    }

    /// How many clients have been accepted, which is what a reconnect adds one
    /// to.
    var accepted: Int {
        state.lock()
        defer { state.unlock() }
        return connections
    }

    init(
        version: String = "v0.6.0", behaviour: Behaviour = .honest, schema: Int = 2,
        sayingFirst: String? = nil
    ) throws {
        self.manner = behaviour
        self.sayingFirst = sayingFirst
        // Once, for the process. SO_NOSIGPIPE covers the descriptors this
        // publisher owns, but a suite running its tests in parallel has several
        // of these opening and closing at once, and a SIGPIPE anywhere in it
        // kills the whole run rather than one test. Production code sets the
        // socket option instead; a test process can simply not die.
        signal(SIGPIPE, SIG_IGN)

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
        Thread.detachNewThread { [self] in serve(version: version, schema: schema) }
    }

    /// Shuts every descriptor down without closing any of them.
    ///
    /// Closing here would be closing a descriptor another thread is blocked
    /// reading, and the number is handed straight back out by the next socket()
    /// in the process - so a suite running in parallel ends up with libdispatch
    /// watching a descriptor that now belongs to somebody else, which it
    /// notices and traps on. shutdown(2) wakes the reader instead, and each
    /// thread closes the one descriptor it owns.
    func stop() {
        state.lock()
        stopped = true
        let open = live
        live = []
        state.unlock()
        for descriptor in open { shutdown(descriptor, SHUT_RDWR) }
        shutdown(listener, SHUT_RDWR)
        unlink(path)
    }

    private func serve(version: String, schema: Int) {
        while true {
            let accepted = Darwin.accept(listener, nil, nil)
            guard accepted >= 0 else {
                // Only a stop is a reason to give up on the listener. Every
                // other errno here - a client that hung up between its connect
                // and this accept, a signal - leaves a listening socket that
                // nobody accepts on, and a client reads that as a publisher
                // refusing it: ECONNREFUSED, in a test that says nothing about
                // why.
                state.lock()
                let finished = stopped
                state.unlock()
                guard finished else { continue }
                // The thread that owns this descriptor is the one that closes
                // it.
                Darwin.close(listener)
                return
            }
            // Without this, writing to a client that has closed raises SIGPIPE,
            // whose default disposition kills the process - and the process
            // here is the test suite.
            var on: Int32 = 1
            setsockopt(
                accepted, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))

            state.lock()
            connections += 1
            live.append(accepted)
            state.unlock()
            Thread.detachNewThread { [self] in
                talk(on: accepted, version: version, schema: schema)
            }
        }
    }

    private func talk(on descriptor: Int32, version: String, schema: Int) {
        // Closed here and nowhere else, by the one thread that reads it - and
        // forgotten first. A number left on that list after it has been closed
        // is a number the process hands to the next socket() call, and stop()
        // would then shut down somebody else's listener: a publisher in another
        // test refusing every connection, for no reason visible from there.
        defer {
            state.lock()
            live.removeAll { $0 == descriptor }
            state.unlock()
            Darwin.close(descriptor)
        }
        let hello =
            #"{"type":"hello","schema":\#(schema),"version":"\#(version)","pubkey":"\#(key)"}"#
            + "\n"
        write(descriptor, hello)
        if let sayingFirst {
            // Before it has said who it is, which is when everything an
            // impostor sends arrives.
            write(descriptor, sayingFirst + "\n")
        }

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
        let manner = behaviour
        switch manner {
        case .silent:
            return
        case .replaying(let nonce, let signature):
            write(descriptor, #"{"type":"auth","nonce":"\#(nonce)","signature":"\#(signature)"}"# + "\n")
        case .honest, .relaying:
            let signedPath = if case .relaying(let elsewhere) = manner { elsewhere } else { path }
            let message = PublisherProof.signedMessage(
                schema: 2, version: version, path: signedPath, nonce: nonce)
            // try!: signing a message with a key made three lines up. A failure
            // here would be CryptoKit having stopped working, not this program.
            let signature = try! identity.signature(for: message)
            let encodedSignature = signature.base64EncodedString()
            state.lock()
            answered = (nonce: encoded, signature: encodedSignature)
            state.unlock()
            write(
                descriptor,
                #"{"type":"auth","nonce":"\#(encoded)","signature":"\#(encodedSignature)"}"# + "\n")
        }
    }

    private func write(_ descriptor: Int32, _ text: String) {
        var bytes = Array(text.utf8)
        _ = Darwin.write(descriptor, &bytes, bytes.count)
    }
}
