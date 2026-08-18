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

    public init(path: String) {
        self.path = path
    }

    public func connect() async throws -> any FeedConnection {
        let bytes = Array(path.utf8)
        guard bytes.count <= Self.pathLimit else {
            // Checked here so the failure names itself. Handed to the kernel it
            // would come back as a puzzling EINVAL from a truncated path.
            throw SocketPathTooLong(path: path, limit: Self.pathLimit)
        }

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

        return UnixSocketConnection(descriptor: descriptor)
    }
}

/// One open connection, reading through a Dispatch channel.
public final class UnixSocketConnection: FeedConnection {
    private let io: DispatchIO
    private let queue: DispatchQueue
    public let chunks: AsyncThrowingStream<Data, any Error>

    public init(descriptor: Int32) {
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
                continuation.yield(Data(data))
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
}
