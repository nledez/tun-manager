/// What the menu bar item looks like.
///
/// A pure decision table, so the choice is testable even though the drawing is
/// not. Health is carried by the *glyph* and never by colour: the menu bar on
/// this system is frequently transparent over an arbitrary wallpaper, a
/// colour-only distinction is invisible to a substantial number of people, and
/// accessibility settings throw it away anyway.
public struct StatusGlyph: Sendable, Equatable {
    /// An SF Symbol name, drawn as a template image.
    public let symbol: String
    /// Drawn faded, the way a disabled control is: nothing is being listened to.
    public let dimmed: Bool
    /// Read aloud by VoiceOver. For an item that conveys everything through a
    /// glyph, this is the whole experience rather than a politeness.
    public let description: String

    public static func of(state: LinkState, snapshot: Snapshot?) -> StatusGlyph {
        switch state {
        case .idle, .connecting, .retrying:
            return StatusGlyph(
                symbol: "shield.slash", dimmed: true,
                description: "Tun Manager: not connected to tun-manager")

        case .blocked:
            return StatusGlyph(
                symbol: "exclamationmark.shield.fill", dimmed: true,
                description: "Tun Manager: this app and tun-manager disagree about the protocol")

        case .live(let sawState):
            guard sawState, let snapshot else {
                return StatusGlyph(
                    symbol: "shield", dimmed: true,
                    description: "Tun Manager: connected, waiting for the first refresh")
            }
            return live(snapshot)
        }
    }

    private static func live(_ snapshot: Snapshot) -> StatusGlyph {
        let tunnels = snapshot.tunnels
        guard !tunnels.isEmpty else {
            // Nothing configured is not the same as everything broken, and a
            // crossed shield would say the second.
            return StatusGlyph(
                symbol: "shield", dimmed: false, description: "Tun Manager: no tunnels configured")
        }

        let up = tunnels.filter { $0.health == .up }.count
        if tunnels.contains(where: { $0.health == .down }) {
            return StatusGlyph(
                symbol: "xmark.shield.fill", dimmed: false,
                description: "Tun Manager: \(up) of \(tunnels.count) tunnels up")
        }
        if tunnels.contains(where: { $0.health != .up }) {
            // Stale, or a health this build does not know. Either way it is not
            // a promise that traffic is flowing.
            return StatusGlyph(
                symbol: "exclamationmark.shield.fill", dimmed: false,
                description: "Tun Manager: \(up) of \(tunnels.count) tunnels up")
        }
        return StatusGlyph(
            symbol: "checkmark.shield.fill", dimmed: false,
            description: "Tun Manager: all \(tunnels.count) tunnels up")
    }
}
