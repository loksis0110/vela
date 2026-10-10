import Foundation

/// Bytes of one day or month of the traffic statistics.
struct TrafficDay: Identifiable, Hashable {
    let day: String // "2026-10-09", "2026-10" or "total"
    var up: Int64 = 0
    var down: Int64 = 0

    var id: String { day }
    var total: Int64 { up + down }

    init(day: String, up: Int64 = 0, down: Int64 = 0) {
        self.day = day
        self.up = up
        self.down = down
    }

    init(_ o: Any?) {
        let d = o as? [String: Any] ?? [:]
        day = d["day"] as? String ?? ""
        up = (d["up"] as? NSNumber)?.int64Value ?? 0
        down = (d["down"] as? NSNumber)?.int64Value ?? 0
    }

    /// The day as a date (local time zone, like the recorder in the extension).
    var date: Date? {
        TrafficSummary.parser(day.count == 7 ? "yyyy-MM" : "yyyy-MM-dd").date(from: day)
    }
}

/// What the statistics screen shows (core.TrafficSummary).
struct TrafficSummary {
    var days: [TrafficDay]
    var months: [TrafficDay]
    var today: TrafficDay
    var thisMonth: TrafficDay
    var total: TrafficDay

    init?(json: String) {
        guard let data = json.data(using: .utf8),
              let o = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let days = o["days"] as? [Any] else { return nil }
        self.days = days.map { TrafficDay($0) }
        months = (o["months"] as? [Any] ?? []).map { TrafficDay($0) }
        today = TrafficDay(o["today"])
        thisMonth = TrafficDay(o["thisMonth"])
        total = TrafficDay(o["total"])
    }

    static func parser(_ format: String) -> DateFormatter {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = .current
        f.dateFormat = format
        return f
    }

    /// The same numbers on a calendar that ends today: a summary saved
    /// yesterday shows yesterday's traffic in its place and zero for today.
    func rebased(to now: Date = Date()) -> TrafficSummary {
        var s = self
        let cal = Calendar.current
        let dayF = Self.parser("yyyy-MM-dd"), monthF = Self.parser("yyyy-MM")
        let known = Dictionary(days.map { ($0.day, $0) }, uniquingKeysWith: { a, _ in a })
        s.days = (0 ... 30).reversed().map { back in
            let key = dayF.string(from: cal.date(byAdding: .day, value: -back, to: now) ?? now)
            return known[key] ?? TrafficDay(day: key)
        }
        let knownMonths = Dictionary(months.map { ($0.day, $0) }, uniquingKeysWith: { a, _ in a })
        s.months = (0 ... 11).reversed().map { back in
            let key = monthF.string(from: cal.date(byAdding: .month, value: -back, to: now) ?? now)
            return knownMonths[key] ?? TrafficDay(day: key)
        }
        s.today = s.days.last ?? TrafficDay(day: dayF.string(from: now))
        s.thisMonth = s.months.last ?? TrafficDay(day: monthF.string(from: now))
        return s
    }
}

/// One open connection of the running core (core.Connection).
struct Conn: Identifiable, Hashable {
    let id: String
    let network: String
    let host: String
    let destination: String
    let outbound: String
    let rule: String
    let process: String
    let upload: Int64
    let download: Int64
    /// Unix milliseconds.
    let start: Int64

    init(_ d: [String: Any]) {
        func i64(_ k: String) -> Int64 { (d[k] as? NSNumber)?.int64Value ?? 0 }
        id = d["id"] as? String ?? UUID().uuidString
        network = d["network"] as? String ?? ""
        host = d["host"] as? String ?? ""
        destination = d["destination"] as? String ?? ""
        outbound = d["outbound"] as? String ?? ""
        rule = d["rule"] as? String ?? ""
        process = d["process"] as? String ?? ""
        upload = i64("upload")
        download = i64("download")
        start = i64("start")
    }

    static func list(_ json: String) -> [Conn] {
        guard let data = json.data(using: .utf8),
              let arr = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] else { return [] }
        return arr.map { Conn($0) }
    }
}
