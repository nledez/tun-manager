/// What a keystroke in the detail window means.
///
/// A decision rather than a switch inside an NSWindow subclass, because it is
/// one: this application is a menu bar item that happens to have a window, and
/// what Command-Q does to it is a choice somebody could reasonably make the
/// other way.
public enum WindowKey {
    /// Whether this keystroke should close the window.
    ///
    /// Command-Q as well as Command-W. In a menu bar application the window is
    /// not the application: quitting from it takes away the icon somebody put
    /// in their menu bar, and the way back is to go and find the application
    /// again. Quit Tun Manager, in the menu, remains the way to actually stop
    /// it — the one place where that is unambiguously what was asked for.
    public static func closesTheWindow(character: String, command: Bool) -> Bool {
        guard command else { return false }
        return character.lowercased() == "q" || character.lowercased() == "w"
    }
}
