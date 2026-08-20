import Foundation

/// Wire lines as the publisher writes them. Invented throughout: the tunnel
/// names are the repository's placeholders and the addresses come from the
/// ranges reserved for documentation (RFC 5737), so no fixture can name a real
/// host.
enum Fixtures {
    /// A hello from a publisher speaking what this build understands, with the
    /// key it is known by. The pubkey is the public half of the seed the Go
    /// tests use, so both sides are talking about the same key.
    static let hello =
        #"{"type":"hello","schema":2,"version":"v0.2.0","pubkey":"A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}"#

    /// A full view: one tunnel up with everything filled in, one down with
    /// every optional key omitted.
    static let state = """
        {"type":"state","context":{"name":"office","interface":"en0","address":"198.51.100.42"},\
        "taken":"2026-08-17T14:03:11.123456789+02:00","tunnels":[\
        {"name":"alpha","group":"needed","health":"up","device":"utun7",\
        "endpoint":"192.0.2.10:51820","check_ip":"10.20.30.1",\
        "last_handshake":"2026-08-17T14:02:58+02:00","rx_bytes":184320,"tx_bytes":92160},\
        {"name":"bravo","group":"","health":"down","endpoint":"bravo.example:51820",\
        "rx_bytes":0,"tx_bytes":0}]}
        """

    static let emptyState = """
        {"type":"state","context":{"name":""},\
        "taken":"2026-08-17T14:03:11.123456789+02:00","tunnels":[]}
        """

    /// A round of probes: one that answered, one that did not.
    static let ping = """
        {"type":"ping","results":[{"tunnel":"alpha","rtt_ms":18.4},\
        {"tunnel":"bravo","error":"timeout"}]}
        """

    static let bye = #"{"type":"bye"}"#

    static func line(_ text: String) -> Data { Data(text.utf8) }
}
