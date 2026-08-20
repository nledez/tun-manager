import AppKit
import SwiftUI
import TunManagerFeed

// NOT TESTED: drawing. Every choice it makes was made in TunManagerFeed —
// TunnelDetail decides which rows exist, StatusGlyph decides the symbols,
// RateSeries turns counters into rates. See macos/docs/coverage-gaps.md,
// "the menu bar target".

/// The window itself, which exists only to say what Command-Q does.
///
/// An accessory application has no main menu to put a Close item in, so nothing
/// dispatches Command-W or Command-Q while this window is key: without this,
/// one does nothing and the other reaches NSApplication and takes the menu bar
/// icon away with it. WindowKey decides which keystrokes mean "close"; this
/// closes.
private final class DetailPanel: NSWindow {
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        let command = event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command
        guard let character = event.charactersIgnoringModifiers,
            WindowKey.closesTheWindow(character: character, command: command)
        else {
            return super.performKeyEquivalent(with: event)
        }
        performClose(nil)
        return true
    }
}

/// What the window is showing, shared with the SwiftUI view.
@MainActor
final class DetailModel: ObservableObject {
    @Published var tunnels: [TunnelStatus] = []
    @Published var selection: DetailSelection = .overview
    /// The most recent probe of each tunnel, as tun-manager measured it. This
    /// application never probes anything itself: the check addresses are
    /// reachable only through the tunnels, which belong to the root process.
    @Published var pings: [String: Ping] = [:]
    @Published var rates: [Rate] = []
    @Published var peakDown: Double = 0
    @Published var peakUp: Double = 0

    /// One history per tunnel, kept for as long as the window is open. The
    /// subscriptions stay open too, so switching away and back shows a
    /// continuous graph rather than a gap where nobody was looking.
    ///
    /// Two minutes at a reading a second, which is as much as the chart can
    /// show without each point being narrower than a pixel.
    private var series: [String: RateSeries] = [:]
    /// The most recent reading of each watched tunnel, so the totals on screen
    /// are as fresh as the chart beside them.
    private var latest: [String: Sample] = [:]

    /// The tunnel on screen, or nil while the overview is.
    var selected: String? {
        guard case .tunnel(let name) = selection else { return nil }
        return name
    }

    var detail: TunnelDetail? {
        guard let selected, let tunnel = tunnels.first(where: { $0.name == selected }) else {
            return nil
        }
        return TunnelDetail(
            tunnel, latest: latest[tunnel.name], ping: pings[tunnel.name], now: Date())
    }

    /// Every tunnel as a row of the table — the same four columns the terminal
    /// shows. Built here rather than in the view so the choices are testable.
    var rows: [TunnelRow] {
        TunnelTable.rows(tunnels, pings: pings, latest: latest, now: Date())
    }

    func select(_ selection: DetailSelection) {
        guard selection != self.selection else { return }
        self.selection = selection
        publishRates()
    }

    /// Every reading is recorded, not only the one on screen: the tunnels left
    /// behind keep filling, which is what makes coming back to one show an
    /// unbroken graph.
    func add(_ sample: Sample) {
        series[sample.tunnel, default: RateSeries(limit: 120)]
            .add(at: sample.at, rx: sample.rx, tx: sample.tx)
        latest[sample.tunnel] = sample
        if sample.tunnel == selected {
            publishRates()
        }
    }

    /// Everything is forgotten when the window closes, along with the
    /// subscriptions: a graph resumed hours later would join two points across
    /// a gap and draw a spike that never happened.
    func forgetHistory() {
        series.removeAll()
        latest.removeAll()
        publishRates()
    }

    private func publishRates() {
        let showing = selected.flatMap { series[$0] }
        rates = showing?.points ?? []
        peakDown = showing?.peakDown ?? 0
        peakUp = showing?.peakUp ?? 0
    }
}

/// Owns the window and keeps it fed.
@MainActor
final class DetailWindowController: NSObject, NSWindowDelegate {
    private let model = DetailModel()
    private let supervisor: FeedSupervisor
    private var window: NSWindow?

    init(supervisor: FeedSupervisor) {
        self.supervisor = supervisor
        super.init()
    }

    /// Shows the window, creating it the first time.
    ///
    /// - Parameter tunnel: what to select, or nil for the overview.
    func show(tunnel: String?, tunnels: [TunnelStatus]) {
        model.tunnels = tunnels
        model.pings = supervisor.pings
        model.select(tunnel.map(DetailSelection.tunnel) ?? .overview)
        if let tunnel {
            supervisor.watch(tunnel)
        }
        // One round on opening, so the latency column is not blank on arrival.
        // The publisher accepts at most one every two seconds, which is what
        // makes asking at the moment somebody looks safe.
        supervisor.askForPing(tunnel)

        if window == nil {
            // Wide enough for the whole table without a horizontal scroll: six
            // columns, and the last of them holds an endpoint, which can be an
            // IPv6 address with a port on the end. A window that opens needing
            // to be resized before it can be read is a window that opens wrong.
            let window = DetailPanel(
                contentRect: NSRect(x: 0, y: 0, width: 1280, height: 680),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered, defer: false)
            window.title = "Tun Manager"
            window.contentView = NSHostingView(
                rootView: DetailView(
                    model: model,
                    onSelect: { [weak self] selection in self?.select(selection) },
                    onPing: { [weak self] name in self?.supervisor.askForPing(name) }))
            window.isReleasedWhenClosed = false
            window.delegate = self

            // Whatever size somebody settles on is theirs from then on; the
            // default above only decides the first time. The name is the
            // bundle's, so the development build cannot move the installed
            // one's window.
            window.setFrameAutosaveName(Bundle.main.bundleIdentifier.map { $0 + ".detail" } ?? "detail")
            if !window.setFrameUsingName(window.frameAutosaveName) {
                window.center()
            }
            self.window = window
        }

        // An accessory application has no Dock icon, no Command-Tab entry, and
        // nothing frontmost — so its windows open behind whatever the user is
        // actually looking at. Becoming a regular application while a window is
        // up fixes all three, and the status item is unaffected either way.
        NSApplication.shared.setActivationPolicy(.regular)
        NSApplication.shared.activate()
        window?.makeKeyAndOrderFront(nil)
    }

    func update(tunnels: [TunnelStatus]) {
        model.tunnels = tunnels
        model.pings = supervisor.pings
        // The tunnel being shown may have gone from the configuration. Falling
        // back to the overview rather than to another tunnel: silently
        // switching to a neighbour is how somebody reads the wrong graph.
        if let selected = model.selected, !tunnels.contains(where: { $0.name == selected }) {
            select(.overview)
        }
    }

    /// A round of probes arrived, or the link moved. Either way the numbers on
    /// screen come from the supervisor rather than from anything kept here.
    func refreshPings() { model.pings = supervisor.pings }

    func add(_ sample: Sample) { model.add(sample) }

    private func select(_ selection: DetailSelection) {
        model.select(selection)
        // What each selection asks for is decided by DetailSelection, where a
        // test can reach it. No unwatch here: the tunnel being left keeps its
        // subscription so its history goes on filling, and they are all
        // released when the window closes.
        if let watched = selection.watches {
            supervisor.watch(watched)
        }
        supervisor.askForPing(selection.probes)
    }

    // MARK: - NSWindowDelegate

    func windowWillClose(_ notification: Notification) {
        // Nobody is looking any more, so tun-manager should stop reading
        // counters for us: the same rule the terminal's graph pane follows.
        supervisor.watchNothing()
        model.forgetHistory()
        // Back to the overview, so reopening does not land on whatever was last
        // looked at with an empty graph under it.
        model.select(.overview)
        // Back to living in the menu bar alone. The status item stays; only the
        // Dock icon and the Command-Tab entry go.
        NSApplication.shared.setActivationPolicy(.accessory)
    }
}
