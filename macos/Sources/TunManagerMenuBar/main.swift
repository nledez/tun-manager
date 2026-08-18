import AppKit

// NOT TESTED: four files of AppKit setter calls. Every decision they could have
// made lives in TunManagerFeed instead — which glyph, which rows, in what
// order, what to omit — and is tested there. See macos/docs/coverage-gaps.md,
// "the menu bar target".

let application = NSApplication.shared

// Held by a top-level binding on purpose. NSApplication.delegate is a *weak*
// reference, so `application.delegate = AppDelegate()` compiles, runs,
// deallocates the delegate immediately, and leaves an application with no
// status item and no error to explain it.
let delegate = AppDelegate()
application.delegate = delegate

// Both this and LSUIElement in the Info.plist are needed, and they are not
// redundant: Launch Services reads the plist when the bundle is opened, while
// this is what makes `swift run` behave like an agent rather than bouncing a
// generic icon in the Dock.
application.setActivationPolicy(.accessory)
application.run()
