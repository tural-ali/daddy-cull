import Foundation

// Pure-logic tests for the matching rules. Run with ./test.sh; nothing here
// touches the Photos library or the network.

var failures = 0
@MainActor func expect(_ condition: Bool, _ message: @autoclosure () -> String, line: Int = #line) {
    if !condition {
        failures += 1
        print("FAIL line \(line): \(message())")
    }
}

// The same table is in next/internal/catalog/photos_test.go; both sides must
// agree or the join silently finds nothing.
let normalised: [String: String] = [
    "IMG_2866": "IMG_2866",
    "IMG_2866 (2)": "IMG_2866",
    "IMG_2866 (2023-08-25)": "IMG_2866",
    "IMG_2866 (2023-08-25) (2)": "IMG_2866",
    "IMG_2866-1234567": "IMG_2866",
    "IMG_2866-12": "IMG_2866-12",
    "IMG_2866(r2)": "IMG_2866",
    "IMG_2866(r 2)": "IMG_2866",
    "IMG_2866_HEVC": "IMG_2866_HEVC",
    "IMG_2866(2)": "IMG_2866(2)",
    "Copy of IMG_1 (3) (4) (5) (6)": "Copy of IMG_1 (3)",
    " padded ": "padded",
    "2022-06-22_IMG_4938": "2022-06-22_IMG_4938",
    "Screenshot 2022-06-22 (12)": "Screenshot 2022-06-22",
]
for (stem, want) in normalised {
    let got = Naming.normalise(stem)
    expect(got == want, "normalise(\(stem.debugDescription)) = \(got.debugDescription), want \(want.debugDescription)")
}

let splits: [String: (String, String)] = [
    "IMG_6748.PNG": ("IMG_6748", "png"),
    "IMG_6748.DNG": ("IMG_6748", "dng"),
    "a.tar.gz": ("a.tar", "gz"),
    ".hidden": (".hidden", ""),
    "trailing.": ("trailing.", ""),
    "plain": ("plain", ""),
]
for (name, want) in splits {
    let got = Naming.split(name)
    expect(got.stem == want.0 && got.ext == want.1, "split(\(name)) = \(got), want \(want)")
}

let utc = TimeZone(identifier: "UTC")!
func at(_ text: String) -> Date { ISO8601DateFormatter().date(from: text)! }
func asset(_ id: String, _ name: String, _ created: String?) -> LibraryAsset {
    LibraryAsset(id: id, name: name, created: created.map(at), favourite: false)
}

expect(Day.string(at("2022-06-22T23:59:59Z"), in: utc) == "2022-06-22", "day at midnight")
expect(Day.withinADay(at("2022-06-23T01:00:00Z"), of: "2022-06-22", in: utc), "the next day is within a day")
expect(Day.withinADay(at("2022-06-21T23:00:00Z"), of: "2022-06-22", in: utc), "the day before is within a day")
expect(!Day.withinADay(at("2022-06-24T00:00:00Z"), of: "2022-06-22", in: utc), "two days off is not within a day")
expect(!Day.withinADay(at("2022-06-22T00:00:00Z"), of: "undated", in: utc), "a malformed day never matches")

// The screenshot and the raw iOS numbered alike, two years apart. Removing the
// screenshot must never reach the raw, on any day.
do {
    let index = LibraryIndex([asset("RAW", "IMG_6748.DNG", "2024-03-01T10:00:00Z")], zone: utc)
    expect(index.match(stem: "IMG_6748", ext: "png", day: "2022-06-22") == .missing, "PNG must not find the DNG")
    expect(index.match(stem: "IMG_6748", ext: "png", day: "2024-03-01") == .missing, "PNG must not find the DNG on its own day")
    expect(index.match(stem: "IMG_6748", ext: "dng", day: "2024-03-01") == .exact([asset("RAW", "IMG_6748.DNG", "2024-03-01T10:00:00Z")]), "the DNG finds itself")
    let both = LibraryIndex([
        asset("RAW", "IMG_6748.DNG", "2024-03-01T10:00:00Z"),
        asset("SHOT", "IMG_6748.PNG", "2022-06-22T10:00:00Z"),
    ], zone: utc)
    expect(both.match(stem: "IMG_6748", ext: "png", day: "2022-06-22") == .exact([asset("SHOT", "IMG_6748.PNG", "2022-06-22T10:00:00Z")]), "PNG finds only the PNG")
}

// Photos' own names are normalised as the archive's are, and every copy on the
// day is included.
do {
    let index = LibraryIndex([
        asset("A", "IMG_2866.MOV", "2023-08-25T09:00:00Z"),
        asset("B", "IMG_2866 (1).MOV", "2023-08-25T09:00:01Z"),
        asset("C", "IMG_2866.MOV", "2019-01-01T09:00:00Z"),
    ], zone: utc)
    if case .exact(let hits) = index.match(stem: "IMG_2866", ext: "mov", day: "2023-08-25") {
        expect(hits.map(\.id) == ["A", "B"], "both copies on the day, got \(hits.map(\.id))")
    } else {
        expect(false, "IMG_2866.MOV should match exactly")
    }
    // Three bearers of the name: a day off is ambiguous, so not found.
    expect(index.match(stem: "IMG_2866", ext: "mov", day: "2023-08-26") == .missing, "an ambiguous name is never matched a day off")
}

// The one-day tolerance, only for a name that is unique in the whole library.
do {
    let unique = asset("U", "IMG_1001.HEIC", "2023-08-25T23:30:00Z")
    let index = LibraryIndex([unique, asset("N", "IMG_0001.HEIC", nil)], zone: utc)
    expect(index.match(stem: "IMG_1001", ext: "heic", day: "2023-08-26") == .near(unique), "unique name a day off is near")
    expect(index.match(stem: "IMG_1001", ext: "heic", day: "2023-08-24") == .near(unique), "unique name a day before is near")
    expect(index.match(stem: "IMG_1001", ext: "heic", day: "2023-08-27") == .missing, "two days off is missing")
    expect(index.match(stem: "IMG_1001", ext: "jpg", day: "2023-08-25") == .missing, "another type is missing")
    expect(index.match(stem: "IMG_0001", ext: "heic", day: "2023-08-25") == .missing, "an undated asset never matches")

    // An undated bearer still makes the name ambiguous.
    let crowded = LibraryIndex([unique, asset("X", "IMG_1001.HEIC", nil)], zone: utc)
    expect(crowded.match(stem: "IMG_1001", ext: "heic", day: "2023-08-26") == .missing, "undated twin makes the name ambiguous")
    expect(crowded.match(stem: "IMG_1001", ext: "heic", day: "2023-08-25") == .exact([unique]), "the exact day still matches")
}

// The day is read in the Mac's time zone, as Photos shows it.
do {
    let london = TimeZone(identifier: "Europe/London")!
    let index = LibraryIndex([asset("L", "IMG_5.JPG", "2023-08-25T23:30:00Z")], zone: london)
    expect(index.match(stem: "IMG_5", ext: "jpg", day: "2023-08-26") == .exact([asset("L", "IMG_5.JPG", "2023-08-25T23:30:00Z")]), "BST moves 23:30 UTC to the next day")
}

// More bearers than the server accepts means the name identifies nothing.
do {
    let many = (0...LibraryIndex.maxAssetsPerEntry).map { asset("M\($0)", "IMG_9.JPG", "2020-01-01T10:00:00Z") }
    expect(LibraryIndex(many, zone: utc).match(stem: "IMG_9", ext: "jpg", day: "2020-01-01") == .missing, "too many bearers is missing")
    let enough = Array(many.dropLast())
    if case .exact(let hits) = LibraryIndex(enough, zone: utc).match(stem: "IMG_9", ext: "jpg", day: "2020-01-01") {
        expect(hits.count == LibraryIndex.maxAssetsPerEntry, "exactly the limit is kept")
    } else {
        expect(false, "exactly the limit should match")
    }
}

// sync.conf
do {
    let config = SyncConfig.parse("""
    # Where the tool is
    url = http://tower.example:8823/

      token =  abc=def==
    other = ignored
    """)
    expect(config.url == "http://tower.example:8823", "url trimmed of its slash, got \(config.url)")
    expect(config.token == "abc=def==", "token keeps its own equals signs")
    let empty = SyncConfig.parse("url =\ntoken =\n")
    expect(empty.url == SyncConfig.defaultURL && empty.token.isEmpty, "empty values fall back")
    expect(SyncConfig.load(from: URL(fileURLWithPath: "/nonexistent/sync.conf")) == SyncConfig(url: SyncConfig.defaultURL, token: ""), "missing file falls back")
}

if failures > 0 {
    print("\(failures) failure(s)")
    exit(1)
}
print("all matching tests passed")
