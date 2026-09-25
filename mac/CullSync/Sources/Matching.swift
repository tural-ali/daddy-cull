import Foundation

// How an archive file is found in the Photos library. This is the join the
// retired cull-sync.py made, carried over rule for rule, and it must stay in
// step with next/internal/catalog/photos.go: the server sends each entry's
// stem already normalised, and this side normalises the library's names the
// same way. If the two drift, the join quietly finds fewer photographs and it
// looks as though Photos simply does not have them.
enum Naming {
    // The archive's own duplicate factories add these: " (2)" from an ingest
    // collision, "-1234567" from a size disambiguation, " (2023-08-25)" from a
    // re-download. The character classes are spelled out in ASCII because Go's
    // \s and \d are ASCII-only while ICU's are not, and \z because ICU's $ also
    // matches before a trailing newline.
    private static let suffix = try! NSRegularExpression(
        pattern: #"([\t\n\f\r ]\([0-9]+\)|\((?:r[\t\n\f\r ]*)[0-9]+\)|[\t\n\f\r ]\([0-9]{4}-[0-9]{2}-[0-9]{2}\)|-[0-9]{3,})\z"#
    )

    /// A filename stem with any import suffix removed. An underscore before
    /// digits is left alone: that is how every camera names its files.
    static func normalise(_ stem: String) -> String {
        var s = stem
        // Repeated, because " (2023-08-25) (2)" happens. Three passes, as the
        // server does, and no more.
        for _ in 0..<3 {
            let next = suffix.stringByReplacingMatches(
                in: s, range: NSRange(s.startIndex..., in: s), withTemplate: "")
            if next == s { break }
            s = next
        }
        return s.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Stem and lower-case extension, split the way Python's pathlib did for the
    /// old script: a leading dot or a trailing one does not start an extension.
    static func split(_ name: String) -> (stem: String, ext: String) {
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex,
              name.index(after: dot) != name.endIndex else {
            return (name, "")
        }
        return (String(name[..<dot]), name[name.index(after: dot)...].lowercased())
    }
}

enum Day {
    private static func calendar(_ zone: TimeZone) -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        return calendar
    }

    /// The YYYY-MM-DD a moment falls on in the given time zone.
    static func string(_ date: Date, in zone: TimeZone) -> String {
        let parts = calendar(zone).dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", parts.year ?? 0, parts.month ?? 0, parts.day ?? 0)
    }

    /// Whether a moment falls on the given day or the day either side of it.
    static func withinADay(_ date: Date, of day: String, in zone: TimeZone) -> Bool {
        let parts = day.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3 else { return false }
        let calendar = calendar(zone)
        guard let target = calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2])) else {
            return false
        }
        let distance = calendar.dateComponents([.day], from: target, to: calendar.startOfDay(for: date)).day ?? .max
        return abs(distance) <= 1
    }
}

/// One asset in the Photos library, reduced to what matching needs.
struct LibraryAsset: Sendable, Equatable {
    let id: String
    let name: String
    let created: Date?
    let favourite: Bool
}

enum MatchResult: Equatable {
    /// Name, type and day all agree. Every such asset is included: Photos can
    /// hold one photograph twice, and removing one copy would leave the other.
    case exact([LibraryAsset])
    /// The one bearer of this name and type in the whole library, a day off.
    case near(LibraryAsset)
    case missing
}

/// The whole library keyed the way the archive names things.
///
/// The key is stem, extension and day. The extension is in there because iOS
/// numbers screenshots and photographs in one IMG_ sequence, so the archive's
/// IMG_6748.PNG and the library's IMG_6748.DNG share a stem while being two
/// unrelated pictures taken two years apart. Keyed on the stem alone, the first
/// version of the old script offered to delete the raw.
struct LibraryIndex: Sendable {
    /// The server refuses a match naming more assets than this, and so many
    /// bearers of one name on one day means the name identifies nothing.
    static let maxAssetsPerEntry = 20

    private var byKey: [String: [LibraryAsset]] = [:]
    private var byName: [String: [LibraryAsset]] = [:]
    let zone: TimeZone
    private(set) var count = 0

    init(_ assets: [LibraryAsset], zone: TimeZone) {
        self.zone = zone
        for asset in assets where !asset.name.isEmpty {
            let (stem, ext) = Naming.split(asset.name)
            let normalised = Naming.normalise(stem)
            count += 1
            byName[Self.key(normalised, ext), default: []].append(asset)
            // An undated asset can still make a name ambiguous, so it counts
            // towards uniqueness, but it can never match on a day.
            if let created = asset.created {
                byKey[Self.key(normalised, ext, Day.string(created, in: zone)), default: []].append(asset)
            }
        }
    }

    private static func key(_ parts: String...) -> String { parts.joined(separator: "\u{0}") }

    /// Resolves one entry. The stem arrives already normalised and the extension
    /// already lower-case, exactly as the server matched its own side.
    ///
    /// Name, type and day must all agree. The one concession is a day either
    /// side, and only when the name and type together have exactly one bearer in
    /// the whole library: Photos stores a capture time and the archive stores
    /// the day folder it was filed under, and those can land on different sides
    /// of midnight. An unambiguous name cannot be the wrong photograph; an
    /// ambiguous one is reported as not found rather than picked between.
    func match(stem: String, ext: String, day: String) -> MatchResult {
        if let hits = byKey[Self.key(stem, ext, day)], !hits.isEmpty {
            return hits.count <= Self.maxAssetsPerEntry ? .exact(hits) : .missing
        }
        let loose = byName[Self.key(stem, ext)] ?? []
        if loose.count == 1, let created = loose[0].created, Day.withinADay(created, of: day, in: zone) {
            return .near(loose[0])
        }
        return .missing
    }
}
