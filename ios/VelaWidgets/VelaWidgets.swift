import AppIntents
import NetworkExtension
import SwiftUI
import WidgetKit

// Home screen widget and Control Center control for Vela.
// Both start the tunnel with the last configuration the app used.

enum VPN {
    static func manager() async -> NETunnelProviderManager? {
        let all = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        return all.first { $0.localizedDescription == "Vela" } ?? all.first
    }

    static func status() async -> NEVPNStatus {
        await manager()?.connection.status ?? .invalid
    }

    static func isOn() async -> Bool {
        switch await status() {
        case .connected, .connecting, .reasserting: true
        default: false
        }
    }

    static func set(_ on: Bool) async throws {
        guard let m = await manager() else { throw VPNError.notSetUp }
        if on {
            if !m.isEnabled {
                m.isEnabled = true
                try await m.saveToPreferences()
            }
            try m.connection.startVPNTunnel()
        } else {
            m.connection.stopVPNTunnel()
        }
    }
}

enum VPNError: Error, CustomLocalizedStringResourceConvertible {
    case notSetUp
    var localizedStringResource: LocalizedStringResource { "Откройте Vela и подключитесь один раз из приложения" }
}

struct ToggleVPNIntent: SetValueIntent {
    static let title: LocalizedStringResource = "Vela VPN"
    static let description = IntentDescription("Подключает или отключает Vela")

    @Parameter(title: "Включён")
    var value: Bool

    init() {}
    init(value: Bool) { self.value = value }

    func perform() async throws -> some IntentResult {
        try await VPN.set(value)
        // the tunnel needs a moment to change state before widgets redraw
        try? await Task.sleep(nanoseconds: 700_000_000)
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

// MARK: - Widget

private enum Colors {
    static let ink = Color(red: 0x0C / 255, green: 0x14 / 255, blue: 0x24 / 255)
    static let deep = Color(red: 0x12 / 255, green: 0x1D / 255, blue: 0x33 / 255)
    static let hull = Color(red: 0x1B / 255, green: 0x29 / 255, blue: 0x44 / 255)
    static let star = Color(red: 0xF3 / 255, green: 0xE6 / 255, blue: 0xC4 / 255)
    static let text = Color(red: 0xE6 / 255, green: 0xEC / 255, blue: 0xF5 / 255)
    static let muted = Color(red: 0x87 / 255, green: 0x96 / 255, blue: 0xB0 / 255)
    static let sea = Color(red: 0x59 / 255, green: 0xD0 / 255, blue: 0xB0 / 255)
    static let onSea = Color(red: 0x06 / 255, green: 0x22 / 255, blue: 0x1B / 255)
    static let onStar = Color(red: 0x1B / 255, green: 0x16 / 255, blue: 0x08 / 255)
}

struct VPNEntry: TimelineEntry {
    let date: Date
    let status: NEVPNStatus

    var on: Bool { status == .connected }
    var busy: Bool { status == .connecting || status == .reasserting || status == .disconnecting }
    var text: String {
        switch status {
        case .connected: "Защищено"
        case .connecting, .reasserting: "Подключение…"
        case .disconnecting: "Отключение…"
        case .invalid: "Не настроено"
        default: "Не подключено"
        }
    }
}

struct VPNProvider: TimelineProvider {
    func placeholder(in _: Context) -> VPNEntry { VPNEntry(date: .now, status: .disconnected) }

    func getSnapshot(in _: Context, completion: @escaping (VPNEntry) -> Void) {
        Task { completion(VPNEntry(date: .now, status: await VPN.status())) }
    }

    func getTimeline(in _: Context, completion: @escaping (Timeline<VPNEntry>) -> Void) {
        Task {
            let entry = VPNEntry(date: .now, status: await VPN.status())
            completion(Timeline(entries: [entry], policy: .after(.now.addingTimeInterval(15 * 60))))
        }
    }
}

struct VPNWidgetView: View {
    let entry: VPNEntry
    @Environment(\.widgetFamily) private var family

    var body: some View {
        let button = Button(intent: ToggleVPNIntent(value: !(entry.on || entry.busy))) {
            Text(entry.on || entry.busy ? "Отключить" : "Подключить")
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(entry.on ? Colors.onSea : Colors.onStar)
                .frame(maxWidth: family == .systemSmall ? .infinity : nil)
                .padding(.horizontal, 16).padding(.vertical, 9)
                .background(entry.on ? Colors.sea : Colors.star, in: Capsule())
        }
        .buttonStyle(.plain)

        if family == .systemSmall {
            VStack(alignment: .leading, spacing: 6) {
                header
                Spacer(minLength: 0)
                button
            }
        } else {
            HStack(spacing: 12) {
                header
                Spacer()
                button
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: "sailboat.fill").foregroundStyle(Colors.star)
                Text("Vela").font(.system(size: 15, weight: .semibold)).foregroundStyle(Colors.star)
            }
            HStack(spacing: 6) {
                Circle().fill(entry.on ? Colors.sea : Colors.muted).frame(width: 8, height: 8)
                Text(entry.text).font(.system(size: 14, weight: .semibold)).foregroundStyle(Colors.text).lineLimit(1)
            }
        }
    }
}

struct VelaStatusWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "app.vela.client.status", provider: VPNProvider()) { entry in
            VPNWidgetView(entry: entry)
                .containerBackground(for: .widget) {
                    LinearGradient(colors: [Colors.hull, Colors.ink], startPoint: .top, endPoint: .bottom)
                }
        }
        .configurationDisplayName("Vela")
        .description("Состояние VPN и подключение одним нажатием")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

// MARK: - Control Center

struct VPNValueProvider: ControlValueProvider {
    var previewValue: Bool { false }
    func currentValue() async throws -> Bool { await VPN.isOn() }
}

struct VelaControl: ControlWidget {
    var body: some ControlWidgetConfiguration {
        StaticControlConfiguration(kind: "app.vela.client.control", provider: VPNValueProvider()) { on in
            ControlWidgetToggle("Vela", isOn: on, action: ToggleVPNIntent()) { isOn in
                Label(isOn ? "Подключено" : "Выключено", systemImage: isOn ? "sailboat.fill" : "sailboat")
            }
        }
        .displayName("Vela VPN")
        .description("Подключение и отключение VPN")
    }
}

@main
struct VelaWidgetBundle: WidgetBundle {
    var body: some Widget {
        VelaStatusWidget()
        VelaControl()
    }
}
