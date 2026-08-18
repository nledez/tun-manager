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

    /// Two minutes at a reading a second, which is as much as the chart can
    /// show without each point being narrower than a pixel.
    private var series = RateSeries(limit: 120)

    var detail: TunnelDetail? {
        guard let selected, let tunnel = tunnels.first(where: { $0.name == selected }) else {
            return nil
        }
        return TunnelDetail(tunnel, now: Date())
    }

    func select(_ tunnel: String?) {
        guard tunnel != selected else { return }
        selected = tunnel
        // The history belongs to the tunnel that was showing. Carrying it over
        // would difference one tunnel's counters against another's.
        series.clear()
        publishRates()
    }

    func add(_ sample: Sample) {
        guard sample.tunnel == selected else { return }
        series.add(at: sample.at, rx: sample.rx, tx: sample.tx)
        publishRates()
    }

    private func publishRates() {
        rates = series.points
        peakDown = series.peakDown
        peakUp = series.peakUp
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

        // An accessory application has nothing frontmost, so without this the
        // window opens behind whatever the user is looking at.
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
        if let tunnel {
            supervisor.watch(tunnel)
        } else {
            supervisor.watchNothing()
        }
    }

    // MARK: - NSWindowDelegate

    func windowWillClose(_ notification: Notification) {
        // Nobody is looking any more, so tun-manager should stop reading
        // counters for us: the same rule the terminal's graph pane follows.
        supervisor.watchNothing()
    }
}
