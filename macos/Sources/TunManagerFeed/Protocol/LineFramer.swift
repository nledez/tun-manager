import Foundation

/// Turns the chunks the kernel hands over into whole lines.
///
/// The feed is newline-delimited JSON, and a read(2) knows nothing about that:
/// one chunk can hold half a line, two lines, or two and a half. Reassembly is
/// the consumer's job.
///
/// A framer belongs to one connection and dies with it. That is the invariant
/// worth stating, because keeping one across a reconnection would carry the
/// half-line a crashed publisher left behind into the first line of the next
/// connection, and produce one corrupt message that no test would ever see.
public struct LineFramer {
    /// The longest line that will be assembled before giving up on it.
    ///
    /// The publisher's own reader caps client lines at 64 KiB, but nothing caps
    /// what it sends us, and a `state` line grows with the number of tunnels.
    /// A megabyte is far past any real view and still refuses to let a process
    /// we do not control spend our memory.
    public static let defaultLimit = 1 << 20

    private let limit: Int
    private var buffer = Data()
    /// Set when the buffer overflowed: everything up to the next newline is
    /// rubbish by definition, since the boundary it belonged to is gone.
    private var resyncing = false

    public init(limit: Int = LineFramer.defaultLimit) {
        self.limit = limit
    }

    /// Appends a chunk and returns whatever complete lines it finished.
    public mutating func push(_ chunk: Data) -> [Data] {
        buffer.append(chunk)
        var lines: [Data] = []

        while let newline = buffer.firstIndex(of: UInt8(ascii: "\n")) {
            // Copied before the buffer is mutated, not after. A Data slice
            // shares the buffer's storage, so building the Data once
            // removeSubrange had run read memory that was no longer ours —
            // which crashed inside malloc, at random, on whichever line
            // happened to arrive.
            let line = Data(buffer[buffer.startIndex..<newline])
            buffer.removeSubrange(buffer.startIndex...newline)

            if resyncing {
                // The newline that ends the oversized line is the one that puts
                // us back in step; the line itself is already unrecoverable.
                resyncing = false
                continue
            }
            // Empty lines carry nothing and would only make the decoder say so.
            if !line.isEmpty {
                lines.append(line)
            }
        }

        if buffer.count > limit {
            buffer.removeAll(keepingCapacity: false)
            resyncing = true
        }
        return lines
    }
}
