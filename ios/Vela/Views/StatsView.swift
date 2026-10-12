import Charts
import SwiftUI

/// Traffic statistics recorded by the Network Extension: today, this month,
/// all time, the last 31 days and the last 12 months.
struct StatsView: View {
    @EnvironmentObject var tunnel: TunnelManager
    @Environment(\.dismiss) private var dismiss
    @State private var loading = true

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if let s = tunnel.summary, s.total.total > 0 {
                        totals(s)
                        daysCard(s)
                        monthsCard(s)
                        footnote
                    } else if loading {
                        ProgressView().frame(maxWidth: .infinity).padding(40)
                    } else {
                        empty
                    }
                }
                .padding(16)
            }
            .background(Palette.ink.ignoresSafeArea())
            .refreshable { await tunnel.loadSummary() }
            .navigationTitle("Статистика")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { dismiss() } } }
        }
        .tint(Palette.star)
        .task {
            await tunnel.loadSummary()
            loading = false
        }
    }

    private func totals(_ s: TrafficSummary) -> some View {
        HStack(spacing: 10) {
            tile(L("Сегодня"), s.today)
            tile(L("Этот месяц"), s.thisMonth)
            tile(L("Всего"), s.total)
        }
    }

    private func tile(_ title: String, _ d: TrafficDay) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title).font(.system(size: 12)).foregroundStyle(Palette.muted).lineLimit(1).minimumScaleFactor(0.75)
            Text(Fmt.bytes(d.total)).font(.system(size: 17, weight: .semibold)).lineLimit(1).minimumScaleFactor(0.7)
            Text("↓ " + Fmt.bytes(d.down)).font(.system(size: 11)).foregroundStyle(Palette.muted).lineLimit(1)
            Text("↑ " + Fmt.bytes(d.up)).font(.system(size: 11)).foregroundStyle(Palette.muted).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(Palette.hull))
        .accessibilityElement(children: .combine)
    }

    private struct Bar: Identifiable {
        let id: String
        let date: Date
        let kind: String
        let bytes: Double
    }

    private func bars(_ s: TrafficSummary) -> [Bar] {
        let down = L("Загрузка"), up = L("Отдача")
        return s.days.flatMap { d -> [Bar] in
            guard let date = d.date else { return [] }
            return [Bar(id: d.day + "d", date: date, kind: down, bytes: Double(d.down)),
                    Bar(id: d.day + "u", date: date, kind: up, bytes: Double(d.up))]
        }
    }

    private func daysCard(_ s: TrafficSummary) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("За 31 день").font(.headline)
            Chart(bars(s)) { b in
                BarMark(x: .value("Day", b.date, unit: .day), y: .value("Bytes", b.bytes))
                    .foregroundStyle(by: .value("Kind", b.kind))
            }
            .chartForegroundStyleScale(domain: [L("Загрузка"), L("Отдача")], range: [Palette.sea, Palette.star])
            .chartYAxis {
                AxisMarks(position: .leading) { value in
                    AxisGridLine().foregroundStyle(Palette.hull)
                    AxisValueLabel {
                        if let v = value.as(Double.self) { Text(Fmt.bytes(Int64(v))).font(.system(size: 10)) }
                    }
                }
            }
            .chartXAxis {
                AxisMarks(values: .stride(by: .day, count: 7)) { value in
                    AxisGridLine().foregroundStyle(Palette.hull)
                    AxisValueLabel {
                        if let d = value.as(Date.self) { Text(Fmt.shortDay(d)).font(.system(size: 10)) }
                    }
                }
            }
            .chartLegend(position: .bottom, alignment: .leading)
            .frame(height: 210)
        }
        .padding(14)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(Palette.hull))
    }

    private func monthsCard(_ s: TrafficSummary) -> some View {
        let months = Array(s.months.reversed())
        let peak = Double(max(1, months.map(\.total).max() ?? 1))
        return VStack(alignment: .leading, spacing: 0) {
            Text("По месяцам").font(.headline).padding(.bottom, 6)
            ForEach(months) { m in
                VStack(alignment: .leading, spacing: 5) {
                    HStack {
                        Text(m.date.map { Fmt.month($0) } ?? m.day)
                        Spacer()
                        Text(Fmt.bytes(m.total)).fontWeight(.semibold).monospacedDigit()
                    }
                    .font(.system(size: 14))
                    GeometryReader { g in
                        ZStack(alignment: .leading) {
                            Capsule().fill(Palette.hull)
                            Capsule().fill(Palette.sea).frame(width: max(m.total > 0 ? 4 : 0, g.size.width * CGFloat(Double(m.total) / peak)))
                        }
                    }
                    .frame(height: 4)
                }
                .padding(.vertical, 8)
                .accessibilityElement(children: .combine)
            }
        }
        .padding(14)
        .background(Palette.deep, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(Palette.hull))
    }

    private var footnote: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !tunnel.summaryLive, let at = tunnel.summaryDate {
                Text(LF("Данные на %@. Свежие цифры появятся, когда VPN будет включён.",
                        at.formatted(Date.FormatStyle(date: .abbreviated, time: .shortened).locale(Lang.locale))))
            }
            Text("Статистика считается на этом устройстве и никуда не отправляется.")
        }
        .font(.footnote)
        .foregroundStyle(Palette.muted)
    }

    private var empty: some View {
        VStack(spacing: 8) {
            Image(systemName: "chart.bar").font(.system(size: 34)).foregroundStyle(Palette.muted)
            Text("Статистики пока нет").font(.headline)
            Text("Она появится после первого подключения: Vela считает трафик через VPN по дням.")
                .foregroundStyle(Palette.muted).multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(32)
    }
}
