package app.vela.client.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.velacore.Velacore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Locale

/** Traffic of one day ("2026-10-09") or month ("2026-10"). */
data class Usage(val key: String, val up: Long, val down: Long) {
    val total: Long get() = up + down
}

data class TrafficSummary(
    val days: List<Usage>,
    val months: List<Usage>,
    val today: Usage,
    val month: Usage,
    val total: Usage,
)

private fun JSONObject?.usage(): Usage =
    if (this == null) Usage("", 0, 0) else Usage(optString("day"), optLong("up"), optLong("down"))

private fun parseSummary(json: String): TrafficSummary {
    val o = JSONObject(json)
    fun list(name: String): List<Usage> {
        val arr = o.optJSONArray(name) ?: return emptyList()
        return (0 until arr.length()).map { arr.optJSONObject(it).usage() }
    }
    return TrafficSummary(
        days = list("days"), months = list("months"),
        today = o.optJSONObject("today").usage(), month = o.optJSONObject("thisMonth").usage(),
        total = o.optJSONObject("total").usage(),
    )
}

private fun parseKey(key: String, pattern: String) = runCatching { SimpleDateFormat(pattern, Locale.US).parse(key) }.getOrNull()

private fun dayLabel(key: String): String =
    parseKey(key, "yyyy-MM-dd")?.let { SimpleDateFormat(Lang.str(R.string.fmt_day_short), Lang.locale()).format(it) } ?: key

private fun monthLabel(key: String): String =
    parseKey(key, "yyyy-MM")?.let { SimpleDateFormat("LLLL yyyy", Lang.locale()).format(it) }
        ?.replaceFirstChar { it.titlecase(Lang.locale()) } ?: key

@Composable
fun StatsScreen(connected: Boolean, onBack: () -> Unit) {
    var tick by remember { mutableIntStateOf(0) }
    val summary by produceState<TrafficSummary?>(null, tick) {
        value = withContext(Dispatchers.IO) {
            runCatching { parseSummary(Velacore.trafficSummary(Store.trafficFile.absolutePath)) }.getOrNull()
        } ?: TrafficSummary(emptyList(), emptyList(), Usage("", 0, 0), Usage("", 0, 0), Usage("", 0, 0))
    }
    // the core adds new numbers every 30 seconds while connected
    LaunchedEffect(connected) {
        while (connected) {
            delay(30_000)
            tick++
        }
    }

    Column(Modifier.fillMaxSize().background(Vela.Ink).safeDrawingPadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 4.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.back)) }
            Text(stringResource(R.string.stats_title), style = MaterialTheme.typography.titleLarge)
        }
        val s = summary
        if (s == null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Vela.Muted) }
            return@Column
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TotalCard(stringResource(R.string.stats_today), s.today, Modifier.weight(1f))
                TotalCard(stringResource(R.string.stats_month), s.month, Modifier.weight(1f))
                TotalCard(stringResource(R.string.stats_total), s.total, Modifier.weight(1f))
            }
            if (s.total.total == 0L) {
                Card(Modifier.padding(top = 12.dp)) {
                    Text(stringResource(R.string.stats_empty), style = MaterialTheme.typography.titleMedium)
                    Text(stringResource(R.string.stats_hint), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(top = 4.dp))
                }
                return@Column
            }
            if (s.days.isNotEmpty()) DaysChart(s.days, Modifier.padding(top = 12.dp))
            val months = s.months.dropWhile { it.total == 0L }.reversed()
            if (months.isNotEmpty()) {
                Text(
                    stringResource(R.string.stats_months), color = Vela.Muted, fontSize = 13.sp,
                    modifier = Modifier.padding(start = 6.dp, top = 16.dp, bottom = 6.dp),
                )
                Card {
                    val peak = months.maxOf { it.total }.coerceAtLeast(1)
                    months.forEachIndexed { i, m -> MonthRow(m, peak, first = i == 0) }
                }
            }
            Text(
                stringResource(R.string.stats_hint), color = Vela.Muted, fontSize = 12.sp, textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth().padding(vertical = 16.dp),
            )
        }
    }
}

@Composable
private fun Card(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(
        modifier.fillMaxWidth().clip(RoundedCornerShape(22.dp)).background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(22.dp)).padding(14.dp),
    ) { content() }
}

@Composable
private fun TotalCard(label: String, u: Usage, modifier: Modifier) {
    Column(
        modifier.clip(RoundedCornerShape(18.dp)).background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(18.dp)).padding(horizontal = 12.dp, vertical = 11.dp),
    ) {
        Text(label, color = Vela.Muted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(formatBytes(u.total), fontWeight = FontWeight.SemiBold, fontSize = 16.sp, maxLines = 1, modifier = Modifier.padding(top = 2.dp))
        Text("↓ " + formatBytes(u.down), color = Vela.Muted, fontSize = 11.5.sp, maxLines = 1)
        Text("↑ " + formatBytes(u.up), color = Vela.Muted, fontSize = 11.5.sp, maxLines = 1)
    }
}

/** The last 31 days: download (bottom) and upload stacked; tap a bar for its numbers. */
@Composable
private fun DaysChart(days: List<Usage>, modifier: Modifier) {
    var selected by remember(days.size) { mutableIntStateOf(days.lastIndex) }
    val down = Vela.Sea
    val up = Vela.Star
    val grid = Vela.Hull
    val highlight = Vela.Hull
    val empty = Vela.Line
    Card(modifier) {
        Text(stringResource(R.string.stats_days), color = Vela.Muted, fontSize = 12.sp)
        val d = days.getOrNull(selected) ?: days.last()
        Text(
            (if (selected == days.lastIndex) stringResource(R.string.stats_today) else dayLabel(d.key)) + ": " + formatBytes(d.total),
            fontWeight = FontWeight.SemiBold, fontSize = 15.sp, modifier = Modifier.padding(top = 2.dp),
        )
        Text("↓ ${formatBytes(d.down)}   ↑ ${formatBytes(d.up)}", color = Vela.Muted, fontSize = 12.sp)
        val peak = days.maxOf { it.total }.coerceAtLeast(1)
        Canvas(
            Modifier.padding(top = 12.dp).fillMaxWidth().height(150.dp).pointerInput(days) {
                detectTapGestures { pos ->
                    val slot = size.width.toFloat() / days.size
                    selected = (pos.x / slot).toInt().coerceIn(0, days.lastIndex)
                }
            },
        ) {
            val n = days.size
            val slot = size.width / n
            val barW = (slot * 0.62f).coerceAtLeast(2f)
            val h = size.height
            val r = CornerRadius(barW / 3, barW / 3)
            drawLine(grid, Offset(0f, h - 0.5f), Offset(size.width, h - 0.5f), 1f)
            drawLine(grid, Offset(0f, h / 2), Offset(size.width, h / 2), 1f)
            days.forEachIndexed { i, u ->
                val x = i * slot + (slot - barW) / 2
                if (i == selected) {
                    drawRoundRect(highlight.copy(alpha = 0.6f), Offset(i * slot, 0f), Size(slot, h), CornerRadius(4f, 4f))
                }
                if (u.total == 0L) {
                    drawRect(empty, Offset(x, h - 2f), Size(barW, 2f))
                    return@forEachIndexed
                }
                val totalH = (u.total.toFloat() / peak * h).coerceAtLeast(2f)
                val downH = u.down.toFloat() / peak * h
                // whole bar in the upload color, download drawn over its lower part
                drawRoundRect(up.copy(alpha = 0.85f), Offset(x, h - totalH), Size(barW, totalH), r)
                if (downH > 0.5f) drawRoundRect(down, Offset(x, h - downH), Size(barW, downH), r)
            }
        }
        Row(Modifier.fillMaxWidth().padding(top = 4.dp)) {
            Text(dayLabel(days.first().key), color = Vela.Muted, fontSize = 11.sp, modifier = Modifier.weight(1f))
            Text(dayLabel(days[days.size / 2].key), color = Vela.Muted, fontSize = 11.sp, textAlign = TextAlign.Center, modifier = Modifier.weight(1f))
            Text(stringResource(R.string.stats_today), color = Vela.Muted, fontSize = 11.sp, textAlign = TextAlign.End, modifier = Modifier.weight(1f))
        }
        Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(8.dp).clip(RoundedCornerShape(2.dp)).background(down))
            Text(" " + stringResource(R.string.graph_download), color = Vela.Muted, fontSize = 11.sp)
            Spacer(Modifier.width(12.dp))
            Box(Modifier.size(8.dp).clip(RoundedCornerShape(2.dp)).background(up))
            Text(" " + stringResource(R.string.graph_upload), color = Vela.Muted, fontSize = 11.sp)
        }
    }
}

@Composable
private fun MonthRow(m: Usage, peak: Long, first: Boolean) {
    Column(Modifier.fillMaxWidth().padding(top = if (first) 0.dp else 12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(monthLabel(m.key), fontSize = 14.sp, modifier = Modifier.weight(1f))
            Text(formatBytes(m.total), fontWeight = FontWeight.SemiBold, fontSize = 14.sp)
        }
        Box(Modifier.padding(top = 6.dp).fillMaxWidth().height(4.dp).clip(RoundedCornerShape(4.dp)).background(Vela.Hull)) {
            val frac = (m.total.toFloat() / peak).coerceIn(0f, 1f)
            if (frac > 0f) Box(Modifier.fillMaxWidth(frac).height(4.dp).clip(RoundedCornerShape(4.dp)).background(Vela.Sea))
        }
        Text("↓ ${formatBytes(m.down)}   ↑ ${formatBytes(m.up)}", color = Vela.Muted, fontSize = 11.5.sp, modifier = Modifier.padding(top = 3.dp))
    }
}
