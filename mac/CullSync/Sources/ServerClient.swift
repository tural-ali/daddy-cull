import Foundation

// The conversation with Daddy Cull, mirroring next/internal/catalog/photos_hub.go.
// The server decodes strictly and refuses unknown fields, so these types carry
// exactly what it expects and nothing more.

struct AgentTask: Decodable, Sendable {
    let jobId: String
    let task: String
    let entries: [TaskEntry]?
    let favourites: [TaskApply]?
    let deletes: [TaskApply]?
}

struct TaskEntry: Decodable, Sendable {
    let id: String
    let action: String
    let name: String
    let stem: String
    let ext: String
    let day: String
}

struct TaskApply: Decodable, Sendable {
    let id: String
    let photos: [String]
}

struct Heartbeat: Encodable, Sendable {
    var version: String
    var access: String
    var job: String?
    var stage: String?
    var message: String?
    var done: Int?
    var total: Int?
}

struct MatchReport: Encodable, Sendable {
    var matches: [ReportedMatch] = []
    var missing: [String] = []
    /// Why some of missing were not found, as MissReason raw values.
    var reasons: [String: String] = [:]
}

struct ReportedMatch: Encodable, Sendable {
    let id: String
    let how: String
    let photos: [ReportedAsset]
}

struct ReportedAsset: Encodable, Sendable {
    let id: String
    let name: String
    let created: String
    let favourite: Bool
    /// A small JPEG, sent base64-encoded as Go expects a []byte to be.
    let thumb: Data?
}

struct AppliedItem: Encodable, Sendable {
    let id: String
    let done: Bool
    let error: String
}

struct AppliedReport: Encodable, Sendable {
    let favourites: [AppliedItem]
    let deletes: [AppliedItem]
    let note: String
}

enum ClientError: Error, Equatable {
    /// The server could not be reached at all.
    case unreachable(String)
    /// The key in sync.conf is not the one the server accepts.
    case refused
    /// An older server with no PHOTOS_AGENT_KEY, so the helper is switched off
    /// there. Current servers answer refused instead and offer a setup command.
    case disabled
    /// The job moved on: cancelled, replaced or timed out.
    case stale
    case failed(Int, String)
}

/// Talks to the server's /api/photos/agent routes. The key travels in a header
/// on every request and is never written anywhere else.
final class ServerClient: Sendable {
    static let keyHeader = "X-Photos-Agent-Key"

    let base: String
    private let key: String
    private let session: URLSession

    init(config: SyncConfig) {
        base = config.url
        key = config.token
        let configuration = URLSessionConfiguration.ephemeral
        // Longer than the server's 25 second long poll, so a quiet poll ends
        // with the server's empty answer rather than a client timeout.
        configuration.timeoutIntervalForRequest = 40
        configuration.waitsForConnectivity = false
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        session = URLSession(configuration: configuration)
    }

    /// Waits for work. Nil means the poll ended with nothing to do.
    func work() async throws -> AgentTask? {
        let (data, status) = try await send("GET", "work", body: Optional<MatchReport>.none)
        if status == 204 { return nil }
        return try JSONDecoder().decode(AgentTask.self, from: data)
    }

    /// Reports that the helper is alive, and learns whether to abandon its job.
    func heartbeat(_ beat: Heartbeat) async throws -> Bool {
        struct Reply: Decodable { let cancel: Bool }
        let (data, _) = try await send("POST", "heartbeat", body: beat)
        return try JSONDecoder().decode(Reply.self, from: data).cancel
    }

    func matches(_ job: String, _ report: MatchReport) async throws {
        _ = try await send("POST", "jobs/\(job)/matches", body: report)
    }

    func checked(_ job: String) async throws {
        _ = try await send("POST", "jobs/\(job)/checked", body: [String: String]())
    }

    func failed(_ job: String, _ reason: String) async throws {
        _ = try await send("POST", "jobs/\(job)/failed", body: ["error": reason])
    }

    func applied(_ job: String, _ report: AppliedReport) async throws {
        _ = try await send("POST", "jobs/\(job)/applied", body: report)
    }

    private func send<Body: Encodable>(_ method: String, _ path: String, body: Body?) async throws -> (Data, Int) {
        guard let url = URL(string: "\(base)/api/photos/agent/\(path)") else {
            throw ClientError.unreachable("The url in sync.conf is not a valid address.")
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue(key, forHTTPHeaderField: Self.keyHeader)
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(body)
        }
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw ClientError.unreachable(error.localizedDescription)
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        switch status {
        case 200..<300:
            return (data, status)
        case 403:
            throw ClientError.refused
        case 409:
            throw ClientError.stale
        case 503:
            // A 503 naming the key means it is not configured on the server; any
            // other is the server, or a proxy in front of it, being down.
            if let message = Self.message(data), message.contains("PHOTOS_AGENT_KEY") { throw ClientError.disabled }
            throw ClientError.unreachable(Self.message(data) ?? "The server is unavailable.")
        default:
            throw ClientError.failed(status, Self.message(data) ?? "The server answered \(status).")
        }
    }

    private static func message(_ data: Data) -> String? {
        struct Failure: Decodable { let error: String }
        return (try? JSONDecoder().decode(Failure.self, from: data))?.error
    }
}
