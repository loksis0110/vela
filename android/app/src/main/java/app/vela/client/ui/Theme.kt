package app.vela.client.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

/** Night-sail palette, shared with the desktop and iOS apps. */
object Vela {
    val Ink = Color(0xFF0C1424)
    val Deep = Color(0xFF121D33)
    val Hull = Color(0xFF1B2944)
    val Line = Color(0xFF26375A)
    val Star = Color(0xFFF3E6C4)
    val Text = Color(0xFFE6ECF5)
    val Muted = Color(0xFF8796B0)
    val Sea = Color(0xFF59D0B0)
    val Flare = Color(0xFFF0A85A)
    val Danger = Color(0xFFEE6B6B)
    val OnSea = Color(0xFF06221B)
    val OnStar = Color(0xFF1B1608)
}

private val scheme = darkColorScheme(
    primary = Vela.Star,
    onPrimary = Vela.OnStar,
    secondary = Vela.Sea,
    onSecondary = Vela.OnSea,
    background = Vela.Ink,
    onBackground = Vela.Text,
    surface = Vela.Deep,
    onSurface = Vela.Text,
    surfaceVariant = Vela.Hull,
    onSurfaceVariant = Vela.Muted,
    surfaceContainerHigh = Vela.Deep,
    surfaceContainerHighest = Vela.Hull,
    outline = Vela.Line,
    outlineVariant = Vela.Hull,
    error = Vela.Danger,
)

private val type = Typography(
    headlineSmall = TextStyle(fontSize = 22.sp, fontWeight = FontWeight.SemiBold, letterSpacing = 0.sp),
    titleLarge = TextStyle(fontSize = 20.sp, fontWeight = FontWeight.SemiBold),
    titleMedium = TextStyle(fontSize = 16.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 21.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = TextStyle(fontSize = 12.5.sp, lineHeight = 17.sp),
    labelLarge = TextStyle(fontSize = 15.sp, fontWeight = FontWeight.SemiBold),
)

@Composable
fun VelaTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = scheme, typography = type, content = content)
}
