import AppKit
import SwiftUI
import TunManagerFeed

// NOT TESTED: drawing. Every choice it makes was made in TunManagerFeed —
// TunnelDetail decides which rows exist, StatusGlyph decides the symbols,
// RateSeries turns counters into rates. See macos/docs/coverage-gaps.md,
// "the menu bar target".

/// What the window is showing, shared with the SwiftUI view.
@MainActor
final class DetailModel: ObservableObject {
    @Published var tunnels: [TunnelStatus] = []
    @Published var selected: String?
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

    var detail: TunnelDetail? {
        guard let selected, let tunnel = tunnels.first(where: { $0.name == selected }) else {
            return nil
        }
        return TunnelDetail(tunnel, now: Date())
    }

    func select(_ tunnel: String?) {
        guard tunnel != selected else { return }
        selected = tunnel
        publishRates()
    }

    /// Every reading is recorded, not only the one on screen: the tunnels left
    /// behind keep filling, which is what makes coming back to one show an
    /// unbroken graph.
    func add(_ sample: Sample) {
        series[sample.tunnel, default: RateSeries(limit: 120)]
            .add(at: sample.at, rx: sample.rx, tx: sample.tx)
        if sample.tunnel == selected {
            publishRates()
        }
    }

    /// Everything is forgotten when the window closes, along with the
    /// subscriptions: a graph resumed hours later would join two points across
    /// a gap and draw a spike that never happened.
    func forgetHistory() {
        series.removeAll()
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

    /// Shows the window with this tunnel selected, creating it the first time.
    func show(tunnel: String, tunnels: [TunnelStatus]) {
        model.tunnels = tunnels
        model.select(tunnel)
        supervisor.watch(tunnel)

        if window == nil {
            let window = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 720, height: 420),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered, defer: false)
            window.title = "Tun Manager"
            window.contentView = NSHostingView(
                rootView: DetailView(model: model, onSelect: { [weak self] name in
                    self?.select(name)
                }))
            window.isReleasedWhenClosed = false
            window.center()
            window.delegate = self
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
        // The tunnel being shown may have gone from the configuration.
        if let selected = model.selected, !tunnels.contains(where: { $0.name == selected }) {
            select(tunnels.first?.name)
        }
    }

    func add(_ sample: Sample) { model.add(sample) }

    private func select(_ tunnel: String?) {
        model.select(tunnel)
        // No unwatch: the tunnel being left keeps its subscription so its
        // history goes on filling. They are all released when the window
        // closes.
        if let tunnel {
            supervisor.watch(tunnel)
        }
    }

    // MARK: - NSWindowDelegate

    func windowWillClose(_ notification: Notification) {
        // Nobody is looking any more, so tun-manager should stop reading
        // counters for us: the same rule the terminal's graph pane follows.
        supervisor.watchNothing()
        model.forgetHistory()
        // Back to living in the menu bar alone. The status item stays; only the
        // Dock icon and the Command-Tab entry go.
        NSApplication.shared.setActivationPolicy(.accessory)
    }
}
