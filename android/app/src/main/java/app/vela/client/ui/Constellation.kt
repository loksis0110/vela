package app.vela.client.ui

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.animateFloat
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import app.vela.client.vpn.ConnState
import kotlin.math.cos
import kotlin.math.sin

// Same geometry as the desktop SVG (viewBox 260×260).
private val stars = listOf(
    Offset(128f, 42f), Offset(128f, 176f), Offset(196f, 160f),
    Offset(70f, 188f), Offset(186f, 190f), Offset(92f, 96f),
)
private val faint = listOf(Offset(56f, 62f), Offset(212f, 74f), Offset(44f, 140f), Offset(214f, 214f))
private val lines = listOf(0 to 1, 0 to 2, 2 to 1, 1 to 3, 1 to 4, 0 to 5)

/** The connect control: the Vela constellation that draws itself as you connect. */
@Composable
fun Constellation(state: ConnState, modifier: Modifier = Modifier) {
    val drawn by animateFloatAsState(
        targetValue = if (state == ConnState.Connecting || state == ConnState.Connected) 1f else 0f,
        animationSpec = tween(900), label = "lines",
    )
    val sail by animateFloatAsState(if (state == ConnState.Connected) 0.22f else 0f, tween(800), label = "sail")
    val starColor by animateColorAsState(
        when (state) {
            ConnState.Connected -> Vela.Star
            ConnState.Connecting -> Vela.Flare
            else -> Vela.Muted
        }, tween(600), label = "star",
    )
    val glow by animateColorAsState(
        when (state) {
            ConnState.Connected -> Vela.Sea
            ConnState.Connecting -> Vela.Flare
            else -> Vela.Muted
        }, tween(800), label = "glow",
    )
    val lineColor = if (state == ConnState.Connecting) Vela.Flare else Vela.Star
    val spin by rememberInfiniteTransition(label = "orbit").animateFloat(
        0f, 360f, infiniteRepeatable(tween(6000, easing = LinearEasing)), label = "spin",
    )

    Box(modifier) {
        Canvas(Modifier.fillMaxSize()) {
            val k = size.minDimension / 260f
            fun p(o: Offset) = Offset(o.x * k, o.y * k)
            val c = Offset(130f * k, 130f * k)

            drawCircle(
                Brush.radialGradient(listOf(glow.copy(alpha = if (state == ConnState.Disconnected) 0.08f else 0.32f), Color.Transparent), c, 128f * k),
                128f * k, c,
            )
            rotate(if (state == ConnState.Connecting) spin else 0f, c) {
                drawCircle(
                    if (state == ConnState.Disconnected) Vela.Line else glow.copy(alpha = 0.5f), 118f * k, c,
                    style = Stroke(width = 1f * k, pathEffect = PathEffect.dashPathEffect(floatArrayOf(2f * k, 6f * k))),
                )
            }
            if (sail > 0f) {
                val path = Path().apply {
                    moveTo(p(stars[0]).x, p(stars[0]).y); lineTo(p(stars[1]).x, p(stars[1]).y)
                    lineTo(p(stars[2]).x, p(stars[2]).y); close()
                }
                drawPath(path, Vela.Sea.copy(alpha = sail))
            }
            lines.forEachIndexed { i, (a, b) ->
                // each line starts a little after the previous one
                val t = ((drawn * (1f + lines.size * 0.15f)) - i * 0.15f).coerceIn(0f, 1f)
                if (t > 0f) {
                    val s = p(stars[a]); val e = p(stars[b])
                    drawLine(lineColor.copy(alpha = 0.9f), s, s + (e - s) * t, 1.4f * k, StrokeCap.Round)
                }
            }
            faint.forEach { drawCircle(if (state == ConnState.Connected) Vela.Muted else Vela.Line, 1.3f * k, p(it)) }
            stars.forEachIndexed { i, o ->
                if (i < 3) sparkle(p(o), (if (i == 0) 12f else 10f) * k, starColor)
                else drawCircle(starColor, 3.3f * k, p(o))
            }
        }
    }
}

private fun DrawScope.sparkle(c: Offset, r: Float, color: Color) {
    val path = Path()
    for (i in 0 until 8) {
        val ang = Math.PI / 4 * i - Math.PI / 2
        val rr = if (i % 2 == 0) r else r * 0.28f
        val x = c.x + (rr * cos(ang)).toFloat()
        val y = c.y + (rr * sin(ang)).toFloat()
        if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
    }
    path.close()
    drawPath(path, color)
}
