import SwiftUI
import UIKit

/// Night-sail palette shared with the desktop and Android apps. Every color
/// follows the light/dark appearance chosen in Settings (or by the system).
enum Palette {
    static let ink = Color(light: 0xEEF2F8, dark: 0x0C1424)
    static let deep = Color(light: 0xFFFFFF, dark: 0x121D33)
    static let hull = Color(light: 0xE4EAF3, dark: 0x1B2944)
    static let line = Color(light: 0xCDD7E5, dark: 0x26375A)
    static let star = Color(light: 0x22406F, dark: 0xF3E6C4)
    static let text = Color(light: 0x142033, dark: 0xE6ECF5)
    static let muted = Color(light: 0x5E6C84, dark: 0x8796B0)
    static let sea = Color(light: 0x148A6D, dark: 0x59D0B0)
    static let flare = Color(light: 0xC26F17, dark: 0xF0A85A)
    static let danger = Color(light: 0xCC4040, dark: 0xEE6B6B)
    static let onSea = Color(light: 0xFFFFFF, dark: 0x06221B)
    static let onStar = Color(light: 0xFFFFFF, dark: 0x1B1608)
    static let glow = Color(light: 0xDFE8F6, dark: 0x16254A)
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }

    init(light: UInt32, dark: UInt32) {
        self.init(UIColor { trait in
            let hex = trait.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                           blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
        })
    }
}

extension ColorScheme {
    /// "system" → nil, "light", "dark".
    static func from(_ theme: String) -> ColorScheme? {
        switch theme {
        case "light": .light
        case "dark": .dark
        default: nil
        }
    }
}

enum Fmt {
    static func bytes(_ n: Int64) -> String {
        let units = Lang.isRU ? ["Б", "КБ", "МБ", "ГБ", "ТБ"] : ["B", "KB", "MB", "GB", "TB"]
        guard n > 0 else { return "0 " + units[0] }
        var v = Double(n), i = 0
        while v >= 1024, i < units.count - 1 { v /= 1024; i += 1 }
        let num = i == 0 ? String(Int(v)) : (v < 10 ? decimal(v) : String(Int(v)))
        return "\(num) \(units[i])"
    }

    /// One decimal with the separator of the app language: 1,5 / 1.5.
    static func decimal(_ v: Double) -> String {
        let s = String(format: "%.1f", v)
        return Lang.isRU ? s.replacingOccurrences(of: ".", with: ",") : s
    }

    static func speed(_ n: Int64) -> String { bytes(n) + (Lang.isRU ? "/с" : "/s") }

    static func duration(_ s: Int) -> String {
        let h = s / 3600, m = (s % 3600) / 60, r = s % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, r) : String(format: "%02d:%02d", m, r)
    }

    static func date(_ unix: Int64) -> String {
        let f = DateFormatter()
        f.locale = Lang.locale
        f.dateFormat = Lang.isRU ? "d MMMM yyyy" : "MMMM d, yyyy"
        return f.string(from: Date(timeIntervalSince1970: TimeInterval(unix)))
    }

    /// "9 окт" / "Oct 9".
    static func shortDay(_ d: Date) -> String {
        let f = DateFormatter()
        f.locale = Lang.locale
        f.setLocalizedDateFormatFromTemplate("dMMM")
        return f.string(from: d)
    }

    /// "Октябрь 2026" / "October 2026".
    static func month(_ d: Date) -> String {
        let f = DateFormatter()
        f.locale = Lang.locale
        f.setLocalizedDateFormatFromTemplate("LLLLyyyy")
        let s = f.string(from: d)
        return s.prefix(1).uppercased() + s.dropFirst()
    }

    /// "5 с", "3 мин", "2 ч" / "5 s", "3 min", "2 h".
    static func age(_ seconds: Int) -> String {
        let s = max(0, seconds)
        if s < 60 { return "\(s) " + L("с") }
        if s < 3600 { return "\(s / 60) " + L("мин") }
        let h = s / 3600, m = (s % 3600) / 60
        var out = "\(h) " + L("ч")
        if m > 0 { out += " \(m) " + L("мин") }
        return out
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

    /// "🇩🇪" → "Германия".
    static func country(_ flag: String) -> String {
        let scalars = Array(flag.unicodeScalars)
        guard scalars.count >= 2 else { return flag }
        let code = String(scalars.prefix(2).compactMap { UnicodeScalar($0.value - 0x1F1E6 + 65).map(Character.init) })
        return Lang.locale.localizedString(forRegionCode: code) ?? code
    }

    static func mbps(_ v: Double) -> String {
        v >= 100 ? String(format: "%.0f", v) : decimal(v)
    }

    static func badge(_ name: String) -> String {
        let s = splitFlag(name)
        if let flag = s.flag { return flag }
        // first letter or digit: names often start with emoji like 🎮⚡
        return s.rest.first { $0.isLetter || $0.isNumber }.map { String($0).uppercased() } ?? "✦"
    }

    static func ping(_ ms: Int?) -> (String, Color)? {
        guard let ms else { return nil }
        switch ms {
        case 0: return ("…", Palette.muted)
        case ..<0: return (L("не работает"), Palette.danger)
        case ..<150: return ("\(ms) " + L("мс"), Palette.sea)
        case ..<300: return ("\(ms) " + L("мс"), Palette.star)
        case ..<600: return ("\(ms) " + L("мс"), Palette.flare)
        default: return ("\(ms) " + L("мс"), Palette.danger)
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

/// Small capsule next to the protocol, e.g. "Xray" for servers on the Xray engine.
struct MiniTag: View {
    let text: String
    var color: Color = Palette.flare
    var body: some View {
        Text(text)
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(color)
            .padding(.horizontal, 5).padding(.vertical, 1)
            .overlay(Capsule().stroke(color.opacity(0.6), lineWidth: 1))
            .fixedSize()
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
