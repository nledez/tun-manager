import Foundation

/// The verbs this client may send.
///
/// The publisher accepts three — `watch`, `unwatch` and `refresh` — and none of
/// them can start or stop a tunnel. Version 1 uses only `refresh`; the other
/// two arrive with the traffic graphs.
///
/// Written as literal bytes rather than encoded. These are two fixed strings,
/// and a JSONEncoder would add a failure path to a function that cannot fail.
public enum ClientCommand: String, Sendable, Equatable, CaseIterable {
    /// Asks for a fresh view. The publisher accepts at most one every two
    /// seconds and acknowledges none of them: a refresh is observed as the
    /// `state` line that follows, or not at all.
    case refresh

    public var line: Data {
        Data(#"{"type":"\#(rawValue)"}"#.utf8) + Data([UInt8(ascii: "\n")])
    }
}
