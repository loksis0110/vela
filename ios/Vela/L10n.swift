import Foundation

/// Localized text. The Russian text is the key; ru.lproj and en.lproj hold
/// the tables (generated together, so both languages stay complete).
func L(_ key: String) -> String {
    NSLocalizedString(key, comment: "")
}

/// Localized format string: LF("Серверов: %@", "\(n)").
func LF(_ key: String, _ args: CVarArg...) -> String {
    String(format: NSLocalizedString(key, comment: ""), locale: Lang.locale, arguments: args)
}

enum Lang {
    /// "ru" or "en": the language iOS picked for the app from the user's
    /// preferred languages (or the one chosen in Vela's settings).
    static let code: String = (Bundle.main.preferredLocalizations.first ?? "en").hasPrefix("ru") ? "ru" : "en"
    static var isRU: Bool { code == "ru" }
    static let locale = Locale(identifier: code == "ru" ? "ru_RU" : "en_US")

    /// The choice in Vela's settings: "system", "ru" or "en". Applied by iOS
    /// on the next launch (the same per-app setting as Settings → Vela → Language).
    static var choice: String {
        get {
            // only the app's own override counts, not the system list
            guard let id = Bundle.main.bundleIdentifier,
                  let langs = UserDefaults.standard.persistentDomain(forName: id)?["AppleLanguages"] as? [String],
                  let first = langs.first else { return "system" }
            return first.hasPrefix("ru") ? "ru" : "en"
        }
        set {
            if newValue == "system" {
                UserDefaults.standard.removeObject(forKey: "AppleLanguages")
            } else {
                UserDefaults.standard.set([newValue], forKey: "AppleLanguages")
            }
        }
    }
}
