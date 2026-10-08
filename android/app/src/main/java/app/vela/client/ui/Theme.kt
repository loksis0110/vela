package app.vela.client.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

/** Colors of one theme, shared with the desktop and iOS apps. */
data class Palette(
    val dark: Boolean,
    val ink: Color,
    val deep: Color,
    val hull: Color,
    val line: Color,
    val star: Color,
    val text: Color,
    val muted: Color,
    val sea: Color,
    val flare: Color,
    val danger: Color,
    val onSea: Color,
    val onStar: Color,
    val glow: Color,
)

val NightPalette = Palette(
    dark = true,
    ink = Color(0xFF0C1424), deep = Color(0xFF121D33), hull = Color(0xFF1B2944), line = Color(0xFF26375A),
    star = Color(0xFFF3E6C4), text = Color(0xFFE6ECF5), muted = Color(0xFF8796B0), sea = Color(0xFF59D0B0),
    flare = Color(0xFFF0A85A), danger = Color(0xFFEE6B6B), onSea = Color(0xFF06221B), onStar = Color(0xFF1B1608),
    glow = Color(0xFF16254A),
)

val DayPalette = Palette(
    dark = false,
    ink = Color(0xFFEEF2F8), deep = Color(0xFFFFFFFF), hull = Color(0xFFE4EAF3), line = Color(0xFFCDD7E5),
    star = Color(0xFF22406F), text = Color(0xFF142033), muted = Color(0xFF5E6C84), sea = Color(0xFF148A6D),
    flare = Color(0xFFC26F17), danger = Color(0xFFCC4040), onSea = Color(0xFFFFFFFF), onStar = Color(0xFFFFFFFF),
    glow = Color(0xFFDFE8F6),
)

/**
 * Night-sail palette. Reading a color subscribes the composable to theme
 * changes, so switching between light and dark is instant.
 */
object Vela {
    var palette by mutableStateOf(NightPalette)

    val Ink get() = palette.ink
    val Deep get() = palette.deep
    val Hull get() = palette.hull
    val Line get() = palette.line
    val Star get() = palette.star
    val Text get() = palette.text
    val Muted get() = palette.muted
    val Sea get() = palette.sea
    val Flare get() = palette.flare
    val Danger get() = palette.danger
    val OnSea get() = palette.onSea
    val OnStar get() = palette.onStar
    val Glow get() = palette.glow
    val isDark get() = palette.dark
}

private fun schemeFor(p: Palette) = if (p.dark) {
    darkColorScheme(
        primary = p.star, onPrimary = p.onStar, secondary = p.sea, onSecondary = p.onSea,
        background = p.ink, onBackground = p.text, surface = p.deep, onSurface = p.text,
        surfaceVariant = p.hull, onSurfaceVariant = p.muted, surfaceContainerHigh = p.deep,
        surfaceContainerHighest = p.hull, surfaceContainer = p.deep, outline = p.line, outlineVariant = p.hull, error = p.danger,
    )
} else {
    lightColorScheme(
        primary = p.star, onPrimary = p.onStar, secondary = p.sea, onSecondary = p.onSea,
        background = p.ink, onBackground = p.text, surface = p.deep, onSurface = p.text,
        surfaceVariant = p.hull, onSurfaceVariant = p.muted, surfaceContainerHigh = p.deep,
        surfaceContainerHighest = p.hull, surfaceContainer = p.deep, outline = p.line, outlineVariant = p.hull, error = p.danger,
    )
}

private val type = Typography(
    headlineSmall = TextStyle(fontSize = 22.sp, fontWeight = FontWeight.SemiBold, letterSpacing = 0.sp),
    titleLarge = TextStyle(fontSize = 20.sp, fontWeight = FontWeight.SemiBold),
    titleMedium = TextStyle(fontSize = 16.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 21.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = TextStyle(fontSize = 12.5.sp, lineHeight = 17.sp),
    labelLarge = TextStyle(fontSize = 15.sp, fontWeight = FontWeight.SemiBold),
)

/** theme: "system", "light" or "dark". */
@Composable
fun VelaTheme(theme: String = "system", content: @Composable () -> Unit) {
    val dark = when (theme) {
        "light" -> false
        "dark" -> true
        else -> isSystemInDarkTheme()
    }
    val palette = if (dark) NightPalette else DayPalette
    if (Vela.palette != palette) Vela.palette = palette
    MaterialTheme(colorScheme = schemeFor(palette), typography = type, content = content)
}
