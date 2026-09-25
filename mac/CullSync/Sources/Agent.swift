import Foundation

/// What the menu shows. Everything the helper knows about itself that a person
/// might want to see, and nothing secret.
struct AgentSnapshot: Sendable, Equatable {
    enum Connection: Sendable, Equatable {
        case starting
        case connected
        case unreachable
        case needsKey
        case refused
        case disabled
    }

    var connection: Connection = .starting
    var serverURL = SyncConfig.defaultURL
    var access = PhotoLibrary.describe(PhotoLibrary.access)
    /// What the helper is doing right now, if anything.
    var activity: String?
    var lastSync: Date?
    var lastSyncSummary: String?
}

private struct Stopped: Error {}

/// A step failed in a way the page should hear about, in words a person reads.
private struct Problem: Error {
    let message: String
}

/// The helper's whole job: wait for work from Daddy Cull, do it against the
/// Photos library, and report back. It never changes Photos without an apply
/// that the reviewer pressed in the browser.
actor Agent {
    static let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"

    private static let lastSyncKey = "lastSync"
    private static let lastSyncSummaryKey = "lastSyncSummary"

    private let library = PhotoLibrary()
    private let publish: @MainActor @Sendable (AgentSnapshot) -> Void
    private var snapshot = AgentSnapshot()
    private var config = SyncConfig(url: SyncConfig.defaultURL, token: "")
    private var client: ServerClient?

    // The job in hand, as the heartbeat reports it.
    private var job: String?
    private var stage = ""
    private var done = 0
    private var total = 0
    private var cancelled = false
    private var lastBeat = Date.distantPast

    init(publish: @escaping @MainActor @Sendable (AgentSnapshot) -> Void) {
        self.publish = publish
        let defaults = UserDefaults.standard
        snapshot.lastSync = defaults.object(forKey: Self.lastSyncKey) as? Date
        snapshot.lastSyncSummary = defaults.string(forKey: Self.lastSyncSummaryKey)
    }

    func start() {
        Task { await pollLoop() }
        Task { await heartbeatLoop() }
        Task { await askForAccess() }
    }

    private func update(_ change: (inout AgentSnapshot) -> Void) async {
        var next = snapshot
        change(&next)
        next.access = PhotoLibrary.describe(PhotoLibrary.access)
        guard next != snapshot else { return }
        snapshot = next
        await publish(next)
    }

    /// Asks for Photos access as soon as the helper first runs, so the prompt
    /// appears while someone is at the Mac setting it up rather than halfway
    /// through the first check.
    private func askForAccess() async {
        _ = await library.authorise()
        await update { _ in }
        await beat()
    }

    // MARK: Connection

    /// Picks up edits to sync.conf without a restart.
    private func refreshClient() -> ServerClient? {
        let fresh = SyncConfig.load()
        if fresh != config || client == nil {
            config = fresh
            client = fresh.token.isEmpty ? nil : ServerClient(config: fresh)
        }
        return client
    }

    private func pollLoop() async {
        var wait: Double = 1
        while !Task.isCancelled {
            guard let client = refreshClient() else {
                await update { $0.connection = .needsKey; $0.serverURL = self.config.url }
                try? await Task.sleep(for: .seconds(10))
                continue
            }
            do {
                let task = try await client.work()
                wait = 1
                await update { $0.connection = .connected; $0.serverURL = client.base }
                guard let task else { continue }
                switch task.task {
                case "check": await check(task, client)
                case "apply": await apply(task, client)
                default: try? await client.failed(task.jobId, "This version of Cull Sync does not know how to \(task.task). Update it on the Mac.")
                }
            } catch let error as ClientError {
                let pause: Double
                switch error {
                case .refused:
                    await update { $0.connection = .refused }
                    pause = 30
                case .disabled:
                    await update { $0.connection = .disabled }
                    pause = 30
                default:
                    await update { $0.connection = .unreachable }
                    // Back off while the server is away, so a sleeping Tower or a
                    // dropped VPN costs nothing, but come back quickly after a blip.
                    pause = wait
                    wait = min(wait * 2, 60)
                }
                try? await Task.sleep(for: .seconds(pause))
            } catch {
                await update { $0.connection = .unreachable }
                try? await Task.sleep(for: .seconds(wait))
                wait = min(wait * 2, 60)
            }
        }
    }

    private func heartbeatLoop() async {
        while !Task.isCancelled {
            await beat()
            try? await Task.sleep(for: .seconds(15))
        }
    }

    /// Tells the server the helper is alive, what it can see, and how far the
    /// job has got; the reply says whether the reviewer cancelled it.
    private func beat() async {
        guard let client = refreshClient() else { return }
        lastBeat = Date()
        let current = job
        let beat = Heartbeat(
            version: Self.version, access: PhotoLibrary.describe(PhotoLibrary.access),
            job: current, stage: current == nil ? nil : stage,
            done: current == nil ? nil : done, total: current == nil ? nil : total)
        if let cancel = try? await client.heartbeat(beat), cancel, current != nil, current == job {
            cancelled = true
        }
    }

    /// Records progress, and passes it on at once when the stage changes or at
    /// most once a second otherwise, which is as often as the page looks.
    private func progress(_ stage: String, _ done: Int = 0, _ total: Int = 0, activity: String) async {
        let changed = stage != self.stage
        self.stage = stage
        self.done = done
        self.total = total
        await update { $0.activity = activity }
        if changed || Date().timeIntervalSince(lastBeat) >= 1 {
            await beat()
        }
    }

    private func begin(_ task: AgentTask) {
        job = task.jobId
        stage = ""
        done = 0
        total = 0
        cancelled = false
    }

    private func finish() async {
        job = nil
        stage = ""
        await update { $0.activity = nil }
        await beat()
    }

    private func requireAccess() async throws {
        switch await library.authorise() {
        case .authorized:
            return
        case .limited:
            throw Problem(message: "Cull Sync can see only some of the Photos library. Give it full access in System Settings, Privacy & Security, Photos, then check again.")
        case .notDetermined:
            throw Problem(message: "Photos access was not granted on the Mac. Answer the prompt, then check again.")
        default:
            throw Problem(message: "Cull Sync is not allowed to use Photos. Allow it in System Settings, Privacy & Security, Photos, then check again.")
        }
    }

    /// Sends a report, trying again after a dropped connection. The server
    /// accepts the same report twice, so a retry is always safe.
    private func deliver(attempts: Int, _ send: () async throws -> Void) async throws {
        var pause: Double = 2
        for attempt in 1...attempts {
            do {
                try await send()
                return
            } catch ClientError.unreachable where attempt < attempts {
                try? await Task.sleep(for: .seconds(pause))
                pause = min(pause * 2, 60)
            }
        }
    }

    // MARK: Check

    /// Finds each entry in Photos and sends back what it found, with a preview of
    /// every match so the reviewer can see it is the same photograph.
    private func check(_ task: AgentTask, _ client: ServerClient) async {
        begin(task)
        let entries = task.entries ?? []
        do {
            try await requireAccess()
            await progress("reading", activity: "Reading the Photos library…")
            let index = await library.index { done, total in
                await self.progress("reading", done, total, activity: "Reading the Photos library… \(done.formatted()) of \(total.formatted())")
            }
            if cancelled { throw Stopped() }

            var found: [(entry: TaskEntry, how: String, assets: [LibraryAsset])] = []
            var missing: [String] = []
            for (position, entry) in entries.enumerated() {
                switch index.match(stem: entry.stem, ext: entry.ext, day: entry.day) {
                case .exact(let assets): found.append((entry, "exact", assets))
                case .near(let asset): found.append((entry, "near", [asset]))
                case .missing: missing.append(entry.id)
                }
                if position % 200 == 0 {
                    await progress("matching", position, entries.count, activity: "Finding photographs in Photos…")
                }
            }
            await progress("matching", entries.count, entries.count, activity: "Finding photographs in Photos…")

            for chunk in stride(from: 0, to: missing.count, by: 1000).map({ Array(missing[$0..<min($0 + 1000, missing.count)]) }) {
                try await deliver(attempts: 3) { try await client.matches(task.jobId, MatchReport(missing: chunk)) }
            }

            // Matches go in small batches, so progress moves steadily and no one
            // request grows large however many copies Photos holds.
            let formatter = ISO8601DateFormatter()
            var report = MatchReport()
            var bytes = 0
            var sent = 0
            for item in found {
                if cancelled { throw Stopped() }
                var photos: [ReportedAsset] = []
                for asset in item.assets {
                    let thumb = await library.thumbnail(for: asset.id)
                    bytes += thumb?.count ?? 0
                    photos.append(ReportedAsset(
                        id: asset.id, name: asset.name,
                        created: asset.created.map { formatter.string(from: $0) } ?? "",
                        favourite: asset.favourite, thumb: thumb))
                }
                report.matches.append(ReportedMatch(id: item.entry.id, how: item.how, photos: photos))
                if report.matches.count >= 20 || bytes >= 3 << 20 {
                    let batch = report
                    try await deliver(attempts: 3) { try await client.matches(task.jobId, batch) }
                    sent += batch.matches.count
                    report = MatchReport()
                    bytes = 0
                    await progress("thumbnails", sent, found.count, activity: "Fetching previews from Photos… \(sent.formatted()) of \(found.count.formatted())")
                }
            }
            if !report.matches.isEmpty {
                let batch = report
                try await deliver(attempts: 3) { try await client.matches(task.jobId, batch) }
            }
            if cancelled { throw Stopped() }
            try await deliver(attempts: 3) { try await client.checked(task.jobId) }
        } catch is Stopped {
            // The reviewer cancelled; the server already knows.
        } catch ClientError.stale {
            // The job moved on without this helper.
        } catch let problem as Problem {
            try? await client.failed(task.jobId, problem.message)
        } catch {
            try? await client.failed(task.jobId, "The check stopped on the Mac: \(error.localizedDescription)")
        }
        await finish()
    }

    // MARK: Apply

    /// Makes the changes the reviewer chose: favourites first, because they
    /// cannot lose anything, then one deletion request that macOS asks about.
    private func apply(_ task: AgentTask, _ client: ServerClient) async {
        begin(task)
        let favourites = task.favourites ?? []
        let deletes = task.deletes ?? []
        do {
            try await requireAccess()
            await progress("starting", activity: "Changing Photos…")
            var notes: [String] = []
            var favourited: [AppliedItem] = []
            var deleted: [AppliedItem] = []
            if !favourites.isEmpty {
                await progress("favourites", 0, favourites.count, activity: "Setting favourites in Photos…")
                let outcome = await library.favourite(favourites)
                favourited = outcome.results
                if !outcome.note.isEmpty { notes.append(outcome.note) }
            }
            if !deletes.isEmpty {
                let outcome = await library.delete(
                    deletes,
                    confirming: { await self.progress("confirm", 0, deletes.count, activity: "Confirm the deletion in the Photos prompt…") },
                    verifying: { await self.progress("verifying", activity: "Checking what Photos deleted…") })
                deleted = outcome.results
                if !outcome.note.isEmpty { notes.append(outcome.note) }
            }
            let report = AppliedReport(favourites: favourited, deletes: deleted, note: notes.joined(separator: " "))
            // What was really changed must reach the server, or the next check
            // offers it again, so this keeps trying for several minutes.
            try await deliver(attempts: 10) { try await client.applied(task.jobId, report) }
            let gone = deleted.filter(\.done).count
            let hearts = favourited.filter(\.done).count
            let summary = "\(gone.formatted()) deleted, \(hearts.formatted()) favourite\(hearts == 1 ? "" : "s") set"
            let now = Date()
            UserDefaults.standard.set(now, forKey: Self.lastSyncKey)
            UserDefaults.standard.set(summary, forKey: Self.lastSyncSummaryKey)
            await update { $0.lastSync = now; $0.lastSyncSummary = summary }
        } catch ClientError.stale {
            // The job moved on without this helper.
        } catch let problem as Problem {
            try? await client.failed(task.jobId, problem.message)
        } catch {
            try? await client.failed(task.jobId, "Photos was changed on the Mac, but the result could not be sent: \(error.localizedDescription). Check again to see where things stand.")
        }
        await finish()
    }
}
