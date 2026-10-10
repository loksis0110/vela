package app.vela.client.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.graphics.drawable.toBitmap
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.vpn.IpInfo
import app.vela.client.vpn.SpeedState
import app.vela.velacore.Velacore
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.Locale

// ------------------------------------------------------------------ graph

/** Download area and upload line for the last two minutes. */
@Composable
fun TrafficGraph(points: List<Pair<Long, Long>>, modifier: Modifier = Modifier) {
    val down = Vela.Sea
    val up = Vela.Star
    val grid = Vela.Hull
    val peak = points.maxOfOrNull { maxOf(it.first, it.second) } ?: 0L
    Column(modifier) {
        Canvas(Modifier.fillMaxWidth().height(64.dp)) {
            val slots = 120
            val max = (peak.coerceAtLeast(16 * 1024)).toFloat() * 1.15f
            val step = size.width / (slots - 1)
            val offset = slots - points.size
            fun x(i: Int) = (i + offset) * step
            fun y(v: Long) = size.height - (v / max) * size.height
            drawLine(grid, Offset(0f, size.height - 0.5f), Offset(size.width, size.height - 0.5f), 1f)
            drawLine(grid, Offset(0f, size.height / 2), Offset(size.width, size.height / 2), 1f)
            if (points.size < 2) return@Canvas
            val area = Path().apply {
                moveTo(x(0), size.height)
                points.forEachIndexed { i, p -> lineTo(x(i), y(p.second)) }
                lineTo(x(points.lastIndex), size.height)
                close()
            }
            drawPath(area, down.copy(alpha = 0.22f))
            val downLine = Path().apply { points.forEachIndexed { i, p -> if (i == 0) moveTo(x(i), y(p.second)) else lineTo(x(i), y(p.second)) } }
            drawPath(downLine, down, style = Stroke(width = 1.6.dp.toPx(), cap = StrokeCap.Round))
            val upLine = Path().apply { points.forEachIndexed { i, p -> if (i == 0) moveTo(x(i), y(p.first)) else lineTo(x(i), y(p.first)) } }
            drawPath(upLine, up.copy(alpha = 0.85f), style = Stroke(width = 1.2.dp.toPx(), cap = StrokeCap.Round))
        }
        Row(Modifier.fillMaxWidth().padding(top = 4.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(8.dp).clip(RoundedCornerShape(2.dp)).background(down))
            Text(" " + stringResource(R.string.graph_download), color = Vela.Muted, fontSize = 11.sp)
            Spacer(Modifier.width(10.dp))
            Box(Modifier.size(8.dp).clip(RoundedCornerShape(2.dp)).background(up))
            Text(" " + stringResource(R.string.graph_upload), color = Vela.Muted, fontSize = 11.sp)
            Spacer(Modifier.width(10.dp))
            Text(stringResource(R.string.graph_peak, formatSpeed(peak)), color = Vela.Muted, fontSize = 11.sp)
        }
    }
}

// ------------------------------------------------------------------ tool cards

@Composable
fun ToolCard(label: String, value: String, meta: String, busy: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Column(
        modifier
            .clip(RoundedCornerShape(18.dp))
            .background(Vela.Deep)
            .border(1.dp, Vela.Hull, RoundedCornerShape(18.dp))
            .clickable(enabled = !busy, onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 11.dp),
    ) {
        Text(label, color = Vela.Muted, fontSize = 12.sp)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(value, fontWeight = FontWeight.SemiBold, fontSize = 15.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
            if (busy) {
                Spacer(Modifier.width(8.dp))
                CircularProgressIndicator(Modifier.size(12.dp), color = Vela.Muted, strokeWidth = 1.5.dp)
            }
        }
        Text(meta, color = Vela.Muted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

fun ipTexts(info: IpInfo?, busy: Boolean, error: String?): Pair<String, String> = when {
    busy && info == null -> Lang.str(R.string.ip_busy) to Lang.str(R.string.ip_busy_meta)
    info != null -> (listOf(info.flag, info.ip).filter { it.isNotBlank() }.joinToString(" ")) to
        listOf(info.city, info.country, info.isp).filter { it.isNotBlank() }.distinct().joinToString(", ").ifEmpty { Lang.str(R.string.ip_seen) }
    error != null -> Lang.str(R.string.failed) to error
    else -> Lang.str(R.string.ip_check) to Lang.str(R.string.ip_check_meta)
}

fun speedTexts(s: SpeedState): Pair<String, String> {
    fun mbps(v: Double) = if (v >= 100) "%.0f".format(Lang.locale(), v) else "%.1f".format(Lang.locale(), v)
    return when {
        s.running -> when (s.phase) {
            "ping" -> Lang.str(R.string.speed_ping) to Lang.str(R.string.speed_ping_meta)
            "download" -> Lang.str(R.string.speed_down_value, mbps(s.downMbps)) to Lang.str(R.string.speed_down_meta)
            "upload" -> Lang.str(R.string.speed_up_value, mbps(s.upMbps)) to Lang.str(R.string.speed_up_meta)
            else -> Lang.str(R.string.speed_measuring) to ""
        }
        s.done && s.error != null && s.downMbps <= 0 -> Lang.str(R.string.failed) to s.error
        s.done -> "↓ ${mbps(s.downMbps)}  ↑ ${mbps(s.upMbps)}" to Lang.str(R.string.speed_done_meta, s.pingMs.toInt())
        else -> Lang.str(R.string.speed_start) to Lang.str(R.string.speed_start_meta)
    }
}

// ------------------------------------------------------------------ QR

fun qrBitmap(text: String, size: Int = 720): ImageBitmap? = runCatching {
    val png = Velacore.encodeQR(text, size)
    BitmapFactory.decodeByteArray(png, 0, png.size).asImageBitmap()
}.getOrNull()

/** Reads a QR code from a picture (gallery, screenshot). */
suspend fun decodeQr(context: Context, uri: Uri): String? = withContext(Dispatchers.Default) {
    val opts = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    context.contentResolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, opts) }
    var sample = 1
    while (opts.outWidth / sample > 2000 || opts.outHeight / sample > 2000) sample *= 2
    val bmp = context.contentResolver.openInputStream(uri)?.use {
        BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
    } ?: return@withContext null
    decodeQr(bmp)
}

fun decodeQr(bmp: Bitmap): String? {
    val px = IntArray(bmp.width * bmp.height)
    bmp.getPixels(px, 0, bmp.width, 0, 0, bmp.width, bmp.height)
    val source = RGBLuminanceSource(bmp.width, bmp.height, px)
    val hints = mapOf(DecodeHintType.TRY_HARDER to true)
    return runCatching { MultiFormatReader().decode(BinaryBitmap(HybridBinarizer(source)), hints).text }
        .recoverCatching { MultiFormatReader().decode(BinaryBitmap(HybridBinarizer(source.invert())), hints).text }
        .getOrNull()
}

fun copyText(context: Context, text: String) {
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Vela", text))
}

/** QR code + text of a server, a subscription or a backup. */
@Composable
fun ShareDialog(title: String, hint: String, text: String, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val qr by produceState<Pair<Boolean, ImageBitmap?>>(false to null, text) {
        value = true to withContext(Dispatchers.Default) { qrBitmap(text) }
    }
    var copied by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Vela.Deep,
        title = { Text(title, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(hint, color = Vela.Muted, fontSize = 13.sp)
                Box(
                    Modifier.padding(top = 12.dp).fillMaxWidth().clip(RoundedCornerShape(16.dp)).background(Color.White).padding(12.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    val (ready, img) = qr
                    if (img != null) {
                        Image(img, stringResource(R.string.qr_code), Modifier.fillMaxWidth().heightIn(max = 300.dp))
                    } else if (ready) {
                        Text(stringResource(R.string.qr_too_long), color = Color.DarkGray, fontSize = 13.sp, modifier = Modifier.padding(24.dp))
                    } else {
                        CircularProgressIndicator(Modifier.padding(40.dp), color = Color.Gray)
                    }
                }
                SelectionContainer {
                    Text(
                        text, fontFamily = FontFamily.Monospace, fontSize = 11.sp, color = Vela.Muted, maxLines = 4, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(top = 10.dp),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = {
                runCatching {
                    context.startActivity(
                        Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text), title),
                    )
                }
            }) { Text(stringResource(R.string.send)) }
        },
        dismissButton = {
            TextButton(onClick = { copyText(context, text); copied = true }) { Text(stringResource(if (copied) R.string.copied else R.string.copy), color = Vela.Muted) }
        },
    )
}

// ------------------------------------------------------------------ logs

@Composable
fun LogsDialog(onDismiss: () -> Unit) {
    val context = LocalContext.current
    var tick by remember { mutableStateOf(0) }
    val loading = stringResource(R.string.loading)
    val empty = stringResource(R.string.log_empty)
    val text by produceState(loading, tick) {
        value = withContext(Dispatchers.IO) {
            Velacore.readLog(Store.logFile.absolutePath, 300).ifBlank { empty }
        }
    }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Column(
            Modifier.fillMaxWidth(0.94f).clip(RoundedCornerShape(24.dp)).background(Vela.Deep).padding(18.dp),
        ) {
            Text(stringResource(R.string.log_title), fontWeight = FontWeight.SemiBold, fontSize = 18.sp)
            Box(
                Modifier.padding(top = 10.dp).fillMaxWidth().heightIn(min = 200.dp, max = 460.dp)
                    .clip(RoundedCornerShape(12.dp)).background(Vela.Ink).border(1.dp, Vela.Hull, RoundedCornerShape(12.dp)),
            ) {
                val scroll = rememberScrollState()
                LaunchedEffect(text) { scroll.scrollTo(scroll.maxValue) }
                SelectionContainer {
                    Text(
                        text, fontFamily = FontFamily.Monospace, fontSize = 10.5.sp, lineHeight = 15.sp,
                        modifier = Modifier.verticalScroll(scroll).padding(10.dp),
                    )
                }
            }
            Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.End) {
                TextButton(onClick = { copyText(context, text) }) { Text(stringResource(R.string.copy), color = Vela.Muted) }
                TextButton(onClick = { tick++ }) { Text(stringResource(R.string.refresh), color = Vela.Muted) }
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.close)) }
            }
        }
    }
}

// ------------------------------------------------------------------ app picker

data class AppEntry(val pkg: String, val label: String)

private fun launcherApps(context: Context): List<AppEntry> {
    val pm = context.packageManager
    val intent = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
    @Suppress("DEPRECATION")
    val list = pm.queryIntentActivities(intent, 0)
    return list.map { it.activityInfo.packageName to it.loadLabel(pm).toString() }
        .filter { it.first != context.packageName }
        .distinctBy { it.first }
        .map { AppEntry(it.first, it.second) }
        .sortedBy { it.label.lowercase() }
}

@Composable
private fun AppIcon(pkg: String) {
    val context = LocalContext.current
    val icon by produceState<ImageBitmap?>(null, pkg) {
        value = withContext(Dispatchers.IO) {
            runCatching { context.packageManager.getApplicationIcon(pkg).toBitmap(96, 96).asImageBitmap() }.getOrNull()
        }
    }
    val img = icon
    if (img != null) Image(img, null, Modifier.size(34.dp)) else Box(Modifier.size(34.dp).clip(RoundedCornerShape(9.dp)).background(Vela.Hull))
}

/** Picks apps for split tunneling. Apps not installed any more stay in the list. */
@Composable
fun AppPickerDialog(selected: List<String>, onDismiss: () -> Unit, onDone: (List<String>) -> Unit) {
    val context = LocalContext.current
    val apps by produceState<List<AppEntry>?>(null) { value = withContext(Dispatchers.IO) { launcherApps(context) } }
    var chosen by remember { mutableStateOf(selected.toSet()) }
    var query by remember { mutableStateOf("") }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Column(Modifier.fillMaxWidth(0.94f).fillMaxSize(0.9f).clip(RoundedCornerShape(24.dp)).background(Vela.Deep).padding(16.dp)) {
            Text(stringResource(R.string.apps_title), fontWeight = FontWeight.SemiBold, fontSize = 18.sp)
            OutlinedTextField(
                value = query, onValueChange = { query = it }, singleLine = true,
                leadingIcon = { Icon(Icons.Rounded.Search, null, tint = Vela.Muted) },
                placeholder = { Text(stringResource(R.string.search), color = Vela.Muted) },
                colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line, cursorColor = Vela.Star),
                modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
            )
            val list = apps
            if (list == null) {
                Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Vela.Muted) }
            } else {
                val q = query.trim().lowercase()
                val shown = list.filter { q.isEmpty() || q in it.label.lowercase() || q in it.pkg }
                    .sortedByDescending { it.pkg in selected }
                LazyColumn(Modifier.weight(1f).padding(top = 6.dp)) {
                    items(shown, key = { it.pkg }) { app ->
                        val on = app.pkg in chosen
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp))
                                .clickable { chosen = if (on) chosen - app.pkg else chosen + app.pkg }
                                .padding(horizontal = 6.dp, vertical = 6.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            AppIcon(app.pkg)
                            Column(Modifier.weight(1f).padding(start = 12.dp)) {
                                Text(app.label, fontSize = 14.5.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(app.pkg, color = Vela.Muted, fontSize = 11.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                            Checkbox(on, null, colors = CheckboxDefaults.colors(checkedColor = Vela.Sea, checkmarkColor = Vela.OnSea, uncheckedColor = Vela.Line))
                        }
                    }
                }
            }
            Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(R.string.apps_selected, chosen.size), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.weight(1f))
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel), color = Vela.Muted) }
                TextButton(onClick = { onDone(chosen.toList().sorted()) }) { Text(stringResource(R.string.done)) }
            }
        }
    }
}

/** Human name of an app, or its package when it is not installed. */
fun appLabel(context: Context, pkg: String): String = runCatching {
    val pm = context.packageManager
    pm.getApplicationLabel(pm.getApplicationInfo(pkg, PackageManager.GET_META_DATA)).toString()
}.getOrDefault(pkg)

// ------------------------------------------------------------------ countries

/** "🇩🇪" → "Germany" / "Германия". */
fun countryName(flag: String): String {
    val cps = flag.codePoints().toArray()
    if (cps.size < 2) return flag
    val code = String(charArrayOf('A' + (cps[0] - 0x1F1E6), 'A' + (cps[1] - 0x1F1E6)))
    return Locale("", code).getDisplayCountry(Lang.locale()).ifBlank { code }
}

@Composable
fun ChipRow(content: @Composable () -> Unit) {
    Row(
        Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) { content() }
}

@Composable
fun Chip(text: String, on: Boolean, onClick: () -> Unit) {
    Text(
        text,
        fontSize = 13.sp,
        color = if (on) Vela.Star else Vela.Muted,
        maxLines = 1,
        modifier = Modifier
            .clip(RoundedCornerShape(50))
            .background(if (on) Vela.Hull else Color.Transparent)
            .border(1.dp, if (on) Vela.Line else Vela.Hull, RoundedCornerShape(50))
            .clickable(onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 6.dp),
    )
}
