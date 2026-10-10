package app.vela.client.ui

import androidx.compose.ui.graphics.Color
import app.vela.client.Lang
import app.vela.client.R
import java.text.SimpleDateFormat
import java.util.Date

fun formatBytes(n: Long): String {
    val units = listOf(R.string.unit_b, R.string.unit_kb, R.string.unit_mb, R.string.unit_gb, R.string.unit_tb)
    if (n <= 0) return "0 " + Lang.str(units[0])
    var v = n.toDouble()
    var i = 0
    while (v >= 1024 && i < units.lastIndex) { v /= 1024; i++ }
    val num = if (i == 0) v.toLong().toString() else if (v < 10) String.format(Lang.locale(), "%.1f", v) else v.toLong().toString()
    return "$num ${Lang.str(units[i])}"
}

fun formatSpeed(n: Long) = Lang.str(R.string.unit_per_second, formatBytes(n))

fun formatDuration(sec: Long): String {
    val s = sec.coerceAtLeast(0)
    val h = s / 3600; val m = (s % 3600) / 60; val r = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, r) else "%02d:%02d".format(m, r)
}

fun formatDate(unix: Long): String = SimpleDateFormat(Lang.str(R.string.fmt_date), Lang.locale()).format(Date(unix * 1000))

fun formatDateTime(unix: Long): String = SimpleDateFormat(Lang.str(R.string.fmt_date_time), Lang.locale()).format(Date(unix * 1000))

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
    if (flag != null) return flag
    // first letter or digit: names often start with emoji like 🎮⚡
    val cp = rest.codePoints().toArray().firstOrNull { Character.isLetterOrDigit(it) } ?: return "✦"
    return String(Character.toChars(cp)).uppercase()
}

data class PingLabel(val text: String, val color: Color)

fun formatMs(ms: Int): String = Lang.str(R.string.unit_ms, ms)

fun pingLabel(ms: Int?): PingLabel? = when {
    ms == null -> null
    ms == 0 -> PingLabel("…", Vela.Muted)
    ms < 0 -> PingLabel(Lang.str(R.string.ping_dead), Vela.Danger)
    ms < 150 -> PingLabel(formatMs(ms), Vela.Sea)
    ms < 300 -> PingLabel(formatMs(ms), Vela.Star)
    ms < 600 -> PingLabel(formatMs(ms), Vela.Flare)
    else -> PingLabel(formatMs(ms), Vela.Danger)
}
