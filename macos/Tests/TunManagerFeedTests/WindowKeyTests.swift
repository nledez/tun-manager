import Testing

@testable import TunManagerFeed

@Test func commandQClosesTheWindowRatherThanTheApplication() {
    // In a menu bar application the window is not the application: quitting
    // from it takes away the icon somebody put in their menu bar, and the way
    // back is to find the app and launch it again. Command-Q here means what
    // Command-W means everywhere, which is what every other menu bar
    // application does with it.
    #expect(WindowKey.closesTheWindow(character: "q", command: true))
}

@Test func commandWClosesTheWindowToo() {
    // The key macOS trains people to use for this.
    #expect(WindowKey.closesTheWindow(character: "w", command: true))
}

@Test func aLetterWithoutCommandClosesNothing() {
    // The window has a Ping button on bare "p". A window that closed on a bare
    // "q" would close while somebody was reaching for it.
    #expect(WindowKey.closesTheWindow(character: "q", command: false) == false)
    #expect(WindowKey.closesTheWindow(character: "w", command: false) == false)
}

@Test func anotherCommandKeyIsLeftAlone() {
    // Command-C, Command-P and the rest belong to whatever the window is doing
    // with them.
    for character in ["c", "p", "r", "a"] {
        #expect(WindowKey.closesTheWindow(character: character, command: true) == false)
    }
}

@Test func theKeyIsReadWithoutRegardToCase() {
    // Caps lock, or shift held from the last thing typed.
    #expect(WindowKey.closesTheWindow(character: "Q", command: true))
    #expect(WindowKey.closesTheWindow(character: "W", command: true))
}
