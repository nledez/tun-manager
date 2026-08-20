import Foundation

/// Where the feed's socket is.
///
/// The publisher's `feed_socket` setting can move it, and an application that
/// hardcoded the default would show "not running" forever to anyone who did,
/// with no clue why. Reading the Go configuration is deliberately not the
/// answer: it lives under the pre-sudo user's home and parsing it here would be
/// a second parser of a format guaranteed to drift. A defaults key is enough,
/// and `tun-manager doctor` already prints the path in use.
public enum SocketPath {
    public static let fallback = "/var/run/tun-manager.sock"
    public static let defaultsKey = "FeedSocket"

    /// The flag that beats every other source.
    public static let flag = "--socket"

    /// A socket and where the choice of it came from.
    ///
    /// The provenance matters because it decides one rule: a path named on the
    /// command line is a demo, and a demo publisher does not run as root. The
    /// defaults key is not a demo — it exists for an installation that moved
    /// `feed_socket`, which is still root's.
    public struct Choice: Sendable, Equatable {
        public let path: String
        /// True only for `--socket`.
        public let isDemo: Bool

        public init(path: String, isDemo: Bool) {
            self.path = path
            self.isDemo = isDemo
        }
    }

    /// Where to connect and where that came from, in order: the flag, the
    /// defaults key, the default.
    public static func chosen(
        arguments: [String] = CommandLine.arguments,
        defaults: UserDefaults = .standard
    ) -> Choice {
        if let fromFlag = value(in: arguments) {
            return Choice(path: fromFlag, isDemo: true)
        }
        return Choice(path: defaults.string(forKey: defaultsKey) ?? fallback, isDemo: false)
    }

    /// Where to connect, in order: the flag, the defaults key, the default.
    ///
    /// The flag wins because it leaves nothing behind. A `defaults write`
    /// persists until somebody remembers to delete it, and a key left pointing
    /// at a demo publisher is a menu bar quietly listing tunnels that do not
    /// exist - which has happened.
    public static func resolved(
        arguments: [String] = CommandLine.arguments,
        defaults: UserDefaults = .standard
    ) -> String {
        chosen(arguments: arguments, defaults: defaults).path
    }

    /// Reads `--socket PATH` or `--socket=PATH`, whichever was written.
    ///
    /// Hand-read rather than taken from UserDefaults' argument domain, which
    /// would spell it `-FeedSocket PATH`. That form works and always has; this
    /// one is the one a person guesses, and it is what `--help` can name.
    private static func value(in arguments: [String]) -> String? {
        var rest = arguments.dropFirst()
        while let argument = rest.first {
            rest = rest.dropFirst()
            if argument == flag {
                // A trailing "--socket" with nothing after it names nothing, so
                // it is ignored rather than read as an empty path.
                return rest.first
            }
            if argument.hasPrefix(flag + "=") {
                return String(argument.dropFirst(flag.count + 1))
            }
        }
        return nil
    }
}
