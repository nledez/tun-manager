import Foundation

/// The feed could not be opened at all.
///
/// The errno is carried rather than swallowed because it is the difference
/// between three sentences the user needs told apart: tun-manager is not
/// running, a socket was left behind by a process that was killed, and the
/// socket belongs to somebody else.
public struct ConnectFailure: Error, Sendable, Equatable {
    public let code: Int32
    public init(code: Int32) { self.code = code }
}

/// The socket path is longer than a `sockaddr_un` can hold.
public struct SocketPathTooLong: Error, Sendable, Equatable {
    public let path: String
    public let limit: Int
}

/// Opens connections to the feed.
public protocol FeedTransport: Sendable {
    func connect() async throws -> any FeedConnection
}

/// One open connection.
public protocol FeedConnection: Sendable {
    /// Bytes as the kernel hands them over, with no framing applied.
    ///
    /// The stream *finishes* on end of stream and *throws* on an I/O error, and
    /// that distinction is the whole reason this throws: a clean end means the
    /// publisher went away, an error means something else did. A plain
    /// AsyncStream would collapse the two.
    var chunks: AsyncThrowingStream<Data, any Error> { get }

    /// Queues a line. Not awaited and not throwing: the feed acknowledges
    /// nothing, so a failed write is not an event — it shows up as the read
    /// ending, which is the only liveness signal worth trusting.
    func send(_ line: Data)

    /// Idempotent.
    func close()
}
