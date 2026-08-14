import Foundation
import ImageIO
import UniformTypeIdentifiers
import CoreGraphics

let dir = "cmd/mornlea/testdata/golden"
let names = ["terrain-noon", "oak-grove", "block-light-room", "materials-showcase", "avatar-nametag"]
let outURL = URL(fileURLWithPath: "docs/demo.gif")

guard let dest = CGImageDestinationCreateWithURL(
    outURL as CFURL, UTType.gif.identifier as CFString, names.count, nil
) else {
    fatalError("无法创建 GIF 目标")
}

let gifProps = [kCGImagePropertyGIFDictionary as String: [
    kCGImagePropertyGIFLoopCount as String: 0
]] as CFDictionary
CGImageDestinationSetProperties(dest, gifProps)

let frameProps = [kCGImagePropertyGIFDictionary as String: [
    kCGImagePropertyGIFDelayTime as String: 1.0
]] as CFDictionary

for name in names {
    let url = URL(fileURLWithPath: "\(dir)/\(name).png") as CFURL
    guard let src = CGImageSourceCreateWithURL(url, nil),
          let img = CGImageSourceCreateImageAtIndex(src, 0, nil) else {
        fatalError("读图失败 \(name)")
    }
    CGImageDestinationAddImage(dest, img, frameProps)
}

guard CGImageDestinationFinalize(dest) else {
    fatalError("写入失败")
}
print("done: \(outURL.path)")
