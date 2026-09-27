import AppKit
import Photos

/// Everything that touches the Photos library, through PhotoKit. Kept in one
/// actor so PhotoKit's objects never cross a thread; only identifiers, names
/// and bytes leave it.
actor PhotoLibrary {
    /// Asset names by local identifier. Reading an asset's resources is the slow
    /// part of indexing a large library, and a name never changes, so the second
    /// check onwards reads only what is new.
    private var names: [String: String] = [:]
    /// The same for Shared Albums, kept apart so a shared asset can never be
    /// taken for one in the library.
    private var sharedNames: [String: String] = [:]

    static let thumbnailPixels = 240
    /// The server keeps nothing larger, so a bigger JPEG would only be dropped.
    static let thumbnailMaxBytes = 120 * 1024

    // MARK: Access

    static func describe(_ status: PHAuthorizationStatus) -> String {
        switch status {
        case .authorized: "authorized"
        case .limited: "limited"
        case .denied: "denied"
        case .restricted: "restricted"
        case .notDetermined: "notDetermined"
        @unknown default: "unknown"
        }
    }

    nonisolated static var access: PHAuthorizationStatus {
        PHPhotoLibrary.authorizationStatus(for: .readWrite)
    }

    /// Asks for full access if macOS has never been asked, and returns the
    /// answer. Without it every fetch returns an empty set and reports no error
    /// at all, which would read as "none of these are in Photos".
    func authorise() async -> PHAuthorizationStatus {
        let status = Self.access
        guard status == .notDetermined else { return status }
        await MainActor.run { NSApp.activate() }
        return await PHPhotoLibrary.requestAuthorization(for: .readWrite)
    }

    // MARK: Reading

    private static func options() -> PHFetchOptions {
        let options = PHFetchOptions()
        // Hidden photographs are still photographs the archive may have removed.
        options.includeHiddenAssets = true
        options.includeAssetSourceTypes = [.typeUserLibrary]
        return options
    }

    /// The name Photos knows an asset by: the original file's name, as iCloud
    /// delivered it to the archive. The primary resource is chosen by type, so a
    /// Live Photo is named by its still and an edited photograph by its original.
    private static func name(of asset: PHAsset) -> String {
        let resources = PHAssetResource.assetResources(for: asset)
        let primary: PHAssetResourceType = asset.mediaType == .video ? .video : .photo
        guard let resource = resources.first(where: { $0.type == primary }) ?? resources.first else { return "" }
        if #available(macOS 27, *) {
            return resource.filename ?? ""
        }
        return resource.originalFilename
    }

    /// Every asset in the library, indexed for matching. Progress is reported
    /// as it goes, because a first read of a large library takes a while.
    func index(progress: @Sendable (Int, Int) async -> Void) async -> LibraryIndex {
        let (assets, seen) = await Self.read(Self.options(), known: names, progress: progress)
        names = seen
        return LibraryIndex(assets, zone: .current)
    }

    /// Every photograph in the Shared Albums this Mac subscribes to. These are
    /// only read, to say why a file was not found: nothing is ever matched,
    /// deleted or favourited from here.
    func sharedIndex(progress: @Sendable (Int, Int) async -> Void) async -> LibraryIndex {
        let options = PHFetchOptions()
        options.includeHiddenAssets = true
        options.includeAssetSourceTypes = [.typeCloudShared]
        let (assets, seen) = await Self.read(options, known: sharedNames, progress: progress)
        sharedNames = seen
        return LibraryIndex(assets, zone: .current)
    }

    private static func read(_ options: PHFetchOptions, known: [String: String], progress: @Sendable (Int, Int) async -> Void) async -> ([LibraryAsset], [String: String]) {
        let result = PHAsset.fetchAssets(with: options)
        let total = result.count
        var assets: [LibraryAsset] = []
        assets.reserveCapacity(total)
        var seen: [String: String] = [:]
        seen.reserveCapacity(total)
        for position in 0..<total {
            let asset = result.object(at: position)
            let id = asset.localIdentifier
            let name = known[id] ?? name(of: asset)
            seen[id] = name
            assets.append(LibraryAsset(id: id, name: name, created: asset.creationDate, favourite: asset.isFavorite))
            if position % 250 == 0 { await progress(position, total) }
        }
        await progress(total, total)
        return (assets, seen)
    }

    /// A small JPEG of one asset, or nil if Photos has none to hand. Only what
    /// is already on this Mac is used: waiting on iCloud for a preview would
    /// stall the check, and the page says plainly when a preview is missing.
    func thumbnail(for id: String) async -> Data? {
        guard let asset = PHAsset.fetchAssets(withLocalIdentifiers: [id], options: Self.options()).firstObject else {
            return nil
        }
        let options = PHImageRequestOptions()
        options.deliveryMode = .highQualityFormat
        options.resizeMode = .fast
        options.isNetworkAccessAllowed = false
        // Synchronous, so the handler runs exactly once before the call returns
        // and the continuation can neither leak nor resume twice.
        options.isSynchronous = true
        let size = CGSize(width: Self.thumbnailPixels, height: Self.thumbnailPixels)
        return await withCheckedContinuation { continuation in
            PHImageManager.default().requestImage(for: asset, targetSize: size, contentMode: .aspectFill, options: options) { image, _ in
                continuation.resume(returning: image.flatMap(Self.jpeg))
            }
        }
    }

    private static func jpeg(_ image: NSImage) -> Data? {
        guard let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else { return nil }
        let bitmap = NSBitmapImageRep(cgImage: cg)
        for quality in [0.72, 0.5, 0.3] {
            if let data = bitmap.representation(using: .jpeg, properties: [.compressionFactor: quality]),
               data.count <= thumbnailMaxBytes {
                return data
            }
        }
        return nil
    }

    /// Which of these identifiers Photos still holds. A deleted asset goes to
    /// Recently Deleted, and PhotoKit stops returning it.
    private func present(_ ids: [String]) -> [String: PHAsset] {
        var found: [String: PHAsset] = [:]
        PHAsset.fetchAssets(withLocalIdentifiers: ids, options: Self.options()).enumerateObjects { asset, _, _ in
            found[asset.localIdentifier] = asset
        }
        return found
    }

    // MARK: Changing

    /// Marks each entry's assets as favourites, then reads them back: an entry
    /// counts as done only when Photos really reports every one as a favourite.
    func favourite(_ entries: [TaskApply]) async -> (results: [AppliedItem], note: String) {
        let ids = Array(Set(entries.flatMap(\.photos)))
        var note = ""
        let before = present(ids)
        let pending = before.filter { !$0.value.isFavorite }.map(\.key)
        if !pending.isEmpty {
            do {
                try await PHPhotoLibrary.shared().performChanges {
                    PHAsset.fetchAssets(withLocalIdentifiers: pending, options: Self.options()).enumerateObjects { asset, _, _ in
                        PHAssetChangeRequest(for: asset).isFavorite = true
                    }
                }
            } catch {
                note = Self.explain(error, doing: "set favourites")
            }
        }
        let after = present(ids)
        let results = entries.map { entry -> AppliedItem in
            let assets = entry.photos.compactMap { after[$0] }
            if assets.isEmpty {
                return AppliedItem(id: entry.id, done: false, error: "No longer in Photos.")
            }
            let done = assets.allSatisfy(\.isFavorite)
            return AppliedItem(id: entry.id, done: done, error: done ? "" : "Photos did not keep the favourite.")
        }
        return (results, note)
    }

    /// Moves each entry's assets to Recently Deleted in one request, so macOS
    /// asks once, then checks what really went.
    ///
    /// The check is the point. When the confirmation on the Mac is declined,
    /// PhotoKit can report success anyway; an entry is only reported as deleted
    /// once Photos no longer returns any of its assets.
    func delete(_ entries: [TaskApply], confirming: @Sendable () async -> Void, verifying: @Sendable () async -> Void) async -> (results: [AppliedItem], note: String) {
        let ids = Array(Set(entries.flatMap(\.photos)))
        var note = ""
        let before = Array(present(ids).keys)
        if !before.isEmpty {
            await confirming()
            // An agent app has no window, so the confirmation would open behind
            // whatever is in front. Coming forward puts it where it is seen.
            await MainActor.run { NSApp.activate() }
            do {
                try await PHPhotoLibrary.shared().performChanges {
                    PHAssetChangeRequest.deleteAssets(PHAsset.fetchAssets(withLocalIdentifiers: before, options: Self.options()))
                }
            } catch {
                note = Self.explain(error, doing: "delete")
            }
        }
        await verifying()
        let after = present(ids)
        let results = entries.map { entry -> AppliedItem in
            let left = entry.photos.filter { after[$0] != nil }.count
            if left == 0 { return AppliedItem(id: entry.id, done: true, error: "") }
            return AppliedItem(id: entry.id, done: false, error: left == entry.photos.count ? "Still in Photos." : "\(left) of \(entry.photos.count) copies are still in Photos.")
        }
        return (results, note)
    }

    private static func explain(_ error: Error, doing what: String) -> String {
        let error = error as NSError
        if error.domain == PHPhotosErrorDomain, error.code == PHPhotosError.userCancelled.rawValue {
            return "The request to \(what) was declined on the Mac."
        }
        return "Photos would not \(what): \(error.localizedDescription)"
    }
}
