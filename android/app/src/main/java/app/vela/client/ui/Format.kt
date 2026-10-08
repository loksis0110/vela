package app.vela.client.ui

import androidx.compose.ui.graphics.Color
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

private val RU = Locale.forLanguageTag("ru")

fun formatBytes(n: Long): String {
    if (n <= 0) return "0 Б"
    val units = listOf("Б", "КБ", "МБ", "ГБ", "ТБ")
    var v = n.toDouble()
    var i = 0
    while (v >= 1024 && i < units.lastIndex) { v /= 1024; i++ }
    val num = if (i == 0) v.toLong().toString() else if (v < 10) String.format(RU, "%.1f", v) else v.toLong().toString()
    return "$num ${units[i]}"
}

fun formatSpeed(n: Long) = formatBytes(n) + "/с"

fun formatDuration(sec: Long): String {
    val s = sec.coerceAtLeast(0)
    val h = s / 3600; val m = (s % 3600) / 60; val r = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, r) else "%02d:%02d".format(m, r)
}

fun formatDate(unix: Long): String = SimpleDateFormat("d MMMM yyyy", RU).format(Date(unix * 1000))

fun formatDateTime(unix: Long): String = SimpleDateFormat("d MMM, HH:mm", RU).format(Date(unix * 1000))

/** Splits a leading flag emoji from a server name. */
fun splitFlag(name: String): Pair<String?, String> {
    val cps = name.trim().codePoints().toArray()
    if (cps.size >= 2 && cps[0] in 0x1F1E6..0x1F1FF && cps[1] in 0x1F1E6..0x1F1FF) {
        val flag = String(cps, 0, 2)
        val rest = String(cps, 2, cps.size - 2).trim()
        return flag to rest.ifEmpty { name }
    }
    return null to name
}

fun badgeFor(name: String): String {
    val (flag, rest) = splitFlag(name)
    return flag ?: rest.trim().firstOrNull()?.uppercase() ?: "✦"
}

data class PingLabel(val text: String, val color: Color)

fun pingLabel(ms: Int?): PingLabel? = when {
    ms == null -> null
    ms == 0 -> PingLabel("…", Vela.Muted)
    ms < 0 -> PingLabel("не работает", Vela.Danger)
    ms < 150 -> PingLabel("$ms мс", Vela.Sea)
    ms < 300 -> PingLabel("$ms мс", Vela.Star)
    ms < 600 -> PingLabel("$ms мс", Vela.Flare)
    else -> PingLabel("$ms мс", Vela.Danger)
}
