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

    public static func resolved(_ defaults: UserDefaults = .standard) -> String {
        defaults.string(forKey: defaultsKey) ?? fallback
    }
}
