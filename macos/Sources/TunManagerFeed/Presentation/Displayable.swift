import Foundation

/// Makes a string from the publisher safe to put on screen.
///
/// Everything this application shows arrived over a socket: tunnel names,
/// endpoints, check addresses, the reason a probe failed, the name of the
/// network context. `tun-manager` reads most of it out of files, and one of
/// those files — the configuration under the user's home — is one any process
/// running as that user can rewrite. So none of it is treated as this
/// application's own text.
///
/// Three different kinds of damage, and each is removed for its own reason:
///
///   - **Control characters**, which are how a value stops being a value.
///     AppKit will happily draw a newline inside a menu item, and the same
///     string is written to the log and read in a terminal.
///   - **The characters that reorder text** — the bidirectional overrides and
///     isolates, and the invisible marks that go with them. They are how
///     `moc.elpmaxe` is drawn as `example.com`, which matters here because most
///     of what this shows is somewhere traffic goes.
///   - **Length.** A ten thousand character name is not a lie, but it takes the
///     menu apart, and a menu somebody cannot read is a menu that tells them
///     nothing.
///
/// It is for display and nothing else. A name used to ask the publisher about a
/// tunnel — a watch, a probe, the row somebody clicked — has to be the name the
/// publisher knows, so those keep the original.
public enum Displayable {
    /// Long enough for a hostname and a port, short enough that one value
    /// cannot own the screen. The same number the Go side uses.
    public static let limit = 96

    public static func of(_ text: String) -> String {
        var kept = String.UnicodeScalarView()
        kept.reserveCapacity(text.unicodeScalars.count)
        for scalar in text.unicodeScalars {
            if scalar == "\t" {
                // The one control character with a defensible meaning in a
                // value, and it still cannot be allowed to shift a column.
                kept.append(" ")
                continue
            }
            guard !isRemoved(scalar) else { continue }
            kept.append(scalar)
        }

        let cleaned = String(kept)
        guard cleaned.count > limit else { return cleaned }
        // Cut by Character rather than by scalar: an accented letter and a flag
        // are each one thing on screen, and half of either is not.
        return String(cleaned.prefix(limit - 1)) + "…"
    }

    private static func isRemoved(_ scalar: Unicode.Scalar) -> Bool {
        if scalar.properties.generalCategory == .control { return true }
        switch scalar.value {
        case 0x200E...0x200F,  // left-to-right and right-to-left marks
            0x202A...0x202E,  // embeddings and overrides
            0x2066...0x2069:  // isolates
            return true
        default:
            return false
        }
    }
}
