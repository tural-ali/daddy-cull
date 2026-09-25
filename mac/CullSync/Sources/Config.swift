import Foundation

/// Where Daddy Cull is, and the key that lets this helper in. Read from
/// ~/.config/daddy-cull/sync.conf, the file the old Terminal tool used, so an
/// existing install keeps its settings.
struct SyncConfig: Equatable, Sendable {
    static let defaultURL = "http://cull.example.ts.net:8830"

    var url: String
    /// The key the setup command wrote, or the server's PHOTOS_AGENT_KEY.
    /// Never logged or shown.
    var token: String

    static var fileURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/daddy-cull/sync.conf")
    }

    /// Lines of `key = value`; blank lines and `#` comments are ignored, and a
    /// value may itself contain `=`. A missing url falls back to the default.
    static func parse(_ text: String) -> SyncConfig {
        var config = SyncConfig(url: defaultURL, token: "")
        for raw in text.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            guard !line.isEmpty, !line.hasPrefix("#"), let equals = line.firstIndex(of: "=") else { continue }
            let key = line[..<equals].trimmingCharacters(in: .whitespaces).lowercased()
            let value = line[line.index(after: equals)...].trimmingCharacters(in: .whitespaces)
            switch key {
            case "url" where !value.isEmpty:
                config.url = value
            case "token":
                config.token = value
            default:
                continue
            }
        }
        while config.url.hasSuffix("/") { config.url.removeLast() }
        return config
    }

    static func load(from file: URL = fileURL) -> SyncConfig {
        guard let text = try? String(contentsOf: file, encoding: .utf8) else {
            return SyncConfig(url: defaultURL, token: "")
        }
        return parse(text)
    }
}
