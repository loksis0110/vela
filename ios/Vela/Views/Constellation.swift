import SwiftUI

/// The connect control: the Vela constellation draws itself while connecting.
/// Same geometry as the desktop SVG (260×260 viewBox).
struct Constellation: View {
    let state: ConnState
    @State private var drawn: CGFloat = 0
    @State private var spin = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private let stars: [CGPoint] = [
        .init(x: 128, y: 42), .init(x: 128, y: 176), .init(x: 196, y: 160),
        .init(x: 70, y: 188), .init(x: 186, y: 190), .init(x: 92, y: 96),
    ]
    private let faint: [CGPoint] = [.init(x: 56, y: 62), .init(x: 212, y: 74), .init(x: 44, y: 140), .init(x: 214, y: 214)]
    private let lines = [(0, 1), (0, 2), (2, 1), (1, 3), (1, 4), (0, 5)]

    private var active: Bool { state == .connecting || state == .connected }
    private var starColor: Color {
        switch state {
        case .connected: Palette.star
        case .connecting: Palette.flare
        default: Palette.muted
        }
    }
    private var glow: Color {
        switch state {
        case .connected: Palette.sea
        case .connecting: Palette.flare
        default: Palette.muted
        }
    }

    var body: some View {
        GeometryReader { geo in
            let k = min(geo.size.width, geo.size.height) / 260
            let p: (CGPoint) -> CGPoint = { CGPoint(x: $0.x * k, y: $0.y * k) }
            ZStack {
                Circle()
                    .fill(RadialGradient(colors: [glow.opacity(state == .disconnected ? 0.08 : 0.32), .clear],
                                         center: .center, startRadius: 0, endRadius: 128 * k))
                Circle()
                    .strokeBorder(state == .disconnected ? Palette.line : glow.opacity(0.5),
                                  style: StrokeStyle(lineWidth: 1, dash: [2 * k, 6 * k]))
                    .padding(12 * k)
                    .rotationEffect(.degrees(spin ? 360 : 0))
                    .animation(state == .connecting ? .linear(duration: 6).repeatForever(autoreverses: false) : .default, value: spin)

                Path { path in
                    path.move(to: p(stars[0])); path.addLine(to: p(stars[1])); path.addLine(to: p(stars[2])); path.closeSubpath()
                }
                .fill(Palette.sea.opacity(state == .connected ? 0.22 : 0))

                ForEach(Array(lines.enumerated()), id: \.offset) { i, pair in
                    Path { path in
                        path.move(to: p(stars[pair.0])); path.addLine(to: p(stars[pair.1]))
                    }
                    .trim(from: 0, to: drawn)
                    .stroke(state == .connecting ? Palette.flare : Palette.star, style: StrokeStyle(lineWidth: 1.4 * k, lineCap: .round))
                    .opacity(0.9)
                    .animation(.easeOut(duration: 0.5).delay(Double(i) * 0.12), value: drawn)
                }

                ForEach(0 ..< faint.count, id: \.self) { i in
                    Circle().fill(state == .connected ? Palette.muted : Palette.line)
                        .frame(width: 2.6 * k, height: 2.6 * k).position(p(faint[i]))
                }
                ForEach(0 ..< stars.count, id: \.self) { i in
                    Group {
                        if i < 3 {
                            Sparkle().fill(starColor).frame(width: (i == 0 ? 24 : 20) * k, height: (i == 0 ? 24 : 20) * k)
                        } else {
                            Circle().fill(starColor).frame(width: 6.6 * k, height: 6.6 * k)
                        }
                    }
                    .position(p(stars[i]))
                    .animation(.easeInOut(duration: 0.6), value: state)
                }
            }
            .frame(width: 260 * k, height: 260 * k)
        }
        .aspectRatio(1, contentMode: .fit)
        .onAppear { drawn = active ? 1 : 0; spin = state == .connecting && !reduceMotion }
        .onChange(of: state) { new in
            drawn = (new == .connecting || new == .connected) ? 1 : 0
            // no endless spinning with «Reduce Motion»
            spin = new == .connecting && !reduceMotion
        }
        .accessibilityHidden(true)
    }
}

/// Four-pointed star.
struct Sparkle: Shape {
    func path(in rect: CGRect) -> Path {
        let c = CGPoint(x: rect.midX, y: rect.midY)
        let r = min(rect.width, rect.height) / 2
        var path = Path()
        for i in 0 ..< 8 {
            let ang = Double.pi / 4 * Double(i) - Double.pi / 2
            let rr = i % 2 == 0 ? r : r * 0.28
            let dx = CGFloat(Foundation.cos(ang)), dy = CGFloat(Foundation.sin(ang))
            let pt = CGPoint(x: c.x + rr * dx, y: c.y + rr * dy)
            if i == 0 { path.move(to: pt) } else { path.addLine(to: pt) }
        }
        path.closeSubpath()
        return path
    }
}
