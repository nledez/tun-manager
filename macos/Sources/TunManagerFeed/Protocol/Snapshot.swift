import Foundation

// Sendable is declared rather than inferred on every type here. Public structs
// do not get it implicitly, and stating it means the day somebody adds a field
// of a non-Sendable type the error lands on the struct that caused it instead
// of at the far end of the program.

/// The network the machine is on, as tun-manager detected it.
public struct FeedContext: Sendable, Equatable {
    public let name: String
    /// Absent when no context rule matched, which is why these are optional
    /// rather than empty strings: the wire omits them, and "not given" is a
    /// different thing to draw than "given as nothing".
    public let interface: String?
    public let address: String?

    public init(name: String, interface: String? = nil, address: String? = nil) {
        self.name = name
        self.interface = interface
        self.address = address
    }
}

/// One tunnel, as of the instant the snapshot was taken.
public struct TunnelStatus: Sendable, Equatable {
    public let name: String
    public let group: String
    public let health: Health
    public let device: String?
    public let endpoint: String?
    public let checkIP: String?
    public let lastHandshake: Date?
    /// Int64 because the wire says int64. On arm64 it coincides with Int,
    /// which is exactly when a contract is worth writing down rather than
    /// relying on.
    public let rxBytes: Int64
    public let txBytes: Int64

    public init(
        name: String, group: String, health: Health,
        device: String? = nil, endpoint: String? = nil, checkIP: String? = nil,
        lastHandshake: Date? = nil, rxBytes: Int64 = 0, txBytes: Int64 = 0
    ) {
        self.name = name
        self.group = group
        self.health = health
        self.device = device
        self.endpoint = endpoint
        self.checkIP = checkIP
        self.lastHandshake = lastHandshake
        self.rxBytes = rxBytes
        self.txBytes = txBytes
    }
}

/// A complete picture at one instant — the payload of a `state` line.
public struct Snapshot: Sendable, Equatable {
    public let context: FeedContext
    public let taken: Date
    public let tunnels: [TunnelStatus]

    public init(context: FeedContext, taken: Date, tunnels: [TunnelStatus]) {
        self.context = context
        self.taken = taken
        self.tunnels = tunnels
    }
}

/// One reading of a watched tunnel's cumulative counters.
///
/// Version 1 never asks for these. The type exists so that a `sample` line
/// arriving anyway is understood and ignored rather than treated as garbage.
public struct Sample: Sendable, Equatable {
    public let tunnel: String
    public let at: Date
    public let rx: Int64
    public let tx: Int64

    public init(tunnel: String, at: Date, rx: Int64, tx: Int64) {
        self.tunnel = tunnel
        self.at = at
        self.rx = rx
        self.tx = tx
    }
}
