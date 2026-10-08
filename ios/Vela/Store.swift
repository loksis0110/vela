import Foundation
import Velacore

struct Node: Codable, Identifiable, Hashable {
    let id: String
    let name: String
    let proto: String
    let server: String
    let port: Int
    let security: String
    let network: String
    let link: String
    /// Full node object from the Go core, passed back verbatim when building the config.
    let raw: String

    var isUDP: Bool { proto == "Hysteria2" || proto == "TUIC" }

    init?(dict: [String: Any]) {
        guard let id = dict["id"] as? String,
              let data = try? JSONSerialization.data(withJSONObject: dict),
              let raw = String(data: data, encoding: .utf8) else { return nil }
        self.id = id
        name = dict["name"] as? String ?? ""
        proto = dict["protocol"] as? String ?? ""
        server = dict["server"] as? String ?? ""
        port = dict["port"] as? Int ?? 0
        security = dict["security"] as? String ?? ""
        network = dict["network"] as? String ?? ""
        link = dict["link"] as? String ?? ""
        self.raw = raw
    }
}

struct Profile: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var kind: String // "subscription" | "manual"
    var url: String = ""
    var nodes: [Node] = []
    var upload: Int64 = 0
    var download: Int64 = 0
    var total: Int64 = 0
    var expire: Int64 = 0
    var intervalHours: Int = 0
    var supportURL: String = ""
    var updatedAt: Int64 = 0
    /// Name from the subscription server; customName is set by the user.
    /// Optional so state saved by older versions still decodes.
    var title: String?
    var customName: String?
}

struct VelaSettings: Codable, Equatable {
    var routing = "smart"
    var blockAds = false
    var ipv6 = true
    var remoteDNS = "https://1.1.1.1/dns-query"
    var directDNS = "77.88.8.8"
    var directDomains: [String] = []
    var proxyDomains: [String] = []

    var json: [String: Any] {
        ["mode": "tun", "routing": routing, "blockAds": blockAds, "ipv6": ipv6, "stack": "system",
         "remoteDns": remoteDNS, "directDns": directDNS, "directDomains": directDomains,
         "proxyDomains": proxyDomains, "logLevel": "warn"]
    }
}

struct AppState: Codable {
    var profiles: [Profile] = []
    var activeProfile = ""
    var selectedNode = "auto"
    var settings = VelaSettings()

    var active: Profile? { profiles.first { $0.id == activeProfile } ?? profiles.first }
}

struct CoreError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

/// Bridges Go probe callbacks to Swift.
private final class ProbeCallback: NSObject, VelacoreProbeHandlerProtocol {
    let handler: (String, Int, String) -> Void
    init(_ handler: @escaping (String, Int, String) -> Void) { self.handler = handler }
    func onResult(_ nodeID: String?, delayMs: Int32, reason: String?, rawError _: String?) {
        handler(nodeID ?? "", Int(delayMs), reason ?? "")
    }
}

@MainActor
final class Store: ObservableObject {
    static let shared = Store()
    static let manualID = "manual"

    @Published private(set) var state = AppState()
    /// ms per node id; -1 failed, 0 measuring
    @Published var pings: [String: Int] = [:]
    /// why a node failed the last check
    @Published var reasons: [String: String] = [:]
    /// progress of the server check
    @Published var probe: (done: Int, total: Int)?

    let dataURL: URL
    private let fileURL: URL

    private init() {
        // App Group is optional: re-signed builds often lose it.
        let group = Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String ?? ""
        let shared = group.isEmpty ? nil : FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)
        dataURL = shared ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Vela", isDirectory: true)
        try? FileManager.default.createDirectory(at: dataURL, withIntermediateDirectories: true)
        fileURL = dataURL.appendingPathComponent("state.json")
        if let data = try? Data(contentsOf: fileURL), let st = try? JSONDecoder().decode(AppState.self, from: data) {
            state = st
        }
    }

    private func update(_ change: (inout AppState) -> Void) {
        var next = state
        change(&next)
        state = next
        if let data = try? JSONEncoder().encode(next) {
            // keys stay out of iCloud/iTunes backups
            try? data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            var url = fileURL
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try? url.setResourceValues(values)
        }
    }

    // MARK: - Import

    /// Imports any mix of subscription URLs, share links and base64 bodies.
    /// Several subscriptions can be pasted at once, one per line.
    func importText(_ text: String) async throws -> String {
        let input = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !input.isEmpty else { throw CoreError(message: "Вставьте ссылку на подписку или сервер") }
        let split = Self.json(VelacoreSplitImport(input))
        let urls = split["subscriptions"] as? [String] ?? []
        let linksText = split["links"] as? String ?? ""
        var parts: [String] = []
        var failures: [String] = []
        var lastProfile: String?

        let fetched: [(String, Result<[String: Any], Error>)] = await withTaskGroup(of: (Int, String, Result<[String: Any], Error>).self) { group in
            for (i, url) in urls.enumerated() {
                group.addTask {
                    do { return (i, url, .success(try await Self.fetchSubscription(url))) } catch { return (i, url, .failure(error)) }
                }
            }
            var out: [(Int, String, Result<[String: Any], Error>)] = []
            for await item in group { out.append(item) }
            return out.sorted { $0.0 < $1.0 }.map { ($0.1, $0.2) }
        }

        for (url, result) in fetched {
            switch result {
            case let .failure(error):
                failures.append((URL(string: url)?.host ?? url) + ": " + error.localizedDescription)
            case let .success(sub):
                let nodes = Self.nodes(sub["nodes"])
                var label = ""
                var isNew = false
                update { st in
                    let existing = st.profiles.firstIndex { $0.kind == "subscription" && $0.url == url }
                    isNew = existing == nil
                    var profile = Self.makeProfile(id: existing.map { st.profiles[$0].id } ?? Self.randomID(), url: url, sub: sub, nodes: nodes,
                                                   customName: existing.flatMap { st.profiles[$0].customName })
                    profile.name = Self.uniqueName(st.profiles, profile)
                    label = profile.name
                    lastProfile = profile.id
                    if let i = existing { st.profiles[i] = profile } else { st.profiles.append(profile) }
                }
                parts.append(isNew ? "«\(label)»: серверов \(nodes.count)" : "«\(label)» обновлена")
            }
        }

        if !linksText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let parsed = Self.json(VelacoreParseText(linksText))
            let nodes = Self.nodes(parsed["nodes"])
            let errors = parsed["errors"] as? [String] ?? []
            if nodes.isEmpty {
                failures.append(errors.first ?? "не нашли поддерживаемых ссылок")
            } else {
                var added = 0
                update { st in
                    var manual = st.profiles.first { $0.id == Self.manualID } ?? Profile(id: Self.manualID, name: "Мои серверы", kind: "manual")
                    let known = Set(manual.nodes.map(\.link))
                    let fresh = nodes.filter { !known.contains($0.link) }
                    added = fresh.count
                    manual.nodes += fresh
                    if let i = st.profiles.firstIndex(where: { $0.id == Self.manualID }) { st.profiles[i] = manual } else { st.profiles.insert(manual, at: 0) }
                }
                lastProfile = Self.manualID
                parts.append(added == 0 ? "эти серверы уже есть в списке" : "серверов добавлено: \(added)")
                if !errors.isEmpty { parts.append("пропущено ссылок: \(errors.count)") }
            }
        }

        guard let target = lastProfile else {
            throw CoreError(message: failures.isEmpty ? "Не нашли поддерживаемых ссылок" : failures.joined(separator: "; "))
        }
        update { st in
            // the first import becomes active; later ones are added alongside
            if st.activeProfile.isEmpty || !st.profiles.contains(where: { $0.id == st.activeProfile }) {
                st.activeProfile = target
                st.selectedNode = "auto"
            }
        }
        var msg = parts.joined(separator: ", ")
        if !failures.isEmpty { msg += ". Не удалось: " + failures.joined(separator: "; ") }
        return msg.prefix(1).uppercased() + msg.dropFirst()
    }

    func refresh(_ profileID: String) async throws {
        guard let p = state.profiles.first(where: { $0.id == profileID && $0.kind == "subscription" }) else {
            throw CoreError(message: "Подписка не найдена")
        }
        let sub = try await Self.fetchSubscription(p.url)
        let nodes = Self.nodes(sub["nodes"])
        update { st in
            if let i = st.profiles.firstIndex(where: { $0.id == p.id }) {
                var fresh = Self.makeProfile(id: p.id, url: p.url, sub: sub, nodes: nodes, customName: p.customName)
                fresh.name = Self.uniqueName(st.profiles, fresh)
                st.profiles[i] = fresh
            }
        }
    }

    func refreshAll() async throws -> String {
        let ids = state.profiles.filter { $0.kind == "subscription" }.map(\.id)
        guard !ids.isEmpty else { throw CoreError(message: "Подписок пока нет") }
        var failed = 0
        for id in ids {
            do { try await refresh(id) } catch { failed += 1 }
        }
        if failed == ids.count { throw CoreError(message: "Не удалось обновить подписки, проверьте интернет") }
        return failed > 0 ? "Обновлено \(ids.count - failed) из \(ids.count) подписок" : "Все подписки обновлены"
    }

    func refreshDue() async {
        let now = Int64(Date().timeIntervalSince1970)
        for p in state.profiles where p.kind == "subscription" {
            let interval = Int64(p.intervalHours > 0 ? p.intervalHours : 12)
            if now - p.updatedAt > interval * 3600 { try? await refresh(p.id) }
        }
    }

    private nonisolated static func fetchSubscription(_ url: String) async throws -> [String: Any] {
        try await Task.detached(priority: .userInitiated) {
            var error: NSError?
            let result = VelacoreFetchSubscription(url, &error)
            if let error { throw CoreError(message: error.localizedDescription) }
            return Self.json(result)
        }.value
    }

    // MARK: - Mutations

    func rename(_ profileID: String, to name: String) {
        update { st in
            guard let i = st.profiles.firstIndex(where: { $0.id == profileID }) else { return }
            let custom = name.trimmingCharacters(in: .whitespacesAndNewlines)
            st.profiles[i].customName = custom.isEmpty ? nil : custom
            st.profiles[i].name = custom.isEmpty ? (st.profiles[i].title ?? st.profiles[i].name) : custom
            st.profiles[i].name = Self.uniqueName(st.profiles, st.profiles[i])
        }
    }

    func deleteProfile(_ id: String) {
        update { st in
            st.profiles.removeAll { $0.id == id }
            if st.activeProfile == id {
                st.activeProfile = st.profiles.first?.id ?? ""
                st.selectedNode = "auto"
            }
        }
    }

    func deleteNode(profile: String, node: String) {
        update { st in
            if let i = st.profiles.firstIndex(where: { $0.id == profile }) { st.profiles[i].nodes.removeAll { $0.id == node } }
            if st.selectedNode == node { st.selectedNode = "auto" }
        }
    }

    func select(profile: String, node: String) {
        update { st in
            st.activeProfile = profile
            st.selectedNode = node
        }
    }

    func setSettings(_ s: VelaSettings) { update { $0.settings = s } }

    // MARK: - Server check

    /// Real request through every server using a temporary core in the app.
    /// Call while the VPN is off; reasons for failures are reported.
    func probeOffline(_ profile: Profile) async {
        guard probe == nil else { return }
        for n in profile.nodes { pings[n.id] = 0; reasons[n.id] = nil }
        probe = (0, profile.nodes.count)
        let nodesJSON = "[" + profile.nodes.map(\.raw).joined(separator: ",") + "]"
        let callback = ProbeCallback { id, ms, reason in
            Task { @MainActor in
                let s = Store.shared
                s.pings[id] = ms > 0 ? ms : -1
                if ms <= 0, !reason.isEmpty { s.reasons[id] = reason }
                if let p = s.probe { s.probe = (p.done + 1, p.total) }
            }
        }
        let error: Error? = await Task.detached(priority: .userInitiated) {
            do {
                try VelacoreProbeNodes(nodesJSON, 9000, 12, callback)
                return nil
            } catch { return error }
        }.value
        if error != nil {
            // temporary core unavailable: fall back to TCP reachability
            for n in profile.nodes where (pings[n.id] ?? 0) == 0 {
                let ms = await Task.detached { Int(VelacoreTCPPing(n.server, Int32(n.port))) }.value
                pings[n.id] = ms > 0 ? ms : -1
                if ms <= 0 { reasons[n.id] = "сервер недоступен" }
            }
        }
        try? await Task.sleep(nanoseconds: 200_000_000)
        probe = nil
    }

    // MARK: - Config

    /// Builds the sing-box config. Paths point to a placeholder that the
    /// Network Extension replaces with its own data directory, so the app and
    /// the extension don't need a shared container.
    func buildConfig(port: Int, secret: String) throws -> String {
        guard let profile = state.active, !profile.nodes.isEmpty else {
            throw CoreError(message: "Сначала добавьте сервер или подписку")
        }
        let nodes = profile.nodes.compactMap { Self.json($0.raw) as Any? }
        let req: [String: Any] = [
            "platform": "ios", "nodes": nodes,
            "selected": profile.id == state.activeProfile ? state.selectedNode : "auto",
            "settings": state.settings.json, "dataDir": "__VELA_DATA__", "skipRuleInstall": true,
            "controllerPort": port, "controllerSecret": secret,
        ]
        let data = try JSONSerialization.data(withJSONObject: req)
        var error: NSError?
        let config = VelacoreBuildConfig(String(data: data, encoding: .utf8), &error)
        if let error { throw CoreError(message: error.localizedDescription) }
        return config
    }

    // MARK: - Helpers

    nonisolated static func json(_ s: String) -> [String: Any] {
        guard let data = s.data(using: .utf8), let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return obj
    }

    nonisolated static func nodes(_ value: Any?) -> [Node] {
        (value as? [[String: Any]] ?? []).compactMap(Node.init(dict:))
    }

    nonisolated static func makeProfile(id: String, url: String, sub: [String: Any], nodes: [Node], customName: String?) -> Profile {
        func i64(_ k: String) -> Int64 { (sub[k] as? NSNumber)?.int64Value ?? 0 }
        let title = (sub["title"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        var p = Profile(id: id, name: customName ?? title ?? (URL(string: url)?.host ?? url), kind: "subscription", url: url)
        p.title = title
        p.customName = customName
        p.nodes = nodes
        p.upload = i64("upload"); p.download = i64("download"); p.total = i64("total"); p.expire = i64("expire")
        p.updatedAt = i64("updatedAt")
        p.intervalHours = (sub["updateIntervalHours"] as? NSNumber)?.intValue ?? 0
        let support = (sub["supportUrl"] as? String) ?? ""
        p.supportURL = support.isEmpty ? ((sub["webPageUrl"] as? String) ?? "") : support
        return p
    }

    /// Keeps profile names unique so tabs can be told apart.
    nonisolated static func uniqueName(_ all: [Profile], _ p: Profile) -> String {
        if let custom = p.customName, !custom.isEmpty { return custom }
        let base = p.name.isEmpty ? (URL(string: p.url)?.host ?? "Подписка") : p.name
        var name = base
        var n = 2
        while all.contains(where: { $0.id != p.id && $0.name == name }) {
            name = "\(base) \(n)"
            n += 1
        }
        return name
    }

    nonisolated static func randomID(_ bytes: Int = 8) -> String {
        (0 ..< bytes).map { _ in String(format: "%02x", UInt8.random(in: 0 ... 255)) }.joined()
    }
}
