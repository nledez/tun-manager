import Foundation

/// Turns numbers into the strings the menu shows.
///
/// The age format deliberately mirrors internal/format on the Go side, so the
/// menu bar and the terminal read the same way: `12s`, `9m04s`, `2h34m`. Not
/// Foundation's relative style, which is locale-dependent and would make these
/// tests assert on whatever the machine happened to be set to.
public enum Formatting {
    /// A byte count, or nothing at all.
    ///
    /// Zero renders as "—" rather than "0 bytes", the same choice the table
    /// makes: a tunnel that is down has no traffic to report, and a zero reads
    /// like a measurement.
    /// The locale is a parameter so a test can pin the output. Byte counts are
    /// localised — the separator and the unit name both move — so asserting on
    /// them without fixing it would be asserting on whatever the machine
    /// running the suite happens to be set to.
    public static func bytes(_ count: Int64, locale: Locale = .autoupdatingCurrent) -> String {
        guard count > 0 else { return "—" }
        return count.formatted(.byteCount(style: .memory).locale(locale))
    }

    /// How long ago, zero-padded so a column of them stays aligned.
    public static func age(_ interval: TimeInterval) -> String {
        // A clock adjustment can put a handshake in the future, and a negative
        // age would render as garbage.
        let seconds = Int(max(0, interval.rounded()))
        switch seconds {
        case ..<60:
            return "\(seconds)s"
        case ..<3600:
            return String(format: "%dm%02ds", seconds / 60, seconds % 60)
        default:
            return String(format: "%dh%02dm", seconds / 3600, (seconds % 3600) / 60)
        }
    }

    /// The context line, assembled from the parts the wire actually gave.
    ///
    /// Absent parts are omitted rather than rendered as empty brackets: the
    /// publisher omits `interface` and `address` when no rule matched, and
    /// "office ( )" would be a worse answer than "office".
    public static func context(_ context: FeedContext) -> String {
        // Every part of this came from the publisher, and the name in
        // particular comes from the configuration under the user's home - the
        // one file tun-manager reads that a process running as that user can
        // rewrite.
        let name = context.name.isEmpty ? "no network context" : Displayable.of(context.name)
        let parts = [context.interface, context.address].compactMap { $0 }.map(Displayable.of)
        guard !parts.isEmpty else { return name }
        return "\(name) — \(parts.joined(separator: " · "))"
    }
}
