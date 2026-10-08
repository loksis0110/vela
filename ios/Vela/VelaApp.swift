import SwiftUI

@main
struct VelaApp: App {
    @StateObject private var store = Store.shared
    @StateObject private var tunnel = TunnelManager.shared

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .environmentObject(tunnel)
                .preferredColorScheme(.dark)
                .tint(Palette.star)
        }
    }
}

struct RootView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.scenePhase) private var phase
    @State private var showServers = false
    @State private var showSettings = false
    @State private var showAdd = false
    @State private var addText = ""
    @State private var toast: String?

    var body: some View {
        HomeView(showServers: $showServers, showSettings: $showSettings, showAdd: $showAdd)
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
            .task {
                await tunnel.load()
                await store.refreshDue()
            }
            .onChange(of: phase) { p in
                if p == .active { Task { await tunnel.load() } }
            }
            .onOpenURL { url in
                // vela://import/<encoded link> or a raw vless://, hy2:// … link
                var text = url.absoluteString
                if text.hasPrefix("vela://import/") {
                    text = String(text.dropFirst("vela://import/".count)).removingPercentEncoding ?? ""
                }
                addText = text
                showAdd = true
            }
    }

    private func done(_ message: String?) {
        addText = ""
        guard let message else { return }
        toast = message
        Task {
            await tunnel.pingAll()
            if tunnel.state == .connected { await tunnel.reload() }
        }
        Task {
            try? await Task.sleep(nanoseconds: 2_600_000_000)
            toast = nil
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
                Button {
                    if let s = UIPasteboard.general.string, !s.isEmpty { text = s.trimmingCharacters(in: .whitespacesAndNewlines) }
                    else { error = "Буфер обмена пуст" }
                } label: { Label("Вставить из буфера", systemImage: "doc.on.clipboard") }
                Spacer()
            }
            .padding(20)
            .background(Palette.deep.ignoresSafeArea())
            .navigationTitle("Добавить серверы")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { onDone(nil); dismiss() }.disabled(busy) }
                ToolbarItem(placement: .confirmationAction) {
                    Button(busy ? "Загружаем…" : "Добавить") { submit() }
                        .disabled(busy || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .tint(Palette.star)
        .presentationDetents([.medium, .large])
        .onAppear { text = initial }
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
