// Rotates an image's hue, to tell a development build apart from the one that
// is installed.
//
// A hue rotation rather than a badge or an overlay: the artwork stays
// recognisable — it is plainly the same application — while no longer being
// mistakable for the real one at a glance in the Dock or in Command-Tab.
//
// CoreImage rather than a drawing tool, so a fresh clone needs nothing beyond
// what builds the application anyway.

import CoreImage
import Foundation

let arguments = CommandLine.arguments
guard arguments.count == 4, let radians = Float(arguments[3]) else {
    FileHandle.standardError.write(Data("usage: tint.swift <in.png> <out.png> <radians>\n".utf8))
    exit(2)
}

let (input, output) = (URL(fileURLWithPath: arguments[1]), URL(fileURLWithPath: arguments[2]))

guard let source = CIImage(contentsOf: input) else {
    FileHandle.standardError.write(Data("cannot read \(input.path)\n".utf8))
    exit(1)
}

guard
    let filter = CIFilter(name: "CIHueAdjust", parameters: [kCIInputImageKey: source, kCIInputAngleKey: radians]),
    let tinted = filter.outputImage
else {
    FileHandle.standardError.write(Data("hue adjustment failed\n".utf8))
    exit(1)
}

let context = CIContext()
guard
    let colourSpace = CGColorSpace(name: CGColorSpace.sRGB),
    let data = context.pngRepresentation(of: tinted, format: .RGBA8, colorSpace: colourSpace)
else {
    FileHandle.standardError.write(Data("cannot encode \(output.path)\n".utf8))
    exit(1)
}

try data.write(to: output)
