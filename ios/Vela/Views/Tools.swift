import AVFoundation
import CoreImage
import CoreImage.CIFilterBuiltins
import PhotosUI
import SwiftUI
import UIKit

// MARK: - Traffic graph

/// Download area and upload line for the last two minutes.
struct TrafficGraph: View {
    let points: [TrafficPoint]

    private var peak: Int64 { points.map { max($0.up, $0.down) }.max() ?? 0 }

    var body: some View {
        VStack(spacing: 4) {
            Canvas { ctx, size in
                let slots = 120
                let maxV = Double(max(peak, 16 * 1024)) * 1.15
                let step = size.width / CGFloat(slots - 1)
                let offset = slots - points.count
                func x(_ i: Int) -> CGFloat { CGFloat(i + offset) * step }
                func y(_ v: Int64) -> CGFloat { size.height - CGFloat(Double(v) / maxV) * size.height }
                var grid = Path()
                grid.move(to: CGPoint(x: 0, y: size.height - 0.5)); grid.addLine(to: CGPoint(x: size.width, y: size.height - 0.5))
                grid.move(to: CGPoint(x: 0, y: size.height / 2)); grid.addLine(to: CGPoint(x: size.width, y: size.height / 2))
                ctx.stroke(grid, with: .color(Palette.hull), lineWidth: 1)
                guard points.count > 1 else { return }
                var area = Path()
                area.move(to: CGPoint(x: x(0), y: size.height))
                for (i, p) in points.enumerated() { area.addLine(to: CGPoint(x: x(i), y: y(p.down))) }
                area.addLine(to: CGPoint(x: x(points.count - 1), y: size.height))
                area.closeSubpath()
                ctx.fill(area, with: .color(Palette.sea.opacity(0.22)))
                var down = Path(), up = Path()
                for (i, p) in points.enumerated() {
                    let d = CGPoint(x: x(i), y: y(p.down)), u = CGPoint(x: x(i), y: y(p.up))
                    if i == 0 { down.move(to: d); up.move(to: u) } else { down.addLine(to: d); up.addLine(to: u) }
                }
                ctx.stroke(down, with: .color(Palette.sea), style: StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
                ctx.stroke(up, with: .color(Palette.star.opacity(0.85)), style: StrokeStyle(lineWidth: 1.2, lineCap: .round, lineJoin: .round))
            }
            .frame(height: 64)
            HStack(spacing: 10) {
                legend(Palette.sea, L("загрузка"))
                legend(Palette.star, L("отдача"))
                Text(LF("пик %@", Fmt.speed(peak))).font(.system(size: 11)).foregroundStyle(Palette.muted)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(LF("График трафика, пик %@", Fmt.speed(peak)))
    }

    private func legend(_ color: Color, _ text: String) -> some View {
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 2).fill(color).frame(width: 8, height: 8)
            Text(text).font(.system(size: 11)).foregroundStyle(Palette.muted)
        }
    }
}

// MARK: - Tool cards

struct ToolCard: View {
    let label: String
    let value: String
    let meta: String
    let busy: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 2) {
                Text(label).font(.system(size: 12)).foregroundStyle(Palette.muted)
                HStack(spacing: 6) {
                    Text(value).font(.system(size: 15, weight: .semibold)).lineLimit(1).minimumScaleFactor(0.8)
                    if busy { ProgressView().controlSize(.mini) }
                }
                Text(meta).font(.system(size: 12)).foregroundStyle(Palette.muted).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 14).padding(.vertical, 11)
            .background(Palette.deep, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(Palette.hull))
        }
        .buttonStyle(.plain)
        .disabled(busy)
    }
}

enum ToolText {
    static func ip(_ info: IPInfo?, busy: Bool, error: String?) -> (String, String) {
        if busy, info == nil { return (L("Узнаём…"), L("Спрашиваем сайт")) }
        if let info {
            let meta = [info.city, info.country, info.isp].filter { !$0.isEmpty }
            return ([info.flag, info.ip].filter { !$0.isEmpty }.joined(separator: " "),
                    meta.isEmpty ? L("Так вас видят сайты") : meta.joined(separator: ", "))
        }
        if let error { return (L("Не удалось"), error) }
        return (L("Узнать"), L("Как вас видят сайты"))
    }

    static func speed(_ s: SpeedState) -> (String, String) {
        if s.running {
            switch s.phase {
            case "ping": return (L("Пинг…"), L("Измеряем задержку"))
            case "download": return ("↓ \(Fmt.mbps(s.downMbps)) " + L("Мбит/с"), L("Загрузка…"))
            case "upload": return ("↑ \(Fmt.mbps(s.upMbps)) " + L("Мбит/с"), L("Отдача…"))
            default: return (L("Измеряем…"), "")
            }
        }
        if s.done, let e = s.error, s.downMbps <= 0 { return (L("Не удалось"), e) }
        if s.done { return ("↓ \(Fmt.mbps(s.downMbps))  ↑ \(Fmt.mbps(s.upMbps))", LF("Мбит/с, пинг %@ мс", "\(Int(s.pingMs))")) }
        return (L("Измерить"), L("Через выбранный сервер"))
    }
}

// MARK: - QR codes

enum QR {
    static func image(_ text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let out = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 10, y: 10)),
              let cg = CIContext().createCGImage(out, from: out.extent) else { return nil }
        return UIImage(cgImage: cg)
    }

    static func decode(_ data: Data) -> String? {
        guard let image = CIImage(data: data) ?? UIImage(data: data).flatMap({ CIImage(image: $0) }) else { return nil }
        let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
        let features = detector?.features(in: image) as? [CIQRCodeFeature] ?? []
        return features.compactMap(\.messageString).first
    }
}

/// QR code + text of a server, a subscription or a backup.
struct ShareSheet: View {
    let title: String
    let hint: String
    let text: String
    @Environment(\.dismiss) private var dismiss
    @State private var copied = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text(hint).font(.subheadline).foregroundStyle(Palette.muted)
                    Group {
                        if text.utf8.count <= 2900, let img = QR.image(text) {
                            Image(uiImage: img).interpolation(.none).resizable().scaledToFit()
                        } else {
                            Text("Слишком длинно для QR-кода, отправьте текст").foregroundStyle(.gray).padding(30)
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .padding(14)
                    .background(Color.white, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    Text(text).font(.system(size: 11, design: .monospaced)).foregroundStyle(Palette.muted).lineLimit(4).textSelection(.enabled)
                    HStack {
                        Button(copied ? L("Скопировано") : L("Скопировать")) { Pickup.shared.copy(text); copied = true }
                            .buttonStyle(.bordered)
                        Spacer()
                        ShareLink(item: text) { Label("Отправить", systemImage: "square.and.arrow.up") }
                            .buttonStyle(.borderedProminent).foregroundStyle(Palette.onStar)
                    }
                }
                .padding(20)
            }
            .background(Palette.deep.ignoresSafeArea())
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { dismiss() } } }
        }
        .tint(Palette.star)
        .presentationDetents([.large])
    }
}

struct ShareItem: Identifiable {
    let id = UUID()
    let title: String
    let hint: String
    let text: String
}

// MARK: - Camera scanner

struct QRScannerSheet: View {
    let onCode: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ZStack(alignment: .bottom) {
                QRScanner { code in
                    onCode(code)
                    dismiss()
                }
                .ignoresSafeArea()
                RoundedRectangle(cornerRadius: 24, style: .continuous)
                    .stroke(Color.white.opacity(0.85), lineWidth: 3)
                    .frame(width: 240, height: 240)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .allowsHitTesting(false)
                Text("Наведите камеру на QR-код подписки или сервера")
                    .font(.subheadline).foregroundStyle(.white)
                    .padding(.horizontal, 16).padding(.vertical, 10)
                    .background(.black.opacity(0.55), in: Capsule())
                    .padding(.bottom, 40)
            }
            .background(Color.black)
            .navigationTitle("Сканировать QR")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Отмена") { dismiss() } } }
        }
    }
}

struct QRScanner: UIViewControllerRepresentable {
    let onCode: (String) -> Void
    func makeUIViewController(context _: Context) -> ScannerController {
        let vc = ScannerController()
        vc.onCode = onCode
        return vc
    }

    func updateUIViewController(_: ScannerController, context _: Context) {}
}

final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onCode: ((String) -> Void)?
    private let session = AVCaptureSession()
    private var preview: AVCaptureVideoPreviewLayer?
    private var found = false

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        AVCaptureDevice.requestAccess(for: .video) { granted in
            DispatchQueue.main.async { granted ? self.setup() : self.showDenied() }
        }
    }

    private func setup() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device), session.canAddInput(input) else {
            showDenied()
            return
        }
        session.addInput(input)
        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]
        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        layer.frame = view.layer.bounds
        view.layer.addSublayer(layer)
        preview = layer
        let session = self.session
        DispatchQueue.global(qos: .userInitiated).async { session.startRunning() }
    }

    private func showDenied() {
        let label = UILabel()
        label.text = L("Нет доступа к камере. Разрешите его для Vela в Настройках iPhone или выберите фото с QR-кодом.")
        label.textColor = .white
        label.numberOfLines = 0
        label.textAlignment = .center
        label.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            label.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 32),
            label.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -32),
        ])
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.layer.bounds
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        let session = self.session
        if session.isRunning { DispatchQueue.global(qos: .userInitiated).async { session.stopRunning() } }
    }

    func metadataOutput(_: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject], from _: AVCaptureConnection) {
        guard !found, let code = (objects.first as? AVMetadataMachineReadableCodeObject)?.stringValue, !code.isEmpty else { return }
        found = true
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        onCode?(code)
    }
}

// MARK: - Logs

struct LogsSheet: View {
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @State private var text = L("Загружаем…")

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                ScrollView {
                    Text(text)
                        .font(.system(size: 10.5, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Palette.ink)
                .onChange(of: text) { _ in proxy.scrollTo("end", anchor: .bottom) }
            }
            .navigationTitle("Журнал ядра")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Закрыть") { dismiss() } }
                ToolbarItemGroup(placement: .primaryAction) {
                    Button { Pickup.shared.copy(text) } label: { Image(systemName: "doc.on.doc") }
                        .accessibilityLabel("Скопировать")
                    Button { Task { text = await tunnel.logs() } } label: { Image(systemName: "arrow.clockwise") }
                        .accessibilityLabel("Обновить")
                }
            }
        }
        .tint(Palette.star)
        .task { text = await tunnel.logs() }
    }
}

// MARK: - Chips

struct Chip: View {
    let text: String
    let on: Bool
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            Text(text)
                .font(.system(size: 13))
                .foregroundStyle(on ? Palette.star : Palette.muted)
                .padding(.horizontal, 12).padding(.vertical, 6)
                .background(on ? Palette.hull : .clear, in: Capsule())
                .overlay(Capsule().stroke(on ? Palette.line : Palette.hull))
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

// MARK: - Diagnostics

struct DiagnosticsSheet: View {
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @State private var text = L("Собираем…")
    @State private var copied = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Text("Если VPN не подключается, нажмите «Подключить», дождитесь ошибки и откройте этот отчёт. Скопируйте его и пришлите разработчику. Ключей и адресов серверов в нём нет.")
                        .font(.footnote).foregroundStyle(Palette.muted)
                    Text(text)
                        .font(.system(size: 11, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                        .background(Palette.ink, in: RoundedRectangle(cornerRadius: 12))
                }
                .padding(16)
            }
            .background(Palette.deep.ignoresSafeArea())
            .navigationTitle("Диагностика")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Закрыть") { dismiss() } }
                ToolbarItemGroup(placement: .primaryAction) {
                    Button(copied ? L("Скопировано") : L("Скопировать")) { Pickup.shared.copy(text); copied = true }
                    Button { Task { text = await tunnel.diagnostics() } } label: { Image(systemName: "arrow.clockwise") }
                        .accessibilityLabel("Обновить")
                }
            }
        }
        .tint(Palette.star)
        .task { text = await tunnel.diagnostics() }
    }
}
