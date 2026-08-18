import Foundation

/// Parses the two timestamp shapes the feed puts on the wire.
///
/// They differ, and not by accident. `taken` is a Go `time.Time` marshalled by
/// `encoding/json`, which writes RFC 3339 with as many fractional digits as the
/// value has and none at all when the nanoseconds are zero. `last_handshake` is
/// a string the publisher pre-formatted with `time.RFC3339`, which never has a
/// fraction. So this has to accept zero to nine fractional digits on one field
/// and exactly zero on the other.
///
/// Both of Foundation's strategies were measured on this toolchain and both
/// accept every shape, fraction preserved. Rather than depend on that being
/// true forever, each one is tried in turn — and `RFC3339Tests` pins all five
/// shapes, so a Foundation that tightened either would fail there rather than
/// in front of somebody whose menu bar had quietly stopped updating.
public enum RFC3339 {
    private static let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let whole = Date.ISO8601FormatStyle(includingFractionalSeconds: false)

    /// Returns nil rather than throwing: a timestamp this client cannot read is
    /// a line it should skip, not a connection it should drop.
    public static func date(from text: String) -> Date? {
        if let date = try? fractional.parse(text) {
            return date
        }
        return try? whole.parse(text)
    }
}
