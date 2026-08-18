import Testing

@testable import TunManagerFeed

@Test func theInstalledApplicationIsARelease() {
    #expect(Flavour(bundleIdentifier: "net.ledez.tun-manager.menubar") == .release)
}

@Test func aDevelopmentBuildIsToldApartByItsIdentifier() {
    // The identifier is what actually keeps the two apart — it governs the
    // notification permission, the remembered position in the menu bar, and the
    // defaults domain, which is where a stray FeedSocket key would otherwise be
    // shared between them.
    #expect(Flavour(bundleIdentifier: "net.ledez.tun-manager.menubar.dev") == .development)
}

@Test func aProcessWithNoBundleAtAllCountsAsDevelopment() {
    // `swift run` produces one. Treating it as a release would have it fight
    // the installed application for the menu bar position it remembers.
    #expect(Flavour(bundleIdentifier: nil) == .development)
}

@Test func eachFlavourRemembersItsMenuBarPositionSeparately() {
    #expect(
        Flavour.release.statusItemAutosaveName != Flavour.development.statusItemAutosaveName)
}

@Test func onlyTheDevelopmentBuildIsColoured() {
    // A release keeps a template image, which the menu bar tints for itself and
    // which stays legible over any wallpaper. Colour is the one thing that
    // tells two shields apart at a glance, and it is worth its cost only on a
    // build nobody but its author runs.
    #expect(!Flavour.release.isTinted)
    #expect(Flavour.development.isTinted)
}
