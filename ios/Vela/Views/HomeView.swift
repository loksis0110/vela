import SwiftUI

/// Routing modes of the core (Settings.routing).
struct RoutingMode: Identifiable {
    let id: String
    let title: String
    let short: String
    let detail: String

    static var all: [RoutingMode] {
        [
            RoutingMode(id: "smart", title: L("Умный режим"), short: L("Умный"),
                        detail: L("Российские сайты напрямую, остальное через VPN")),
            RoutingMode(id: "blocked", title: L("Только заблокированное"), short: L("Заблокированное"),
                        detail: L("Через VPN только заблокированные сайты, остальное напрямую")),
            RoutingMode(id: "global", title: L("Весь трафик"), short: L("Всё через VPN"),
                        detail: L("Весь трафик идёт через VPN")),
        ]
    }

    static func find(_ id: String) -> RoutingMode { all.first { $0.id == id } ?? all[0] }
}

struct HomeView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @EnvironmentObject var pickup: Pickup
    @Binding var showServers: Bool
    @Binding var showSettings: Bool
    @Binding var showAdd: Bool
    // owned by RootView, which closes them before showing an app link offer
    @Binding var showStats: Bool
    @Binding var showConnections: Bool
    @Binding var showDiag: Bool
    /// Text pasted on the clipboard banner.
    let onPaste: (String) -> Void

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                header
                if pickup.hint { clipboardBanner.transition(.move(edge: .top).combined(with: .opacity)) }
                controlArea
                if let err = tunnel.error { errorBanner(err) }
                if let note = tunnel.notice { noticeBanner(note) }
                if let p = store.state.active, let text = store.announcement(for: p) { announceBanner(p, text) }
                Spacer(minLength: 24)
                VStack(spacing: 12) {
                    HStack(spacing: 10) {
                        let ip = ToolText.ip(tunnel.ip, busy: tunnel.ipBusy, error: tunnel.ipError)
                        ToolCard(label: L("Мой IP"), value: ip.0, meta: ip.1, busy: tunnel.ipBusy) { Task { await tunnel.refreshIP() } }
                        let sp = ToolText.speed(tunnel.speed, viaVPN: tunnel.state == .connected)
                        ToolCard(label: L("Скорость"), value: sp.0, meta: sp.1, busy: tunnel.speed.running) { Task { await tunnel.runSpeedTest() } }
                    }
                    HStack(spacing: 10) {
                        let today = tunnel.summary?.today.total
                        ToolCard(label: L("Статистика"), value: today.map { Fmt.bytes($0) } ?? L("Трафик"),
                                 meta: today == nil ? L("По дням и месяцам") : L("за сегодня"), busy: false) { showStats = true }
                        ConnectionsCard(live: tunnel.live, on: tunnel.state == .connected) { showConnections = true }
                    }
                    currentServer
                    routingSwitch
                }
                .padding(.bottom, 16)
            }
            .padding(.horizontal, 20)
            .frame(minHeight: UIScreen.main.bounds.height - 120)
            .animation(.easeOut(duration: 0.2), value: pickup.hint)
        }
        .sheet(isPresented: $showDiag) { DiagnosticsSheet() }
        .sheet(isPresented: $showStats) { StatsView() }
        .sheet(isPresented: $showConnections) { ConnectionsView() }
        .background(
            LinearGradient(stops: [.init(color: Palette.glow, location: 0), .init(color: Palette.ink, location: 0.45)],
                           startPoint: .top, endPoint: .bottom).ignoresSafeArea()
        )
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image("Logo").resizable().frame(width: 30, height: 30).clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            Text(verbatim: "Vela").font(.system(size: 20, weight: .semibold)).foregroundStyle(Palette.star)
            Spacer()
            Button { showSettings = true } label: {
                Image(systemName: "gearshape").font(.system(size: 20)).foregroundStyle(Palette.muted).frame(width: 44, height: 44)
            }
            .accessibilityLabel(L("Настройки"))
        }
        .padding(.top, 8)
    }

    /// A link is probably in the clipboard. The system Paste button reads it
    /// without the iOS "Allow Paste" prompt.
    private var clipboardBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "doc.on.clipboard").foregroundStyle(Palette.star)
            Text("В буфере обмена есть ссылка — добавить?")
                .font(.subheadline)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 4)
            PasteButton(payloadType: String.self) { items in
                guard let text = items.first else { return }
                Task { @MainActor in onPaste(text) }
            }
            .labelStyle(.titleOnly)
            .buttonBorderShape(.capsule)
            .controlSize(.small)
            .tint(Color(hex: 0x148A6D))
            Button { pickup.hint = false } label: {
                Image(systemName: "xmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Palette.muted).frame(width: 28, height: 28)
            }
            .accessibilityLabel("Скрыть")
        }
        .padding(.leading, 14).padding(.trailing, 8).padding(.vertical, 10)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).stroke(Palette.line))
        .padding(.top, 8)
    }

    private var label: String {
        switch tunnel.state {
        case .connected: L("Отключить")
        case .connecting: L("Отмена")
        case .disconnecting: L("Отключение")
        case .disconnected: L("Подключить")
        }
    }

    private var controlArea: some View {
        VStack(spacing: 0) {
            Button(action: toggle) {
                ZStack(alignment: .bottom) {
                    Constellation(state: tunnel.state)
                    let on = tunnel.state == .connected
                    Text(label)
                        .font(.system(size: 15, weight: .semibold))
                        .foregroundStyle(on ? Palette.onSea : Palette.text)
                        .padding(.horizontal, 22).padding(.vertical, 9)
                        .background(on ? Palette.sea : Palette.hull, in: Capsule())
                        .overlay(Capsule().stroke(on ? Palette.sea : Palette.line, lineWidth: 1))
                        .padding(.bottom, 14)
                }
                .frame(maxWidth: 320)
                .contentShape(Circle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(label)
            .padding(.top, 8)
            .sensoryFeedbackIfAvailable(tunnel.state)

            Text(statusText.0).font(.system(size: 22, weight: .semibold)).foregroundStyle(statusText.1).padding(.top, 10)
            Group {
                // the clock ticks only while connected
                if tunnel.state == .connected, let since = tunnel.since {
                    LiveSubtitle(live: tunnel.live, since: since)
                } else {
                    Text(store.state.profiles.isEmpty ? L("Добавьте сервер, чтобы начать") : "")
                        .font(.subheadline).foregroundStyle(Palette.muted)
                }
            }
            .lineLimit(1).minimumScaleFactor(0.8)
            .frame(height: 20).padding(.top, 4)
            if tunnel.state == .connected {
                LiveTraffic(live: tunnel.live)
            }
        }
    }

    private var statusText: (String, Color) {
        switch tunnel.state {
        case .connected:
            switch tunnel.link {
            case .ok: return (L("Защищено"), Palette.sea)
            // shown until a request through the server went through
            case .checking: return (L("Проверяем соединение…"), Palette.flare)
            case .offline: return (L("Нет сети"), Palette.flare)
            case .down: return (L("Сервер не отвечает"), Palette.danger)
            }
        case .connecting: return (L("Подключение…"), Palette.flare)
        case .disconnecting: return (L("Отключение…"), Palette.text)
        case .disconnected: return (L("Не подключено"), Palette.text)
        }
    }

    private func errorBanner(_ text: String) -> some View {
        HStack {
            Text(text).font(.subheadline)
            Spacer()
            VStack(spacing: 6) {
                Button("Скрыть") { tunnel.error = nil }.foregroundStyle(Palette.muted)
                Button("Отчёт") { showDiag = true }.foregroundStyle(Palette.star)
            }
        }
        .padding(14)
        .background(Palette.danger.opacity(0.14), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Palette.danger.opacity(0.4)))
        .padding(.top, 16)
    }

    private func noticeBanner(_ text: String) -> some View {
        HStack {
            Text(text).font(.subheadline)
            Spacer()
            Button("Понятно") { tunnel.notice = nil }.foregroundStyle(Palette.muted)
        }
        .padding(14)
        .background(Palette.sea.opacity(0.12), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Palette.sea.opacity(0.4)))
        .padding(.top, 16)
    }

    /// Message from the provider; closed until the text changes.
    private func announceBanner(_ p: Profile, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "megaphone").foregroundStyle(Palette.flare).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                Text(LF("Сообщение от «%@»", p.displayName)).font(.caption).foregroundStyle(Palette.muted)
                Text(text).font(.subheadline).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
            }
            Spacer(minLength: 4)
            Button { withAnimation(.easeOut(duration: 0.2)) { store.dismissAnnouncement(p) } } label: {
                Image(systemName: "xmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Palette.muted).frame(width: 28, height: 28)
            }
            .accessibilityLabel("Скрыть")
        }
        .padding(.leading, 14).padding(.trailing, 8).padding(.vertical, 12)
        .background(Palette.flare.opacity(0.10), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Palette.flare.opacity(0.35)))
        .padding(.top, 16)
    }

    private var currentServer: some View {
        let info = currentInfo
        return Button {
            if store.state.profiles.isEmpty { showAdd = true } else { showServers = true }
        } label: {
            Card {
                HStack(spacing: 14) {
                    Badge(text: info.badge)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(info.title).font(.system(size: 16, weight: .semibold)).lineLimit(1)
                        HStack(spacing: 6) {
                            if info.xray { MiniTag(text: "Xray") }
                            Text(info.meta).font(.caption).foregroundStyle(Palette.muted).lineLimit(1)
                        }
                    }
                    Spacer()
                    Image(systemName: "chevron.right").foregroundStyle(Palette.muted)
                }
                .padding(14)
            }
        }
        .buttonStyle(.plain)
    }

    private var currentInfo: (badge: String, title: String, meta: String, xray: Bool) {
        let st = store.state
        guard let p = st.active, !p.nodes.isEmpty else {
            return ("+", L("Добавить сервер"), L("Ссылка на подписку или vless://, hy2://…"), false)
        }
        if st.selectedNode == "auto" || !p.nodes.contains(where: { $0.id == st.selectedNode }) {
            if let live = p.nodes.first(where: { $0.id == tunnel.activeNode }) {
                let ms = store.pings[live.id] ?? 0
                let ping = ms > 0 ? ", \(ms) " + L("мс") : ""
                return (Fmt.badge(live.name), L("Автовыбор"), LF("Сейчас: %@", Fmt.splitFlag(live.name).rest) + ping, live.isXray)
            }
            return ("✦", L("Автовыбор"), LF("%@, серверов: %@", p.displayName, "\(p.nodes.count)"), false)
        }
        guard let n = p.nodes.first(where: { $0.id == st.selectedNode }) else {
            return ("✦", L("Автовыбор"), p.displayName, false)
        }
        var parts = [n.proto, n.security, p.displayName].filter { !$0.isEmpty }
        if let ms = store.pings[n.id], ms > 0 { parts.append("\(ms) " + L("мс")) }
        return (Fmt.badge(n.name), Fmt.splitFlag(n.name).rest, parts.joined(separator: ", "), n.isXray)
    }

    private var routingSwitch: some View {
        let current = store.state.settings.routing
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 4) {
                ForEach(RoutingMode.all) { m in routingOption(m, selected: current == m.id) }
            }
            .padding(4)
            .background(Palette.deep, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).stroke(Palette.hull))
            Text(RoutingMode.find(current).detail)
                .font(.system(size: 12))
                .foregroundStyle(Palette.muted)
                .padding(.horizontal, 8)
        }
    }

    private func routingOption(_ m: RoutingMode, selected: Bool) -> some View {
        Button {
            guard !selected else { return }
            var s = store.state.settings
            s.routing = m.id
            store.setSettings(s)
            Task { await tunnel.reload() }
        } label: {
            Text(m.short)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(selected ? Palette.star : Palette.text)
                .lineLimit(1)
                .minimumScaleFactor(0.6)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 6).padding(.vertical, 11)
                .background(selected ? Palette.hull : .clear, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(m.title)
        .accessibilityHint(m.detail)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private func toggle() {
        switch tunnel.state {
        case .disconnected:
            if store.state.profiles.isEmpty { showAdd = true } else { Task { await tunnel.connect() } }
        case .connected, .connecting:
            tunnel.disconnect()
        case .disconnecting:
            break
        }
    }
}

/// Time connected and bytes passed; the only part of the home screen that
/// changes every second.
private struct LiveSubtitle: View {
    @ObservedObject var live: LiveStats
    let since: Date

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { ctx in
            Text(LF("%@, передано %@", Fmt.duration(Int(ctx.date.timeIntervalSince(since))), Fmt.bytes(live.total)))
                .font(.subheadline).foregroundStyle(Palette.muted).monospacedDigit()
        }
    }
}

/// Current speed and the two-minute graph.
private struct LiveTraffic: View {
    @ObservedObject var live: LiveStats

    var body: some View {
        HStack(spacing: 28) {
            speed("↓", live.down)
            speed("↑", live.up)
        }
        .padding(.top, 10)
        TrafficGraph(points: live.history).padding(.top, 12)
    }

    private func speed(_ arrow: String, _ value: Int64) -> some View {
        HStack(spacing: 6) {
            Text(arrow).foregroundStyle(Palette.muted)
            Text(Fmt.speed(value)).fontWeight(.semibold).monospacedDigit()
        }
    }
}

private struct ConnectionsCard: View {
    @ObservedObject var live: LiveStats
    let on: Bool
    let action: () -> Void

    var body: some View {
        ToolCard(label: L("Соединения"), value: on ? "\(live.connectionCount)" : "—",
                 meta: on ? L("открыто сейчас") : L("когда VPN включён"), busy: false, action: action)
            .disabled(!on)
            .opacity(on ? 1 : 0.6)
    }
}

private extension View {
    @ViewBuilder func sensoryFeedbackIfAvailable(_ state: ConnState) -> some View {
        if #available(iOS 17.0, *) {
            sensoryFeedback(.success, trigger: state == .connected)
        } else {
            self
        }
    }
}
