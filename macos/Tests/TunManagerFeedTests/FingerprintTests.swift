import Foundation
import Testing

@testable import TunManagerFeed

// The public half of the key tun-manager derives from the seed
// AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=, and the fingerprint the Go side
// prints for it. Both were taken from `internal/feed`, so this test is what
// keeps the two implementations saying the same thing about the same key —
// which is the whole point of a fingerprint somebody compares by eye.
private let publicKey = Data([
    0x03, 0xa1, 0x07, 0xbf, 0xf3, 0xce, 0x10, 0xbe, 0x1d, 0x70, 0xdd, 0x18, 0xe7, 0x4b, 0xc0, 0x99,
    0x67, 0xe4, 0xd6, 0x30, 0x9b, 0xa5, 0x0d, 0x5f, 0x1d, 0xdc, 0x86, 0x64, 0x12, 0x55, 0x31, 0xb8,
])
private let asPrinted = "56:47:5a:a7:54:63:47:4c:02:85:df:5d:bf:2b:ca:b7"

@Test func theFingerprintIsTheOneTunManagerPrints() {
    #expect(Fingerprint.of(publicKey) == asPrinted)
}

@Test func theFingerprintOfWhatArrivedOnTheWire() {
    // The hello carries the key base64-encoded, which is the form this has to
    // start from.
    #expect(Fingerprint.of(base64: publicKey.base64EncodedString()) == asPrinted)
}

@Test func somethingThatIsNotBase64HasNoFingerprint() {
    #expect(Fingerprint.of(base64: "not base64 at all!") == nil)
}

@Test func aKeyOfTheWrongLengthHasNoFingerprint() {
    // A truncated key is not a key. Showing a fingerprint of one would invite
    // somebody to compare it against tun-manager's and conclude the publisher
    // had changed.
    #expect(Fingerprint.of(base64: Data([1, 2, 3]).base64EncodedString()) == nil)
}

@Test func theFingerprintIsShortEnoughToReadOutLoud() {
    // Sixteen bytes in pairs: what ssh settled on for the same job. Long enough
    // that two keys will not collide in any population this program will see,
    // short enough to compare against a window on the other side of the desk.
    #expect(asPrinted.count == 47)
    #expect(Fingerprint.of(publicKey).split(separator: ":").count == 16)
}
