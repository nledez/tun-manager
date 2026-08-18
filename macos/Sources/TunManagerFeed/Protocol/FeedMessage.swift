/// Everything the publisher can say.
public enum FeedMessage: Sendable, Equatable {
    /// The first line on every connection. `schema` is the contract's version.
    case hello(schema: Int, version: String)
    case state(Snapshot)
    case sample(Sample)
    /// An orderly shutdown. Distinct from an end of stream, which means a crash
    /// or that this client was dropped for falling behind.
    case bye
}
