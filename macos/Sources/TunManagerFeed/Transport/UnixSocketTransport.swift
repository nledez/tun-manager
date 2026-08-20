import Darwin
import Dispatch
import Foundation

/// Opens the feed's unix socket.
///
/// This is the only file in the program that touches a file descriptor, and it
/// is deliberately small. Everything above it works in terms of `FeedTransport`
/// and can be tested with a fake.
///
/// Network.framework was the obvious alternative and was rejected for two
/// reasons. It owns a retry policy of its own, which cannot be inspected or
/// tested, while this program needs one anyway — a goodbye, a bare end of
/// stream and a socket that was never there all deserve different delays. And
/// it flattens the three connect failures that need three different sentences
/// into `.waiting`, from which the errno has to be dug back out.
public struct UnixSocketTransport: FeedTransport {
    /// sockaddr_un.sun_path is char[104] on this SDK, and the path must be
    /// NUL-terminated inside it.
    static let pathLimit = 103

    private let path: String
    private let policy: PeerPolicy
    /// The two lookups, injected so the rule can be exercised against uids a
    /// test chooses rather than against whoever happens to run the suite.
    private let peer: @Sendable (Int32) -> UInt32?
    private let owner: @Sendable (String) -> UInt32?

    /// - Parameter policy: whether whoever is listening has to be root. It is
    ///   not, and only is not, for a publisher named with `--socket`.
    public init(
        path: String,
        policy: PeerPolicy = PeerPolicy(requiresRoot: true),
        peer: @escaping @Sendable (Int32) -> UInt32? = peerUID,
        owner: @escaping @Sendable (String) -> UInt32? = ownerOfFile
    ) {
        self.path = path
        self.policy = policy
        self.peer = peer
        self.owner = owner
    }

    public func connect() async throws -> any FeedConnection {
        let bytes = Array(path.utf8)
        guard bytes.count <= Self.pathLimit else {
            // Checked here so the failure names itself. Handed to the kernel it
            // would come back as a puzzling EINVAL from a truncated path.
            throw SocketPathTooLong(path: path, limit: Self.pathLimit)
        }

        // Before the connect, because this one is about the name: whoever can
        // write the directory can put their own socket where root's was, and
        // the cheapest moment to find that out is before a byte is exchanged.
        try policy.check(socketOwner: owner(path))

        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw ConnectFailure(code: errno) }

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.baseAddress!.copyMemory(from: bytes, byteCount: bytes.count)
        }

        // A blocking connect is safe here in a way it would not be over a
        // network: AF_UNIX does no name resolution and no handshake, so it
        // either finds a listener in the backlog or fails at once.
        let connected = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0 else {
            let code = errno
            close(descriptor)
            throw ConnectFailure(code: code)
        }

        // And after it, because this one is about the process: the file says
        // who bound the socket, LOCAL_PEERCRED says who is answering on it now.
        // A socket root left behind and somebody else took over passes the
        // first check and fails this one.
        do {
            try policy.check(peer: peer(descriptor))
        } catch {
            close(descriptor)
            throw error
        }

        return UnixSocketConnection(descriptor: descriptor)
    }
}

/// One open connection, reading through a Dispatch channel.
public final class UnixSocketConnection: FeedConnection {
    private let io: DispatchIO
    private let queue: DispatchQueue
    public let chunks: AsyncThrowingStream<Data, any Error>

    public init(descriptor: Int32) {
        // Without this, writing to a socket whose peer has gone raises SIGPIPE,
        // whose default disposition is to kill the process. The publisher comes
        // and goes constantly — that is the normal condition, not an edge case
        // — and this application sends a refresh the moment somebody opens the
        // menu. So the one race that matters is: tun-manager exits, the user
        // opens the menu, the write lands on a dead socket, and the menu bar
        // item vanishes. With SO_NOSIGPIPE the write returns EPIPE instead, and
        // the reader reports the end of stream a moment later, which is the
        // path the state machine already handles.
        var on: Int32 = 1
        setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))

        let queue = DispatchQueue(label: "net.ledez.tun-manager.feed.io")
        self.queue = queue

        // DispatchIO does not own the descriptor; this handler is the one and
        // only place it is closed, whatever ends the connection.
        let io = DispatchIO(type: .stream, fileDescriptor: descriptor, queue: queue) { _ in
            // Qualified: inside this class `close` would resolve to the
            // connection's own method rather than the system call.
            Darwin.close(descriptor)
        }
        // Dispatch documents the default low-water mark as *unspecified*, which
        // means the channel is entitled to withhold a complete line while it
        // waits for more bytes. The publisher sends one line every five
        // minutes, so that would show up as a menu bar minutes behind with no
        // error to explain it. One byte is what "hand it over as it arrives"
        // has to be spelled as.
        io.setLimit(lowWater: 1)
        io.setLimit(highWater: 64 * 1024)
        self.io = io

        var handle: AsyncThrowingStream<Data, any Error>.Continuation!
        self.chunks = AsyncThrowingStream { handle = $0 }
        let continuation = handle!

        // This closure is NOT @Sendable — Dispatch does not declare it so, and
        // Swift 6 will therefore not complain about anything captured in it.
        // It is the one unguarded edge in the program, so it captures nothing
        // but a Sendable continuation: all buffering happens on the consumer
        // side, where the compiler is watching.
        io.read(offset: 0, length: Int.max, queue: queue) { done, data, error in
            if error != 0 {
                continuation.finish(throwing: ConnectFailure(code: error))
                return
            }
            if let data, !data.isEmpty {
                // Copied out explicitly rather than through Data's Sequence
                // initialiser. The DispatchData handed to this handler is only
                // guaranteed for the length of the call, and this is the one
                // place in the program where memory owned by something else
                // crosses a boundary — worth being obvious about rather than
                // trusting an initialiser to do the right thing.
                var copy = Data(count: data.count)
                copy.withUnsafeMutableBytes { destination in
                    _ = data.copyBytes(to: destination)
                }
                continuation.yield(copy)
            }
            if done {
                continuation.finish()
            }
        }

        // One teardown path for cancellation, end of stream, error and close():
        // stopping the channel runs the cleanup handler, which closes the
        // descriptor.
        continuation.onTermination = { _ in io.close(flags: DispatchIO.CloseFlags.stop) }
    }

    public func send(_ line: Data) {
        let data = line.withUnsafeBytes { DispatchData(bytes: $0) }
        // The completion is ignored on purpose. The feed acknowledges nothing,
        // so a failed write tells us only what the read is about to.
        io.write(offset: 0, data: data, queue: queue) { _, _, _ in }
    }

    public func close() {
        io.close(flags: DispatchIO.CloseFlags.stop)
    }

    deinit {
        // A connection let go of without being closed would leave an armed read
        // on a background queue, holding a descriptor and a continuation whose
        // consumer has gone. Closing is idempotent, so this costs nothing on
        // the ordinary path and closes the leak on the one where somebody
        // forgot.
        io.close(flags: DispatchIO.CloseFlags.stop)
    }
}
