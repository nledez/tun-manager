import Darwin
import Foundation

/// Something that is not root is listening on that socket.
///
/// Named rather than an errno, because it is not one: connect(2) succeeded and
/// the kernel is perfectly happy. What failed is the only question worth asking
/// about a socket that is supposed to be tun-manager's — who is on the other
/// end of it.
public struct PublisherNotRoot: Error, Sendable, Equatable {
    /// Where the answer came from. Two lookups, because they fail differently:
    /// the credentials name whoever is *listening now*, and the file names
    /// whoever bound it. A socket root left behind and somebody else took over
    /// answers one way; a socket somebody else created answers the other.
    public enum Found: Sendable, Equatable {
        /// LOCAL_PEERCRED on the open connection.
        case peer
        /// The owner of the socket file itself.
        case socketFile
    }

    /// The uid found, or nil when it could not be read at all.
    public let uid: UInt32?
    public let found: Found

    public init(uid: UInt32?, found: Found) {
        self.uid = uid
        self.found = found
    }
}

/// Whether the thing on the other end has to be root, and what to do about it.
///
/// A value with the rule in it, so the rule can be tested without a socket.
///
/// **This and the signature of the hello exchange overlap on purpose, and
/// neither replaces the other.** The signature proves the publisher holds a key
/// this application pinned — it works over any socket, including one a demo
/// publisher bound in /tmp, and it is the only thing that can tell one
/// unprivileged process from another. This says something the signature never
/// can: that whoever is answering is root. A key can be copied out of a backup,
/// out of a Time Machine snapshot, out of a configuration file somebody
/// widened by accident, and replayed by a process running as an ordinary user.
/// Being uid 0 cannot be copied. The two fail in different weather, which is
/// why both are here: remove the credentials check and a stolen key is enough;
/// remove the signature and any root-owned process on the machine will do — and
/// the demo, which is deliberately not root, has nothing left to prove itself
/// with at all.
public struct PeerPolicy: Sendable, Equatable {
    /// False only for a demo publisher, which does not run as root — and that
    /// is exactly why it is safe: it can reach nothing this user could not
    /// reach anyway.
    public let requiresRoot: Bool

    /// Who this application is running as. The socket file is expected to
    /// belong to root or to them, and to nobody else.
    public let me: UInt32

    public init(requiresRoot: Bool, me: UInt32 = UInt32(getuid())) {
        self.requiresRoot = requiresRoot
        self.me = me
    }

    /// The rule for a socket the user named on the command line, versus the one
    /// this application connects to on its own.
    public static func of(_ choice: SocketPath.Choice) -> PeerPolicy {
        PeerPolicy(requiresRoot: !choice.isDemo)
    }

    /// Refuses a peer that is not root.
    ///
    /// A credential that could not be read is a refusal too, not a shrug: the
    /// answer to "who is that" being unavailable is not the same as it being
    /// acceptable, and every path that treats the two alike is a path somebody
    /// arranges to take.
    public func check(peer uid: UInt32?) throws {
        guard requiresRoot else { return }
        guard uid == 0 else { throw PublisherNotRoot(uid: uid, found: .peer) }
    }

    /// Refuses a socket file root does not own.
    ///
    /// Not the same question as the one above, and not redundant with it: this
    /// one is about the name, and a name is what somebody who can write the
    /// directory replaces. A publisher that binds a socket where root's used to
    /// be is caught here before a single byte is read.
    ///
    /// Root **or this user**, and that is not a weakening: it is what
    /// tun-manager does. The socket is bound by root and then handed to
    /// whoever ran `sudo tun-manager` — chowned to them, mode 0600 — which is
    /// the only reason an application running as that user can open it at all.
    /// Requiring root here refused every real installation while letting no
    /// attack through, because nobody else can own that file either way.
    ///
    /// A file that could not be looked at is *not* refused here, and that is
    /// the one place these two rules differ. There being no file is the normal
    /// state of this machine — there is no daemon — and connect(2) is about to
    /// say so with an errno that names it. Refusing here would turn "tun-manager
    /// is not running" into "something is not root", which is a worse sentence
    /// and a false one. Nothing is lost: if the connect then succeeds, whoever
    /// answers still has to be root, and that check does refuse silence.
    public func check(socketOwner uid: UInt32?) throws {
        guard requiresRoot, let uid else { return }
        guard uid == 0 || uid == me else { throw PublisherNotRoot(uid: uid, found: .socketFile) }
    }
}

/// Who is on the other end of an open unix socket, as the kernel sees them.
///
/// NOT TESTED: the getsockopt call. What it returns is decided by the kernel
/// from the peer's process, so a test could only assert that this machine's own
/// uid comes back — which is what the call does by definition, not what this
/// program does with it. The rule is in PeerPolicy, against uids a test chooses.
/// See macos/docs/coverage-gaps.md, "the peer's credentials".
public func peerUID(of descriptor: Int32) -> UInt32? {
    var credentials = xucred()
    var size = socklen_t(MemoryLayout<xucred>.size)
    guard getsockopt(descriptor, SOL_LOCAL, LOCAL_PEERCRED, &credentials, &size) == 0,
        credentials.cr_version == XUCRED_VERSION
    else {
        return nil
    }
    return UInt32(credentials.cr_uid)
}

/// Who owns a socket file. Nil when it cannot be looked at, which is a refusal
/// wherever root is required.
///
/// NOT TESTED: the same argument as above, one syscall further down.
/// See macos/docs/coverage-gaps.md, "the peer's credentials".
public func ownerOfFile(at path: String) -> UInt32? {
    var info = stat()
    // lstat, not stat: a symbolic link where the socket should be is somebody
    // else's answer to the question, and following it would let them choose
    // which file is examined.
    guard lstat(path, &info) == 0 else { return nil }
    return UInt32(info.st_uid)
}
