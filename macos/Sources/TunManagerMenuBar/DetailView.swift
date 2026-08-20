import Charts
import SwiftUI
import TunManagerFeed

// NOT TESTED: drawing. See macos/docs/coverage-gaps.md, "the menu bar target".

/// The tunnel list on the left, what it is doing on the right.
struct DetailView: View {
    @ObservedObject var model: DetailModel
    let onSelect: (DetailSelection) -> Void
    /// Asks tun-manager to probe a tunnel, or every one it knows when nil.
    let onPing: (String?) -> Void

    var body: some View {
        NavigationSplitView {
            List(selection: selection) {
                Label("All tunnels", systemImage: "list.bullet.rectangle")
                    .tag(DetailSelection.overview)
                ForEach(sections, id: \.header) { section in
                    Section(section.header) {
                        ForEach(section.tunnels, id: \.name) { tunnel in
                            Label {
                                Text(tunnel.name)
                            } icon: {
                                Image(systemName: symbol(tunnel.health))
                            }
                            .tag(DetailSelection.tunnel(tunnel.name))
                        }
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200, max: 280)
        } detail: {
            if let detail = model.detail {
                TunnelPane(detail: detail, model: model)
            } else {
                OverviewPane(model: model, onOpen: onSelect)
            }
        }
        // The floor is what the six columns need side by side, plus the
        // sidebar. Below it the table starts clipping rather than shrinking.
        .frame(minWidth: 820, minHeight: 380)
        .toolbar {
            // On the split view rather than in a pane, so it is there whichever
            // one is showing. A round of probes costs packets sent by a process
            // running as root, so it is asked for and never run on a timer.
            //
            // Bare "p", the key the terminal uses: this window has nothing to
            // type into, so there is nothing for it to collide with.
            Button("Ping", systemImage: "dot.radiowaves.left.and.right") { onPing(nil) }
                .keyboardShortcut("p", modifiers: [])
                .help("Ask tun-manager to probe every tunnel (p)")
        }
    }

    private var selection: Binding<DetailSelection?> {
        Binding(get: { model.selection }, set: { if let choice = $0 { onSelect(choice) } })
    }

    /// Grouped the way the menu groups them, so the two read alike.
    private var sections: [(header: String, tunnels: [TunnelStatus])] {
        let byGroup = Dictionary(grouping: model.tunnels, by: \.group)
        let rank = { (group: String) in
            group == GroupName.needed ? 0 : group == GroupName.extra ? 1 : 2
        }
        return
            byGroup.keys
            .sorted { rank($0) == rank($1) ? $0 < $1 : rank($0) < rank($1) }
            .map {
                (header: $0.isEmpty ? "no group" : $0,
                 tunnels: (byGroup[$0] ?? []).sorted { $0.name < $1.name })
            }
    }

    private func symbol(_ health: Health) -> String {
        switch health {
        case .up: "checkmark.circle.fill"
        case .stale: "exclamationmark.triangle.fill"
        case .down: "xmark.circle"
        case .unknown: "questionmark.circle"
        }
    }
}

/// One tunnel: its facts, then what is going through it.
private struct TunnelPane: View {
    let detail: TunnelDetail
    @ObservedObject var model: DetailModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                header
                facts
                traffic
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .navigationTitle(detail.name)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Circle().fill(colour).frame(width: 10, height: 10)
            Text(detail.health.wireName).font(.headline)
            Text(detail.group).foregroundStyle(.secondary)
        }
    }

    private var colour: Color {
        switch detail.health {
        case .up: .green
        case .stale: .orange
        case .down: .secondary
        case .unknown: .purple
        }
    }

    @ViewBuilder private var facts: some View {
        if !detail.facts.isEmpty {
            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 6) {
                ForEach(detail.facts, id: \.label) { fact in
                    GridRow {
                        Text(fact.label).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                        // Monospaced so a column of addresses and counts lines
                        // up rather than drifting with each glyph's width.
                        Text(fact.value).monospaced().textSelection(.enabled)
                    }
                }
            }
        }
    }

    @ViewBuilder private var traffic: some View {
        Divider()
        if model.rates.isEmpty {
            // It takes two readings a second apart before there is a rate at
            // all, and a blank chart in the meantime looks like a broken one.
            Label("Waiting for the first reading…", systemImage: "clock")
                .foregroundStyle(.secondary)
        } else {
            // Each direction on its own scale: sharing one would flatten a
            // typical upload into a line along the axis under any download
            // worth watching.
            chart("Received", model.rates.map(\.down), peak: model.peakDown, colour: .green)
            chart("Sent", model.rates.map(\.up), peak: model.peakUp, colour: .blue)
        }
    }

    private func chart(_ title: String, _ values: [Double], peak: Double, colour: Color)
        -> some View
    {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title).font(.subheadline).foregroundStyle(.secondary)
                Spacer()
                // The shape says how the traffic moved and nothing about how
                // much of it there was, so the peak is named beside it.
                Text("peak \(rate(peak))").font(.caption).monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            Chart(Array(values.enumerated()), id: \.offset) { point in
                AreaMark(x: .value("t", point.offset), y: .value("B/s", point.element))
                    .foregroundStyle(colour.opacity(0.25))
                LineMark(x: .value("t", point.offset), y: .value("B/s", point.element))
                    .foregroundStyle(colour)
            }
            .chartYScale(domain: 0...max(peak, 1))
            .chartXAxis(.hidden)
            .frame(height: 70)
        }
    }

    private func rate(_ bytesPerSecond: Double) -> String {
        Int64(bytesPerSecond).formatted(.byteCount(style: .memory)) + "/s"
    }
}

/// Every tunnel at once, in the four columns the terminal shows.
private struct OverviewPane: View {
    @ObservedObject var model: DetailModel
    /// What a double-click on a row asks for: the detail of that tunnel.
    let onOpen: (DetailSelection) -> Void
    @State private var picked: Set<TunnelRow.ID> = []

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if model.rows.isEmpty {
                ContentUnavailableView(
                    "No tunnels", systemImage: "shield",
                    description: Text("tun-manager has not reported any."))
            } else {
                table
            }
        }
        .navigationTitle("All tunnels")
    }

    private var table: some View {
        Table(model.rows, selection: $picked) {
            TableColumn("TUNNEL") { row in
                Label {
                    Text(row.name)
                } icon: {
                    Image(systemName: symbol(row.health)).foregroundStyle(colour(row.health))
                }
            }
            .width(min: 110, ideal: 140)
            TableColumn("STATE") { row in Text(row.health.wireName) }
                .width(min: 60, ideal: 70)
            TableColumn("HANDSHAKE") { row in Text(row.handshake).monospacedDigit() }
                .width(min: 80, ideal: 90)
            TableColumn("RX / TX") { row in Text(row.traffic).monospacedDigit() }
                .width(min: 120, ideal: 150)
            TableColumn("PING") { row in ping(row.ping) }
                .width(min: 60, ideal: 70)
            TableColumn("ENDPOINT") { row in Text(row.endpoint).monospaced() }
                .width(min: 140, ideal: 200)
        }
        // primaryAction is the double-click. Somebody looking at a row in this
        // table and wanting its graph should not have to go back to the sidebar
        // and find the same name a second time.
        //
        // The menu is the same act on the right button, so the gesture is
        // discoverable rather than folklore. Both go through
        // DetailSelection.opening, which is where the "one row, and only one"
        // rule lives and is tested.
        .contextMenu(forSelectionType: TunnelRow.ID.self) { names in
            if let choice = DetailSelection.opening(names), case .tunnel(let name) = choice {
                Button("Open \(name)") { onOpen(choice) }
            }
        } primaryAction: { names in
            if let choice = DetailSelection.opening(names) {
                onOpen(choice)
            }
        }
    }

    /// A cross for a probe that got nothing, with the reason on hover. Blank
    /// means nobody asked, which is a different thing and reads as one.
    @ViewBuilder private func ping(_ cell: TunnelRow.PingCell) -> some View {
        switch cell {
        case .none:
            Text("")
        case .rtt(let value):
            Text(value).monospacedDigit()
        case .failed(let reason):
            Text("×").foregroundStyle(.red).help(reason)
        }
    }

    private func symbol(_ health: Health) -> String {
        switch health {
        case .up: "checkmark.circle.fill"
        case .stale: "exclamationmark.triangle.fill"
        case .down: "xmark.circle"
        case .unknown: "questionmark.circle"
        }
    }

    private func colour(_ health: Health) -> Color {
        switch health {
        case .up: .green
        case .stale: .orange
        case .down: .secondary
        case .unknown: .purple
        }
    }
}
