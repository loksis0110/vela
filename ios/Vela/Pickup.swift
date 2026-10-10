import Foundation
import UIKit
import Velacore

/// Subscriptions and servers offered by an app link or the clipboard
/// (core.ImportRequest).
struct ImportOffer: Identifiable, Equatable {
    let id = UUID()
    var subscriptions: [String]
    var links: String
    var linkCount: Int
    var name: String

    /// nil when there is nothing to add.
    init?(json: String) {
        let o = Store.json(json)
        subscriptions = (o["subscriptions"] as? [String] ?? []).filter { !$0.isEmpty }
        links = (o["links"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        linkCount = (o["linkCount"] as? NSNumber)?.intValue ?? 0
        name = (o["name"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if links.isEmpty { linkCount = 0 } else if linkCount == 0 { linkCount = links.split(separator: "\n").count }
        if subscriptions.isEmpty, links.isEmpty { return nil }
    }

    /// Text for the existing import path.
    var text: String { (subscriptions + (links.isEmpty ? [] : [links])).joined(separator: "\n") }

    var title: String {
        if subscriptions.count > 1 { return L("Добавить подписки?") }
        if subscriptions.count == 1 { return L("Добавить подписку?") }
        return L("Добавить серверы?")
    }

    /// Name, address and number of servers for the confirmation.
    var summary: String {
        var lines: [String] = []
        if !name.isEmpty { lines.append("«\(name)»") }
        for s in subscriptions.prefix(4) { lines.append(URL(string: s)?.host ?? s) }
        if subscriptions.count > 4 { lines.append(LF("и ещё %@", "\(subscriptions.count - 4)")) }
        if linkCount > 0 { lines.append(LF("Серверов: %@", "\(linkCount)")) }
        return lines.joined(separator: "\n")
    }
}

/// Offers links copied to the clipboard without the iOS paste prompt: the
/// clipboard is only inspected for a likely link (no prompt), and read when
/// the user taps the system Paste button on the banner.
@MainActor
final class Pickup: ObservableObject {
    static let shared = Pickup()

    /// The clipboard probably holds a link: show the banner.
    @Published var hint = false

    private let defaults = UserDefaults.standard
    private var lastOffer: ImportOffer?

    /// Call when the app becomes active.
    func check() {
        guard Store.shared.state.clipboardCheck else { hint = false; return }
        let pb = UIPasteboard.general
        let count = pb.changeCount
        guard count != defaults.integer(forKey: "clipChange") else { return }
        defaults.set(count, forKey: "clipChange")
        hint = false
        if pb.hasURLs { hint = true; return }
        guard pb.hasStrings else { return }
        pb.detectPatterns(for: [.probableWebURL]) { result in
            guard case let .success(found) = result, found.contains(.probableWebURL) else { return }
            Task { @MainActor in
                // still the same clipboard
                if UIPasteboard.general.changeCount == count { Pickup.shared.hint = true }
            }
        }
    }

    /// Copies text from Vela itself, so it isn't offered back.
    func copy(_ text: String) {
        UIPasteboard.general.string = text
        defaults.set(UIPasteboard.general.changeCount, forKey: "clipChange")
        hint = false
    }

    enum Outcome {
        case offer(ImportOffer)
        case message(String)
    }

    /// Text the user pasted on the banner: what can be added from it.
    func handle(_ text: String) -> Outcome {
        hint = false
        // remembered, so the same text isn't offered twice in a row
        let hash = Self.hash(text)
        if defaults.string(forKey: "clipHash") == hash, let last = lastOffer, Store.shared.newPart(of: last) == nil {
            return .message(last.subscriptions.isEmpty ? L("Эти серверы уже добавлены") : L("Эта подписка уже добавлена"))
        }
        defaults.set(hash, forKey: "clipHash")
        guard let offer = ImportOffer(json: VelacoreDetectImport(text)) else {
            return .message(L("В буфере обмена нет ссылки на подписку или сервер"))
        }
        lastOffer = offer
        guard let fresh = Store.shared.newPart(of: offer) else {
            return .message(offer.subscriptions.isEmpty ? L("Эти серверы уже добавлены") : L("Эта подписка уже добавлена"))
        }
        return .offer(fresh)
    }

    /// Stable across launches (unlike hashValue): FNV-1a.
    private static func hash(_ text: String) -> String {
        var h: UInt64 = 0xCBF2_9CE4_8422_2325
        for b in text.utf8 {
            h ^= UInt64(b)
            h = h &* 0x0000_0100_0000_01B3
        }
        return String(h, radix: 16)
    }
}
