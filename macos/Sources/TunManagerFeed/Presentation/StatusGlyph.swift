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

    /// The needed group is what has to work; extra is offered rather than
    /// expected, and a tunnel in it being off is the ordinary state of affairs.
    /// Judging every tunnel alike showed a crossed shield because an optional
    /// tunnel was down, which is the menu bar crying wolf — and the only value
    /// a status icon has is that it is believed.
    private static func live(_ snapshot: Snapshot) -> StatusGlyph {
        let tunnels = snapshot.tunnels
        guard !tunnels.isEmpty else {
            // Nothing configured is not the same as everything broken, and a
            // crossed shield would say the second.
            return StatusGlyph(
                symbol: "shield", dimmed: false, description: "Tun Manager: no tunnels configured")
        }

        let needed = tunnels.filter { $0.group == GroupName.needed }
        // With no needed group at all, "every needed tunnel is up" would be
        // true because there are none, and a configuration that defines no
        // groups would show all-clear with everything down.
        let judged = needed.isEmpty ? tunnels : needed
        let others = needed.isEmpty ? [] : tunnels.filter { $0.group != GroupName.needed }

        let up = judged.filter { $0.health == .up }.count
        let othersDown = others.filter { $0.health != .up }.count
        let what = needed.isEmpty ? "tunnels" : "needed"

        if judged.contains(where: { $0.health == .down }) {
            return StatusGlyph(
                symbol: "xmark.shield.fill", dimmed: false,
                description: describe(up, of: judged.count, what, othersDown))
        }
        if judged.contains(where: { $0.health != .up }) {
            // Stale, or a health this build does not know. Up, and nothing
            // getting through: not down, and not fine either.
            return StatusGlyph(
                symbol: "exclamationmark.shield.fill", dimmed: false,
                description: describe(up, of: judged.count, what, othersDown))
        }
        return StatusGlyph(
            // Outlined rather than filled when something optional is off. Worth
            // seeing at a glance, not worth an alarm.
            symbol: othersDown == 0 ? "checkmark.shield.fill" : "checkmark.shield",
            dimmed: false,
            description: describe(up, of: judged.count, what, othersDown))
    }

    private static func describe(_ up: Int, of total: Int, _ what: String, _ othersDown: Int)
        -> String
    {
        var sentence = "Tun Manager: \(up) of \(total) \(what) tunnels up"
        if othersDown > 0 {
            sentence += ", \(othersDown) other\(othersDown == 1 ? "" : "s") down"
        }
        return sentence
    }
}
