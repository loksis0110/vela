import SwiftUI

/// Night-sail palette shared with the desktop and Android apps.
enum Palette {
    static let ink = Color(hex: 0x0C1424)
    static let deep = Color(hex: 0x121D33)
    static let hull = Color(hex: 0x1B2944)
    static let line = Color(hex: 0x26375A)
    static let star = Color(hex: 0xF3E6C4)
    static let text = Color(hex: 0xE6ECF5)
    static let muted = Color(hex: 0x8796B0)
    static let sea = Color(hex: 0x59D0B0)
    static let flare = Color(hex: 0xF0A85A)
    static let danger = Color(hex: 0xEE6B6B)
    static let onSea = Color(hex: 0x06221B)
    static let onStar = Color(hex: 0x1B1608)
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

enum Fmt {
    static func bytes(_ n: Int64) -> String {
        guard n > 0 else { return "0 Б" }
        let units = ["Б", "КБ", "МБ", "ГБ", "ТБ"]
        var v = Double(n), i = 0
        while v >= 1024, i < units.count - 1 { v /= 1024; i += 1 }
        let num = i == 0 ? String(Int(v)) : (v < 10 ? String(format: "%.1f", v).replacingOccurrences(of: ".", with: ",") : String(Int(v)))
        return "\(num) \(units[i])"
    }

    static func speed(_ n: Int64) -> String { bytes(n) + "/с" }

    static func duration(_ s: Int) -> String {
        let h = s / 3600, m = (s % 3600) / 60, r = s % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, r) : String(format: "%02d:%02d", m, r)
    }

    static func date(_ unix: Int64) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ru_RU")
        f.dateFormat = "d MMMM yyyy"
        return f.string(from: Date(timeIntervalSince1970: TimeInterval(unix)))
    }

    /// Splits a leading flag emoji from a server name.
    static func splitFlag(_ name: String) -> (flag: String?, rest: String) {
        let scalars = Array(name.trimmingCharacters(in: .whitespaces).unicodeScalars)
        if scalars.count >= 2, (0x1F1E6 ... 0x1F1FF).contains(scalars[0].value), (0x1F1E6 ... 0x1F1FF).contains(scalars[1].value) {
            var flag = String.UnicodeScalarView()
            flag.append(scalars[0]); flag.append(scalars[1])
            let rest = String(String.UnicodeScalarView(scalars.dropFirst(2))).trimmingCharacters(in: .whitespaces)
            return (String(flag), rest.isEmpty ? name : rest)
        }
        return (nil, name)
    }

    static func badge(_ name: String) -> String {
        let s = splitFlag(name)
        return s.flag ?? s.rest.first.map { String($0).uppercased() } ?? "✦"
    }

    static func ping(_ ms: Int?) -> (String, Color)? {
        guard let ms else { return nil }
        switch ms {
        case 0: return ("…", Palette.muted)
        case ..<0: return ("не работает", Palette.danger)
        case ..<150: return ("\(ms) мс", Palette.sea)
        case ..<300: return ("\(ms) мс", Palette.star)
        case ..<600: return ("\(ms) мс", Palette.flare)
        default: return ("\(ms) мс", Palette.danger)
        }
    }
}

struct Badge: View {
    let text: String
    var size: CGFloat = 42
    var body: some View {
        Text(text)
            .font(.system(size: size * 0.45))
            .foregroundStyle(Palette.star)
            .frame(width: size, height: size)
            .background(Palette.hull, in: RoundedRectangle(cornerRadius: size * 0.28, style: .continuous))
    }
}

struct Card<Content: View>: View {
    @ViewBuilder var content: Content
    var body: some View {
        content
            .background(Palette.deep, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).stroke(Palette.hull, lineWidth: 1))
    }
}
