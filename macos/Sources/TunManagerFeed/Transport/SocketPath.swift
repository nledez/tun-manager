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
        if let fromFlag = value(in: arguments) {
            return fromFlag
        }
        return defaults.string(forKey: defaultsKey) ?? fallback
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
