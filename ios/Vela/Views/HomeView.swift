import SwiftUI

struct HomeView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @Binding var showServers: Bool
    @Binding var showSettings: Bool
    @Binding var showAdd: Bool
    @State private var showDiag = false

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                header
                controlArea
                if let err = tunnel.error { errorBanner(err) }
                if let note = tunnel.notice { noticeBanner(note) }
                Spacer(minLength: 24)
                VStack(spacing: 12) {
                    HStack(spacing: 10) {
                        let ip = ToolText.ip(tunnel.ip, busy: tunnel.ipBusy, error: tunnel.ipError)
                        ToolCard(label: "Мой IP", value: ip.0, meta: ip.1, busy: tunnel.ipBusy) { Task { await tunnel.refreshIP() } }
                        let sp = ToolText.speed(tunnel.speed)
                        ToolCard(label: "Скорость", value: sp.0, meta: sp.1, busy: tunnel.speed.running) { Task { await tunnel.runSpeedTest() } }
                    }
                    currentServer
                    routingSwitch
                }
                .padding(.bottom, 16)
            }
            .padding(.horizontal, 20)
            .frame(minHeight: UIScreen.main.bounds.height - 120)
        }
        .sheet(isPresented: $showDiag) { DiagnosticsSheet() }
        .background(
            LinearGradient(stops: [.init(color: Palette.glow, location: 0), .init(color: Palette.ink, location: 0.45)],
                           startPoint: .top, endPoint: .bottom).ignoresSafeArea()
        )
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image("Logo").resizable().frame(width: 30, height: 30).clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            Text("Vela").font(.system(size: 20, weight: .semibold)).foregroundStyle(Palette.star)
            Spacer()
            Button { showSettings = true } label: {
                Image(systemName: "gearshape").font(.system(size: 20)).foregroundStyle(Palette.muted).frame(width: 44, height: 44)
            }
            .accessibilityLabel("Настройки")
        }
        .padding(.top, 8)
    }

    private var label: String {
        switch tunnel.state {
        case .connected: "Отключить"
        case .connecting: "Отмена"
        case .disconnecting: "Отключение"
        case .disconnected: "Подключить"
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
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                Text(subtitle(ctx.date)).font(.subheadline).foregroundStyle(Palette.muted).monospacedDigit()
            }
            .frame(height: 20).padding(.top, 4)
            if tunnel.state == .connected {
                HStack(spacing: 28) {
                    speed("↓", tunnel.down)
                    speed("↑", tunnel.up)
                }
                .padding(.top, 10)
                TrafficGraph(points: tunnel.history).padding(.top, 12)
            }
        }
    }

    private var statusText: (String, Color) {
        switch tunnel.state {
        case .connected: ("Защищено", Palette.sea)
        case .connecting: ("Подключение…", Palette.flare)
        case .disconnecting: ("Отключение…", Palette.text)
        case .disconnected: ("Не подключено", Palette.text)
        }
    }

    private func subtitle(_ now: Date) -> String {
        if tunnel.state == .connected, let since = tunnel.since {
            return Fmt.duration(Int(now.timeIntervalSince(since))) + ", передано " + Fmt.bytes(tunnel.total)
        }
        return store.state.profiles.isEmpty ? "Добавьте сервер, чтобы начать" : ""
    }

    private func speed(_ arrow: String, _ value: Int64) -> some View {
        HStack(spacing: 6) {
            Text(arrow).foregroundStyle(Palette.muted)
            Text(Fmt.speed(value)).fontWeight(.semibold).monospacedDigit()
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
                        Text(info.meta).font(.caption).foregroundStyle(Palette.muted).lineLimit(1)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").foregroundStyle(Palette.muted)
                }
                .padding(14)
            }
        }
        .buttonStyle(.plain)
    }

    private var currentInfo: (badge: String, title: String, meta: String) {
        let st = store.state
        guard let p = st.active, !p.nodes.isEmpty else {
            return ("+", "Добавить сервер", "Ссылка на подписку или vless://, hy2://…")
        }
        if st.selectedNode == "auto" || !p.nodes.contains(where: { $0.id == st.selectedNode }) {
            if let live = p.nodes.first(where: { $0.id == tunnel.activeNode }) {
                let ping = (store.pings[live.id] ?? 0) > 0 ? ", \(store.pings[live.id]!) мс" : ""
                return (Fmt.badge(live.name), "Автовыбор", "Сейчас: " + Fmt.splitFlag(live.name).rest + ping)
            }
            return ("✦", "Автовыбор", "\(p.name), серверов: \(p.nodes.count)")
        }
        let n = p.nodes.first { $0.id == st.selectedNode }!
        var parts = [n.proto, n.security, p.name].filter { !$0.isEmpty }
        if let ms = store.pings[n.id], ms > 0 { parts.append("\(ms) мс") }
        return (Fmt.badge(n.name), Fmt.splitFlag(n.name).rest, parts.joined(separator: ", "))
    }

    private var routingSwitch: some View {
        HStack(spacing: 4) {
            routingOption("Умный режим", "Российские сайты напрямую", "smart")
            routingOption("Весь трафик", "Всё через сервер", "global")
        }
        .padding(4)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).stroke(Palette.hull))
    }

    private func routingOption(_ title: String, _ sub: String, _ value: String) -> some View {
        let selected = store.state.settings.routing == value
        return Button {
            guard !selected else { return }
            var s = store.state.settings
            s.routing = value
            store.setSettings(s)
            Task { await tunnel.reload() }
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 14, weight: .semibold)).foregroundStyle(selected ? Palette.star : Palette.text)
                Text(sub).font(.system(size: 12)).foregroundStyle(Palette.muted).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12).padding(.vertical, 10)
            .background(selected ? Palette.hull : .clear, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
        .buttonStyle(.plain)
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

private extension View {
    @ViewBuilder func sensoryFeedbackIfAvailable(_ state: ConnState) -> some View {
        if #available(iOS 17.0, *) {
            sensoryFeedback(.success, trigger: state == .connected)
        } else {
            self
        }
    }
}
