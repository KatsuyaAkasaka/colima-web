// 絵文字を 512x512 の画像にレンダリングし、指定ファイルのカスタムアイコンに設定する。
// 使い方: ./seticon "🐳" "/path/to/file"
import AppKit

let args = CommandLine.arguments
guard args.count >= 3 else {
    FileHandle.standardError.write("usage: seticon <emoji> <path>\n".data(using: .utf8)!)
    exit(1)
}
let emoji = args[1] as NSString
let path = args[2]

let size = NSSize(width: 512, height: 512)
let image = NSImage(size: size)
image.lockFocus()
let style = NSMutableParagraphStyle()
style.alignment = .center
let attrs: [NSAttributedString.Key: Any] = [
    .font: NSFont.systemFont(ofSize: 360),
    .paragraphStyle: style,
]
let textSize = emoji.size(withAttributes: attrs)
let rect = NSRect(x: 0, y: (size.height - textSize.height) / 2, width: size.width, height: textSize.height)
emoji.draw(in: rect, withAttributes: attrs)
image.unlockFocus()

let ok = NSWorkspace.shared.setIcon(image, forFile: path, options: [])
exit(ok ? 0 : 2)
