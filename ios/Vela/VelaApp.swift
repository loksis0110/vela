import PhotosUI
import SwiftUI
import UserNotifications
import Velacore

@main
struct VelaApp: App {
    @StateObject private var store = Store.shared
    @StateObject private var tunnel = TunnelManager.shared
    @StateObject private var pickup = Pickup.shared

    init() {
        // messages of the core (errors, server check reasons) in the app's language
        VelacoreSetLanguage(Lang.code)
        UNUserNotificationCenter.current().delegate = NotificationDelegate.shared
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .environmentObject(tunnel)
                .environmentObject(pickup)
                .tint(Palette.star)
                .onAppear { applyTheme(store.state.theme) }
                .onChange(of: store.state.theme) { applyTheme($0) }
        }
    }
}

/// Applies the theme to the whole window. Unlike preferredColorScheme(nil),
/// "system" reliably goes back to following iOS.
@MainActor
func applyTheme(_ theme: String) {
    let style: UIUserInterfaceStyle = theme == "light" ? .light : (theme == "dark" ? .dark : .unspecified)
    for scene in UIApplication.shared.connectedScenes {
        (scene as? UIWindowScene)?.windows.forEach { $0.overrideUserInterfaceStyle = style }
    }
}

private enum LaunchState {
    static var handled = false
}

struct RootView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @EnvironmentObject var pickup: Pickup
    @Environment(\.scenePhase) private var phase
    @State private var showServers = false
    @State private var showSettings = false
    @State private var showAdd = false
    @State private var showStats = false
    @State private var showConnections = false
    @State private var showDiag = false
    @State private var addText = ""
    @State private var toast: String?
    @State private var toastID = UUID()
    /// Subscription or servers from an app link or the clipboard, waiting
    /// for the user's «Добавить».
    @State private var offer: ImportOffer?
    @State private var alertText: String?

    var body: some View {
        HomeView(showServers: $showServers, showSettings: $showSettings, showAdd: $showAdd,
                 showStats: $showStats, showConnections: $showConnections, showDiag: $showDiag, onPaste: pasted)
            .sheet(isPresented: $showServers) {
                ServersView(showAdd: $showAdd)
                    .sheet(isPresented: $showAdd) { AddSheet(initial: addText, onDone: done) }
            }
            .sheet(isPresented: $showSettings) { SettingsView() }
            .sheet(isPresented: Binding(get: { showAdd && !showServers }, set: { showAdd = $0 })) {
                AddSheet(initial: addText, onDone: done)
            }
            .overlay(alignment: .bottom) {
                if let toast {
                    Text(toast)
                        .font(.subheadline)
                        .padding(.horizontal, 16).padding(.vertical, 10)
                        .background(Palette.hull, in: Capsule())
                        .overlay(Capsule().stroke(Palette.line))
                        .padding(.bottom, 24)
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(.easeOut(duration: 0.2), value: toast)
            .task { await launchWork() }
            .onChange(of: phase) { p in
                if p == .active {
                    tunnel.setForeground(true)
                    Task { await tunnel.load() }
                    pickup.check()
                } else if p == .background {
                    // no polling while nobody looks; changes are on disk before
                    // iOS may close the app
                    tunnel.setForeground(false)
                    store.flush()
                }
            }
            .onOpenURL { url in openLink(url.absoluteString) }
            .alert(offer?.title ?? "", isPresented: Binding(get: { offer != nil }, set: { if !$0 { offer = nil } }), presenting: offer) { o in
                Button(L("Добавить")) { add(o) }
                Button(L("Отмена"), role: .cancel) {}
            } message: { o in
                Text(o.summary)
            }
            .alert(alertText ?? "", isPresented: Binding(get: { alertText != nil }, set: { if !$0 { alertText = nil } })) {
                Button("OK", role: .cancel) {}
            }
    }

    /// Links from a provider's site (vela://, happ://, v2rayng://, sub:// …)
    /// or a raw vless://, hy2:// … link: confirm, then import.
    private func openLink(_ raw: String) {
        var err: NSError?
        let json = VelacoreParseDeepLink(raw, &err)
        if let err {
            alertText = err.localizedDescription
            return
        }
        guard let found = ImportOffer(json: json) else {
            alertText = L("В ссылке нет подписки или серверов")
            return
        }
        guard let fresh = store.newPart(of: found) else {
            // all of it is already in Vela: the provider's button then just
            // refreshes the subscription, nothing to confirm
            add(found)
            return
        }
        present(fresh)
    }

    /// Shows the confirmation; sheets are closed first, since an alert
    /// can't appear over them.
    private func present(_ found: ImportOffer) {
        let covered = showServers || showSettings || showAdd || showStats || showConnections || showDiag
        showServers = false
        showSettings = false
        showAdd = false
        showStats = false
        showConnections = false
        showDiag = false
        if covered {
            Task {
                try? await Task.sleep(nanoseconds: 650_000_000)
                offer = found
            }
        } else {
            offer = found
        }
    }

    /// Text pasted on the clipboard banner.
    private func pasted(_ text: String) {
        switch pickup.handle(text) {
        case let .offer(found): present(found)
        case let .message(m): show(m)
        }
    }

    private func add(_ o: ImportOffer) {
        show(o.subscriptions.isEmpty ? L("Добавляем…") : L("Загружаем подписку…"), seconds: 30)
        Task {
            do {
                done(try await store.importOffer(o))
            } catch {
                toast = nil
                alertText = error.localizedDescription
            }
        }
    }

    private func show(_ text: String, seconds: Double = 2.6) {
        let id = UUID()
        toastID = id
        toast = text
        Task {
            try? await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
            if toastID == id { toast = nil }
        }
    }

    @MainActor
    private func launchWork() async {
        await tunnel.load()
        // «Подключаться при запуске»: once per app start, before anything else
        if !LaunchState.handled {
            LaunchState.handled = true
            if store.state.connectOnLaunch, tunnel.state == .disconnected, !store.state.profiles.isEmpty {
                await tunnel.connect()
            }
        }
        pickup.check()
        Reminders.shared.sync()
        await tunnel.loadSummary()
        // subscriptions are refreshed while the app is open, as often as set
        while !Task.isCancelled {
            // the provider changed the servers of the one in use: the tunnel
            // gets the new list (an old server may be gone); while it's down
            // the list is sent when iOS brings it back
            if await store.refreshDue() { await tunnel.reload() }
            try? await Task.sleep(nanoseconds: 10 * 60 * 1_000_000_000)
        }
    }

    private func done(_ message: String?) {
        addText = ""
        guard let message else { return }
        show(message)
        Task {
            await tunnel.pingAll()
            if tunnel.state == .connected { await tunnel.reload() }
        }
    }
}

struct AddSheet: View {
    let initial: String
    let onDone: (String?) -> Void
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var error: String?
    @State private var busy = false
    @State private var scanning = false
    @State private var photo: PhotosPickerItem?

    private func append(_ found: String) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        text = t.isEmpty ? found : t + "\n" + found
        error = nil
    }

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 12) {
                Text("Вставьте ссылки на подписки или на серверы: vless, vmess, trojan, ss, hysteria2, tuic. Несколько штук можно вставить сразу, каждую с новой строки.")
                    .font(.subheadline).foregroundStyle(Palette.muted)
                TextEditor(text: $text)
                    .font(.system(size: 13, design: .monospaced))
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .scrollContentBackground(.hidden)
                    .padding(8)
                    .frame(minHeight: 140, maxHeight: 220)
                    .background(Palette.ink, in: RoundedRectangle(cornerRadius: 10))
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(error == nil ? Palette.line : Palette.danger))
                if let error { Text(error).font(.footnote).foregroundStyle(Palette.danger) }
                // the system Paste button reads the clipboard without the iOS prompt
                PasteButton(payloadType: String.self) { items in
                    let s = items.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    Task { @MainActor in
                        if s.isEmpty { error = L("Буфер обмена пуст") } else { text = s; error = nil }
                    }
                }
                .buttonBorderShape(.capsule)
                .tint(Color(hex: 0x148A6D))
                HStack(spacing: 18) {
                    Button { scanning = true } label: { Label("Сканировать QR", systemImage: "qrcode.viewfinder") }
                    PhotosPicker(selection: $photo, matching: .images) { Label("QR из фото", systemImage: "photo") }
                }
                Spacer()
            }
            .padding(20)
            .background(Palette.deep.ignoresSafeArea())
            .navigationTitle("Добавить серверы")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { onDone(nil); dismiss() }.disabled(busy) }
                ToolbarItem(placement: .confirmationAction) {
                    Button(busy ? L("Загружаем…") : L("Добавить")) { submit() }
                        .disabled(busy || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .tint(Palette.star)
        .presentationDetents([.medium, .large])
        // a swipe down mid-import would lose its result message
        .interactiveDismissDisabled(busy)
        .onAppear { text = initial }
        .fullScreenCover(isPresented: $scanning) {
            QRScannerSheet { code in append(code.trimmingCharacters(in: .whitespacesAndNewlines)) }
        }
        .onChange(of: photo) { item in
            guard let item else { return }
            Task {
                let data = try? await item.loadTransferable(type: Data.self)
                photo = nil
                if let data, let code = QR.decode(data) { append(code) } else { error = L("На картинке не нашли QR-код") }
            }
        }
    }

    private func submit() {
        busy = true
        Task {
            do {
                let msg = try await store.importText(text)
                busy = false
                onDone(msg)
                dismiss()
            } catch {
                busy = false
                self.error = error.localizedDescription
            }
        }
    }
}
