import Charts
import SwiftUI
import TunManagerFeed

// NOT TESTED: drawing. See macos/docs/coverage-gaps.md, "the menu bar target".

/// The tunnel list on the left, what it is doing on the right.
struct DetailView: View {
    @ObservedObject var model: DetailModel
    let onSelect: (String) -> Void

    var body: some View {
        NavigationSplitView {
            List(selection: selection) {
                ForEach(sections, id: \.header) { section in
                    Section(section.header) {
                        ForEach(section.tunnels, id: \.name) { tunnel in
                            Label {
                                Text(tunnel.name)
                            } icon: {
                                Image(systemName: symbol(tunnel.health))
                            }
                            .tag(tunnel.name)
                        }
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200, max: 280)
        } detail: {
            if let detail = model.detail {
                TunnelPane(detail: detail, model: model)
            } else {
                ContentUnavailableView(
                    "No tunnel selected", systemImage: "shield",
                    description: Text("Pick one on the left."))
            }
        }
        .frame(minWidth: 640, minHeight: 380)
    }

    private var selection: Binding<String?> {
        Binding(get: { model.selected }, set: { if let name = $0 { onSelect(name) } })
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
