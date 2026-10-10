import Foundation
import UserNotifications

/// Local notifications about subscriptions: 3 days and 1 day before the end,
/// at the end, and when 90% of the traffic is used. Expiry reminders are
/// scheduled ahead (identifiers carry the end date, so each fires once per
/// period); the traffic alert is sent once until the traffic is reset.
@MainActor
final class Reminders {
    static let shared = Reminders()

    private let center = UNUserNotificationCenter.current()
    private static let expiryPrefix = "vela.expire."
    private static let trafficPrefix = "vela.traffic."

    /// Asks iOS for permission; false when the user declined (now or before).
    func requestPermission() async -> Bool {
        (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
    }

    /// true when notifications are allowed (asks iOS the first time); nil
    /// when the user turned them off for Vela in the iPhone settings.
    func ensurePermission() async -> Bool? {
        switch await center.notificationSettings().authorizationStatus {
        case .notDetermined: return await requestPermission()
        case .denied: return nil
        default: return true
        }
    }

    /// Reschedules everything for the current subscriptions. ask: request
    /// permission when a subscription with an end date or a traffic limit
    /// was just added.
    func sync(ask: Bool = false) {
        // one after another, so two refreshes don't schedule over each other
        let previous = running
        running = Task {
            await previous?.value
            await syncNow(ask: ask)
        }
    }

    private var running: Task<Void, Never>?

    private func syncNow(ask: Bool) async {
        let st = Store.shared.state
        let subs = st.profiles.filter { $0.kind == "subscription" }
        var status = await center.notificationSettings().authorizationStatus
        if st.reminders, ask, status == .notDetermined, subs.contains(where: { $0.expire > 0 || $0.total > 0 }) {
            _ = await requestPermission()
            status = await center.notificationSettings().authorizationStatus
        }
        let allowed = st.reminders && (status == .authorized || status == .provisional || status == .ephemeral)

        var wanted: [UNNotificationRequest] = []
        if allowed {
            for p in subs { wanted += expiryRequests(p) }
        }
        let keep = Set(wanted.map(\.identifier))
        let pending = await center.pendingNotificationRequests()
        let stale = pending.map(\.identifier).filter { $0.hasPrefix(Self.expiryPrefix) && !keep.contains($0) }
        if !stale.isEmpty { center.removePendingNotificationRequests(withIdentifiers: stale) }
        for r in wanted { try? await center.add(r) }
        await checkTraffic(subs, allowed: allowed)
    }

    private func expiryRequests(_ p: Profile) -> [UNNotificationRequest] {
        guard p.expire > 0 else { return [] }
        let end = Date(timeIntervalSince1970: TimeInterval(p.expire))
        let name = p.displayName
        let date = Fmt.date(p.expire)
        var out: [UNNotificationRequest] = []
        func add(_ kind: String, _ at: Date, _ title: String, _ body: String) {
            guard at > Date().addingTimeInterval(60) else { return }
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default
            content.threadIdentifier = "subscription"
            let parts = Calendar.current.dateComponents([.year, .month, .day, .hour, .minute], from: at)
            let trigger = UNCalendarNotificationTrigger(dateMatching: parts, repeats: false)
            out.append(UNNotificationRequest(identifier: "\(Self.expiryPrefix)\(p.id).\(p.expire).\(kind)", content: content, trigger: trigger))
        }
        add("3d", daytime(end.addingTimeInterval(-3 * 86400)), L("Подписка скоро закончится"),
            LF("«%@» действует до %@, осталось 3 дня. Продлите её у провайдера, чтобы VPN продолжил работать.", name, date))
        add("1d", daytime(end.addingTimeInterval(-86400)), L("Подписка закончится завтра"),
            LF("«%@» действует до %@. Продлите её у провайдера, чтобы VPN продолжил работать.", name, date))
        add("end", end, L("Подписка закончилась"),
            LF("Срок действия «%@» истёк. Продлите её у провайдера и обновите подписку в Vela.", name))
        return out
    }

    /// Keeps reminders out of the night: 22:00–9:00 moves to the evening or
    /// the morning of the same day.
    private func daytime(_ d: Date) -> Date {
        let cal = Calendar.current
        let h = cal.component(.hour, from: d)
        if h < 9 { return cal.date(bySettingHour: 10, minute: 0, second: 0, of: d) ?? d }
        if h >= 22 { return cal.date(bySettingHour: 20, minute: 0, second: 0, of: d) ?? d }
        return d
    }

    private func checkTraffic(_ subs: [Profile], allowed: Bool) async {
        let key = "trafficAlerted"
        var alerted = Set(UserDefaults.standard.stringArray(forKey: key) ?? [])
        for p in subs {
            let used = p.upload + p.download
            guard p.total > 0, Double(used) >= 0.9 * Double(p.total) else {
                alerted.remove(p.id) // traffic was reset: alert again next time
                continue
            }
            guard allowed, !alerted.contains(p.id) else { continue }
            let content = UNMutableNotificationContent()
            content.title = L("Трафик почти закончился")
            content.body = LF("«%@»: израсходовано %@ из %@. Пополните трафик у провайдера.", p.displayName, Fmt.bytes(used), Fmt.bytes(p.total))
            content.sound = .default
            content.threadIdentifier = "subscription"
            do {
                try await center.add(UNNotificationRequest(identifier: Self.trafficPrefix + p.id, content: content, trigger: nil))
                alerted.insert(p.id)
            } catch {}
        }
        let ids = Set(subs.map(\.id))
        UserDefaults.standard.set(Array(alerted.filter { ids.contains($0) }), forKey: key)
    }
}

/// Shows Vela's notifications while the app is open too.
final class NotificationDelegate: NSObject, UNUserNotificationCenterDelegate {
    static let shared = NotificationDelegate()

    func userNotificationCenter(_: UNUserNotificationCenter, willPresent _: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }
}
