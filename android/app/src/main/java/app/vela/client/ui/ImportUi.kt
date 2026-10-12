package app.vela.client.ui

import android.content.ClipboardManager
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ContentPaste
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.vela.client.R
import app.vela.client.data.Store
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject

/** What an app link, shared text or the clipboard offers to add (from the core). */
data class ImportOffer(
    val subscriptions: List<String>,
    val links: String,
    val linkCount: Int,
    val name: String,
) {
    val isEmpty: Boolean get() = subscriptions.isEmpty() && (linkCount == 0 || links.isBlank())

    /** Text for the ordinary import path. */
    val text: String get() = (subscriptions + links).filter { it.isNotBlank() }.joinToString("\n")

    /** Without what is already added. */
    fun withoutKnown(): ImportOffer {
        val subs = subscriptions.filterNot { Store.hasSubscription(it) }
        val knownLinks = links.isBlank() || Store.hasAllLinks(links)
        return copy(subscriptions = subs, links = if (knownLinks) "" else links, linkCount = if (knownLinks) 0 else linkCount)
    }

    companion object {
        fun from(json: String): ImportOffer {
            val o = JSONObject(json)
            val arr = o.optJSONArray("subscriptions")
            val subs = if (arr == null) emptyList() else (0 until arr.length()).map { arr.optString(it).trim() }.filter { it.isNotEmpty() }
            return ImportOffer(subs, o.optString("links").trim(), o.optInt("linkCount"), o.optString("name").trim())
        }
    }
}

fun hostOf(url: String): String = runCatching { Uri.parse(url).host }.getOrNull()?.removePrefix("www.") ?: url

/** State of the subscription download shown in the confirmation. */
private sealed interface Preview {
    data object Loading : Preview
    data class Ready(val sub: JSONObject) : Preview
    data class Failed(val message: String) : Preview
}

/**
 * «Add subscription?» for an app link from a provider's site or shared text.
 * A single subscription is downloaded right away to show its name and servers.
 */
@Composable
fun ImportConfirmDialog(
    offer: ImportOffer,
    onDismiss: () -> Unit,
    onConfirm: (preview: JSONObject?, done: (String?) -> Unit) -> Unit,
) {
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val single = offer.subscriptions.singleOrNull()
    var preview by remember(offer) { mutableStateOf<Preview?>(if (single != null) Preview.Loading else null) }
    LaunchedEffect(offer) {
        if (single != null) {
            preview = try {
                Preview.Ready(Store.preview(single))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Preview.Failed(e.message ?: "")
            }
        }
    }
    val title = when {
        offer.subscriptions.size > 1 -> R.string.confirm_add_subs
        offer.subscriptions.size == 1 -> R.string.confirm_add_sub
        offer.linkCount == 1 -> R.string.confirm_add_server
        else -> R.string.confirm_add_servers
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        containerColor = Vela.Deep,
        title = { Text(stringResource(title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                val p = preview
                val providerTitle = (p as? Preview.Ready)?.sub?.optString("title").orEmpty()
                val name = offer.name.ifBlank { providerTitle }
                if (name.isNotBlank()) Text(name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(bottom = 6.dp))
                offer.subscriptions.forEach { url ->
                    Text(hostOf(url), fontWeight = FontWeight.SemiBold)
                    Text(
                        url, color = Vela.Muted, fontSize = 11.5.sp, fontFamily = FontFamily.Monospace,
                        maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(bottom = 6.dp),
                    )
                    if (Store.hasSubscription(url)) {
                        Text(stringResource(R.string.confirm_already_added), color = Vela.Muted, fontSize = 13.sp, modifier = Modifier.padding(bottom = 6.dp))
                    }
                }
                when (p) {
                    Preview.Loading -> Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 4.dp)) {
                        CircularProgressIndicator(Modifier.size(14.dp), color = Vela.Muted, strokeWidth = 1.5.dp)
                        Spacer(Modifier.size(8.dp))
                        Text(stringResource(R.string.confirm_loading), color = Vela.Muted, fontSize = 13.sp)
                    }
                    is Preview.Ready -> {
                        val count = p.sub.optJSONArray("nodes")?.length() ?: 0
                        Text(stringResource(R.string.confirm_servers, count), fontSize = 14.sp)
                        val total = p.sub.optLong("total")
                        val used = p.sub.optLong("upload") + p.sub.optLong("download")
                        if (total > 0) {
                            Text(stringResource(R.string.sub_traffic_of, formatBytes(used), formatBytes(total)), color = Vela.Muted, fontSize = 13.sp)
                        }
                        val expire = p.sub.optLong("expire")
                        if (expire > 0) Text(stringResource(R.string.confirm_until, formatDate(expire)), color = Vela.Muted, fontSize = 13.sp)
                    }
                    is Preview.Failed -> Text(p.message.ifBlank { stringResource(R.string.error_generic) }, color = Vela.Danger, fontSize = 13.sp)
                    null -> {}
                }
                if (offer.linkCount > 0) Text(stringResource(R.string.confirm_servers, offer.linkCount), fontSize = 14.sp)
                error?.let { Text(it, color = Vela.Danger, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
            }
        },
        confirmButton = {
            Button(
                enabled = !busy,
                onClick = {
                    busy = true
                    error = null
                    onConfirm((preview as? Preview.Ready)?.sub) { err -> busy = false; error = err }
                },
            ) { Text(stringResource(if (busy) R.string.adding else R.string.add)) }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text(stringResource(R.string.cancel), color = Vela.Muted) } },
    )
}

/** Compact «There is a link in the clipboard. Add?» card. */
@Composable
fun ClipboardPrompt(offer: ImportOffer, busy: Boolean, onAdd: () -> Unit, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val text = when {
        offer.subscriptions.size == 1 -> stringResource(R.string.clip_sub, hostOf(offer.subscriptions[0]))
        offer.subscriptions.size > 1 -> stringResource(R.string.clip_subs, offer.subscriptions.size)
        offer.linkCount == 1 -> stringResource(R.string.clip_server)
        else -> stringResource(R.string.clip_servers, offer.linkCount)
    }
    Row(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(Vela.Deep)
            .border(1.dp, Vela.Line, RoundedCornerShape(18.dp))
            .padding(start = 14.dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.ContentPaste, null, tint = Vela.Star, modifier = Modifier.size(20.dp))
        Text(text, fontSize = 13.5.sp, maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(horizontal = 10.dp))
        TextButton(onClick = onDismiss, enabled = !busy) { Text(stringResource(R.string.no), color = Vela.Muted) }
        TextButton(onClick = onAdd, enabled = !busy) {
            if (busy) CircularProgressIndicator(Modifier.size(14.dp), color = Vela.Star, strokeWidth = 1.5.dp)
            else Text(stringResource(R.string.add), color = Vela.Star, fontWeight = FontWeight.SemiBold)
        }
    }
}

@Composable
fun AddDialog(initial: String, onDismiss: () -> Unit, onSubmit: (String, (String?) -> Unit) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var text by rememberSaveable { mutableStateOf(initial) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val noQr = stringResource(R.string.add_no_qr)
    val clipEmpty = stringResource(R.string.add_clipboard_empty)
    val scanPrompt = stringResource(R.string.add_scan_prompt)

    fun append(found: String) {
        text = if (text.isBlank()) found else text.trimEnd() + "\n" + found
        error = null
    }
    val scanner = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.takeIf { it.isNotBlank() }?.let { append(it.trim()) }
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        if (uri != null) {
            scope.launch {
                val found = runCatching { decodeQr(context, uri) }.getOrNull()
                if (found.isNullOrBlank()) error = noQr else append(found.trim())
            }
        }
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        containerColor = Vela.Deep,
        title = { Text(stringResource(R.string.add_title)) },
        text = {
            Column {
                Text(stringResource(R.string.add_hint), color = Vela.Muted, fontSize = 13.sp)
                OutlinedTextField(
                    value = text, onValueChange = { text = it; error = null },
                    placeholder = { Text("https://… / vless://…", color = Vela.Muted) },
                    textStyle = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.5.sp),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                    isError = error != null,
                    colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Vela.Star, unfocusedBorderColor = Vela.Line, cursorColor = Vela.Star),
                    modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp).padding(top = 12.dp),
                )
                error?.let { Text(it, color = Vela.Danger, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.Start) {
                    TextButton(onClick = {
                        val pasted = runCatching {
                            val clip = context.getSystemService(ClipboardManager::class.java)?.primaryClip
                            clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
                        }.getOrNull()
                        if (pasted.isNullOrBlank()) error = clipEmpty else text = pasted.trim()
                    }) { Text(stringResource(R.string.add_from_clipboard), color = Vela.Star) }
                }
                Row {
                    TextButton(onClick = {
                        runCatching {
                            scanner.launch(
                                ScanOptions()
                                    .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                                    .setPrompt(scanPrompt)
                                    .setBeepEnabled(false)
                                    .setOrientationLocked(false),
                            )
                        }.onFailure { error = noQr }
                    }) { Text(stringResource(R.string.add_scan_qr), color = Vela.Star) }
                    // some devices (and work profiles) have no picture picker
                    TextButton(onClick = { runCatching { picker.launch("image/*") }.onFailure { error = noQr } }) {
                        Text(stringResource(R.string.add_qr_photo), color = Vela.Star)
                    }
                }
            }
        },
        confirmButton = {
            Button(
                enabled = !busy && text.isNotBlank(),
                onClick = {
                    busy = true
                    onSubmit(text) { err -> busy = false; error = err }
                },
            ) { Text(stringResource(if (busy) R.string.loading_short else R.string.add)) }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text(stringResource(R.string.cancel), color = Vela.Muted) } },
    )
}
