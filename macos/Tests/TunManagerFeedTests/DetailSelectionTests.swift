import Testing

@testable import TunManagerFeed

@Test func pickingATunnelSubscribesToIt() {
    #expect(DetailSelection.tunnel("alpha").watches == "alpha")
}

@Test func theOverviewSubscribesToNothing() {
    // It draws from the state that arrives anyway. A subscription for it would
    // cost tun-manager a control-socket read per second per tunnel, to fill a
    // column that changes every five minutes.
    #expect(DetailSelection.overview.watches == nil)
}

@Test func pickingATunnelProbesThatOneAlone() {
    // A client asking about one tunnel must not make the publisher send packets
    // to every address it knows.
    #expect(DetailSelection.tunnel("alpha").probes == "alpha")
}

@Test func theOverviewProbesEveryTunnel() {
    // It has a latency column for each. Asking per tunnel as they are selected
    // leaves that column half filled: the rows nobody happened to click stay
    // blank, which reads as "no answer" rather than as "never asked".
    //
    // Nil is how ClientCommand.ping spells "all of them", so this is the value
    // that goes on the wire unchanged.
    #expect(DetailSelection.overview.probes == nil)
}

@Test func everySelectionAsksForAProbeOfSomething() {
    // Neither case opts out. A view that showed a latency column and never
    // asked for one would be a column that is always empty.
    for selection in [DetailSelection.overview, .tunnel("alpha")] {
        #expect(ClientCommand.ping(selection.probes).line.isEmpty == false)
    }
}

@Test func aSelectionIsComparedByWhatItPointsAt() {
    // The window skips the work when the selection has not moved, which needs
    // two selections of the same tunnel to be equal.
    #expect(DetailSelection.tunnel("alpha") == DetailSelection.tunnel("alpha"))
    #expect(DetailSelection.tunnel("alpha") != DetailSelection.tunnel("bravo"))
    #expect(DetailSelection.overview != DetailSelection.tunnel("overview"))
}

@Test func openingARowFromTheOverviewPointsAtThatTunnel() {
    // Double-clicking a row in "All tunnels" is how somebody asks for the
    // detail of the tunnel they are looking at, rather than going back to the
    // sidebar to find the same name again.
    #expect(DetailSelection.opening(["alpha"]) == .tunnel("alpha"))
}

@Test func openingNothingChangesNothing() {
    // A double-click that lands between rows hands over an empty set. Treating
    // that as a selection would blank the table for no reason.
    #expect(DetailSelection.opening([]) == nil)
}

@Test func openingSeveralRowsChangesNothing() {
    // The table allows more than one row to be selected, and there is one
    // detail pane. Picking whichever came first out of an unordered set would
    // open a tunnel nobody chose.
    #expect(DetailSelection.opening(["alpha", "bravo"]) == nil)
}
