package app.vela.client.data

import android.content.Context
import app.vela.velacore.Velacore
import android.net.Uri
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.security.SecureRandom

data class Node(
    val id: String,
    val name: String,
    val protocol: String,
    val server: String,
    val port: Int,
    val security: String,
    val network: String,
    /** Full node object as produced by the Go core (kept verbatim). */
    val raw: JSONObject,
) {
    val isUdp: Boolean get() = protocol == "Hysteria2" || protocol == "TUIC"

    companion object {
        fun from(o: JSONObject) = Node(
            id = o.optString("id"),
            name = o.optString("name"),
            protocol = o.optString("protocol"),
            server = o.optString("server"),
            port = o.optInt("port"),
            security = o.optString("security"),
            network = o.optString("network"),
            raw = o,
        )
    }
}

data class Profile(
    val id: String,
    val name: String,
    val kind: String, // "subscription" | "manual"
    val title: String = "",
    val customName: String = "",
    val url: String = "",
    val nodes: List<Node> = emptyList(),
    val upload: Long = 0,
    val download: Long = 0,
    val total: Long = 0,
    val expire: Long = 0,
    val intervalHours: Int = 0,
    val supportUrl: String = "",
    val updatedAt: Long = 0,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("name", name).put("kind", kind).put("url", url)
        .put("title", title).put("customName", customName)
        .put("nodes", JSONArray().apply { nodes.forEach { put(it.raw) } })
        .put("upload", upload).put("download", download).put("total", total)
        .put("expire", expire).put("updateIntervalHours", intervalHours)
        .put("supportUrl", supportUrl).put("updatedAt", updatedAt)

    companion object {
        fun from(o: JSONObject) = Profile(
            id = o.optString("id"),
            name = o.optString("name"),
            kind = o.optString("kind"),
            title = o.optString("title"),
            customName = o.optString("customName"),
            url = o.optString("url"),
            nodes = o.optJSONArray("nodes").toNodes(),
            upload = o.optLong("upload"),
            download = o.optLong("download"),
            total = o.optLong("total"),
            expire = o.optLong("expire"),
            intervalHours = o.optInt("updateIntervalHours"),
            supportUrl = o.optString("supportUrl"),
            updatedAt = o.optLong("updatedAt"),
        )
    }
}

private fun JSONArray?.toNodes(): List<Node> {
    if (this == null) return emptyList()
    // the same server twice breaks lists (duplicate keys) and the config
    return (0 until length()).map { Node.from(getJSONObject(it)) }.distinctBy { it.id }
}

/** User routing rule: domain, *keyword* or IP/CIDR → direct, proxy or block. */
data class Rule(val match: String, val action: String = "direct") {
    fun toJson(): JSONObject = JSONObject().put("match", match).put("action", action)
}

data class Settings(
    val routing: String = "smart",
    val blockAds: Boolean = false,
    val ipv6: Boolean = true,
    val stack: String = "mixed",
    val remoteDns: String = "https://1.1.1.1/dns-query",
    val directDns: String = "77.88.8.8",
    val directDomains: List<String> = emptyList(),
    val proxyDomains: List<String> = emptyList(),
    /** Switch to a working server when the chosen one stops answering. */
    val disableFailover: Boolean = false,
    /** "" off, "record" light, "full" strongest TLS handshake splitting. */
    val tlsFragment: String = "",
    val rules: List<Rule> = emptyList(),
    /** "" all apps, "exclude" listed apps bypass the VPN, "only" only listed apps use it. */
    val appMode: String = "",
    val apps: List<String> = emptyList(),
) {
    fun toJson(): JSONObject = JSONObject()
        .put("mode", "tun").put("routing", routing).put("blockAds", blockAds)
        .put("ipv6", ipv6).put("stack", stack).put("remoteDns", remoteDns)
        .put("directDns", directDns)
        .put("directDomains", JSONArray(directDomains))
        .put("proxyDomains", JSONArray(proxyDomains))
        .put("logLevel", "info")
        .put("disableFailover", disableFailover)
        .put("tlsFragment", tlsFragment)
        .put("rules", JSONArray().apply { rules.forEach { put(it.toJson()) } })
        .put("appMode", appMode)
        .put("apps", JSONArray(apps))

    companion object {
        fun from(o: JSONObject?): Settings {
            if (o == null) return Settings()
            return Settings(
                routing = o.optString("routing", "smart"),
                blockAds = o.optBoolean("blockAds"),
                ipv6 = o.optBoolean("ipv6", true),
                stack = o.optString("stack", "mixed"),
                remoteDns = o.optString("remoteDns", "https://1.1.1.1/dns-query"),
                directDns = o.optString("directDns", "77.88.8.8"),
                directDomains = o.optJSONArray("directDomains").strings(),
                proxyDomains = o.optJSONArray("proxyDomains").strings(),
                disableFailover = o.optBoolean("disableFailover"),
                tlsFragment = o.optString("tlsFragment"),
                rules = o.optJSONArray("rules")?.let { arr ->
                    (0 until arr.length()).mapNotNull { i ->
                        arr.optJSONObject(i)?.let { r -> Rule(r.optString("match"), r.optString("action", "direct")) }
                    }.filter { it.match.isNotBlank() }
                } ?: emptyList(),
                appMode = o.optString("appMode"),
                apps = o.optJSONArray("apps").strings(),
            )
        }
    }
}

private fun JSONArray?.strings(): List<String> =
    if (this == null) emptyList() else (0 until length()).map { getString(it) }

data class AppState(
    val profiles: List<Profile> = emptyList(),
    val activeProfile: String = "",
    val selectedNode: String = "auto",
    val settings: Settings = Settings(),
    val wasConnected: Boolean = false,
    /** Favorite server ids; automatic selection prefers them. */
    val favorites: Set<String> = emptySet(),
    /** "system", "light" or "dark". */
    val theme: String = "system",
) {
    val active: Profile? get() = profiles.firstOrNull { it.id == activeProfile } ?: profiles.firstOrNull()
}

/** Holds the app state, persisted as JSON in the app's private storage. */
object Store {
    private lateinit var file: File
    lateinit var dataDir: File
        private set

    private val _state = MutableStateFlow(AppState())
    val state: StateFlow<AppState> = _state.asStateFlow()

    /** Latency in ms per node id: -1 unreachable, -2 measured only when connected, 0 measuring. */
    val pings = MutableStateFlow<Map<String, Int>>(emptyMap())

    /** Why a server failed the last check (Russian, human readable). */
    val reasons = MutableStateFlow<Map<String, String>>(emptyMap())

    const val MANUAL_ID = "manual"

    fun init(context: Context) {
        dataDir = context.filesDir
        file = File(dataDir, "state.json")
        _state.value = load()
        loadPings()
    }

    /** Core log of the current connection (shown in Settings). */
    val logFile: File get() = File(dataDir, "core.log")

    private fun load(): AppState = runCatching {
        val o = JSONObject(file.readText())
        AppState(
            profiles = o.optJSONArray("profiles")?.let { arr ->
                (0 until arr.length()).map { Profile.from(arr.getJSONObject(it)) }
            } ?: emptyList(),
            activeProfile = o.optString("activeProfile"),
            selectedNode = o.optString("selectedNode", "auto"),
            settings = Settings.from(o.optJSONObject("settings")),
            wasConnected = o.optBoolean("wasConnected"),
            favorites = o.optJSONArray("favorites").strings().toSet(),
            theme = o.optString("theme", "system"),
        )
    }.getOrDefault(AppState())

    private fun loadPings() {
        runCatching {
            val o = JSONObject(File(dataDir, "pings.json").readText())
            pings.value = o.keys().asSequence().associateWith { o.getInt(it) }.filterValues { it != 0 }
        }
    }

    /** Remembers check results so automatic selection can skip dead servers after a restart. */
    fun savePings() {
        runCatching {
            val o = JSONObject()
            pings.value.filterValues { it != 0 }.forEach { (k, v) -> o.put(k, v) }
            File(dataDir, "pings.json").writeText(o.toString())
        }
    }

    @Synchronized
    private fun update(block: (AppState) -> AppState) {
        val next = block(_state.value)
        _state.value = next
        val o = JSONObject()
            .put("profiles", JSONArray().apply { next.profiles.forEach { put(it.toJson()) } })
            .put("activeProfile", next.activeProfile)
            .put("selectedNode", next.selectedNode)
            .put("settings", next.settings.toJson())
            .put("wasConnected", next.wasConnected)
            .put("favorites", JSONArray(next.favorites.toList()))
            .put("theme", next.theme)
        val tmp = File(file.parentFile, "state.json.tmp")
        tmp.writeText(o.toString())
        tmp.renameTo(file)
    }

    /**
     * Imports any mix of subscription URLs, share links and base64 bodies.
     * Several subscriptions can be pasted at once, one per line.
     */
    suspend fun import(text: String): String = withContext(Dispatchers.IO) {
        val input = text.trim()
        require(input.isNotEmpty()) { "Вставьте ссылку на подписку или сервер" }
        val backup = runCatching { Velacore.decodeBackup(input) }.getOrElse { error(it.message ?: "Резервная копия повреждена") }
        if (backup.isNotEmpty()) return@withContext restore(JSONObject(backup))
        val split = JSONObject(Velacore.splitImport(input))
        val urls = split.optJSONArray("subscriptions").strings()
        val linksText = split.optString("links")
        val parts = mutableListOf<String>()
        val failures = mutableListOf<String>()
        var lastProfile: String? = null

        val fetched = coroutineScope {
            urls.map { url -> async { url to runCatching { JSONObject(Velacore.fetchSubscription(url)) } } }.awaitAll()
        }
        for ((url, result) in fetched) {
            val sub = result.getOrElse {
                failures += (Uri.parse(url).host ?: url) + ": " + (it.message ?: "ошибка")
                null
            } ?: continue
            val nodes = sub.optJSONArray("nodes").toNodes()
            var label = ""
            var isNew = false
            update { st ->
                val existing = st.profiles.firstOrNull { it.kind == "subscription" && it.url == url }
                isNew = existing == null
                var profile = subscriptionProfile(existing?.id ?: randomId(), url, sub, nodes, existing?.customName ?: "")
                profile = profile.copy(name = uniqueName(st.profiles, profile))
                label = profile.name
                lastProfile = profile.id
                val profiles = if (existing != null) st.profiles.map { if (it.id == existing.id) profile else it } else st.profiles + profile
                st.copy(profiles = profiles)
            }
            parts += if (isNew) "«$label»: серверов ${nodes.size}" else "«$label» обновлена"
        }

        if (linksText.isNotBlank()) {
            val parsed = JSONObject(Velacore.parseText(linksText))
            val nodes = parsed.optJSONArray("nodes").toNodes()
            val errors = parsed.optJSONArray("errors").strings()
            if (nodes.isEmpty()) {
                failures += errors.firstOrNull() ?: "не нашли поддерживаемых ссылок"
            } else {
                var added = 0
                update { st ->
                    val manual = st.profiles.firstOrNull { it.id == MANUAL_ID } ?: Profile(MANUAL_ID, "Мои серверы", "manual")
                    val known = manual.nodes.map { it.raw.optString("link") }.toSet()
                    val fresh = nodes.filter { it.raw.optString("link") !in known && manual.nodes.none { m -> m.id == it.id } }
                    added = fresh.size
                    val updated = manual.copy(nodes = manual.nodes + fresh)
                    val profiles = if (st.profiles.any { it.id == MANUAL_ID }) {
                        st.profiles.map { if (it.id == MANUAL_ID) updated else it }
                    } else {
                        listOf(updated) + st.profiles
                    }
                    st.copy(profiles = profiles)
                }
                lastProfile = MANUAL_ID
                parts += if (added == 0) "эти серверы уже есть в списке" else "серверов добавлено: $added"
                if (errors.isNotEmpty()) parts += "пропущено ссылок: ${errors.size}"
            }
        }

        val target = lastProfile ?: error(failures.joinToString("; ").ifEmpty { "Не нашли поддерживаемых ссылок" })
        update { st ->
            // the first import becomes active; later ones are added alongside
            if (st.activeProfile.isEmpty() || st.profiles.none { it.id == st.activeProfile }) {
                st.copy(activeProfile = target, selectedNode = "auto")
            } else {
                st
            }
        }
        var msg = parts.joinToString(", ")
        if (failures.isNotEmpty()) msg += ". Не удалось: " + failures.joinToString("; ")
        msg.replaceFirstChar { it.uppercase() }
    }

    /** Keeps profile names unique so tabs can be told apart. */
    private fun uniqueName(all: List<Profile>, p: Profile): String {
        if (p.customName.isNotBlank()) return p.customName
        val base = p.name.ifBlank { Uri.parse(p.url).host ?: "Подписка" }
        var name = base
        var n = 2
        while (all.any { it.id != p.id && it.name == name }) {
            name = "$base $n"
            n++
        }
        return name
    }

    fun rename(profileId: String, name: String) = update { st ->
        st.copy(
            profiles = st.profiles.map { p ->
                if (p.id != profileId) {
                    p
                } else {
                    val custom = name.trim()
                    val renamed = p.copy(customName = custom, name = custom.ifEmpty { p.title.ifEmpty { p.name } })
                    renamed.copy(name = uniqueName(st.profiles, renamed))
                }
            },
        )
    }

    /** Refreshes all subscriptions. Returns a message for the user. */
    suspend fun refreshAll(): String = coroutineScope {
        val ids = state.value.profiles.filter { it.kind == "subscription" }.map { it.id }
        if (ids.isEmpty()) error("Подписок пока нет")
        val failed = ids.map { id -> async { runCatching { refresh(id) }.isFailure } }.awaitAll().count { it }
        when {
            failed == ids.size -> error("Не удалось обновить подписки, проверьте интернет")
            failed > 0 -> "Обновлено ${ids.size - failed} из ${ids.size} подписок"
            else -> "Все подписки обновлены"
        }
    }

    suspend fun refresh(profileId: String) = withContext(Dispatchers.IO) {
        val p = _state.value.profiles.firstOrNull { it.id == profileId && it.kind == "subscription" }
            ?: error("Подписка не найдена")
        val sub = JSONObject(Velacore.fetchSubscription(p.url))
        val nodes = sub.optJSONArray("nodes").toNodes()
        update { st ->
            st.copy(
                profiles = st.profiles.map {
                    if (it.id == p.id) {
                        val fresh = subscriptionProfile(p.id, p.url, sub, nodes, p.customName)
                        fresh.copy(name = uniqueName(st.profiles, fresh))
                    } else {
                        it
                    }
                },
            )
        }
    }

    /** Refreshes subscriptions whose update interval has passed. */
    suspend fun refreshDue() {
        val now = System.currentTimeMillis() / 1000
        for (p in _state.value.profiles.filter { it.kind == "subscription" }) {
            val interval = if (p.intervalHours > 0) p.intervalHours else 12
            if (now - p.updatedAt > interval * 3600L) runCatching { refresh(p.id) }
        }
    }

    private fun subscriptionProfile(id: String, url: String, sub: JSONObject, nodes: List<Node>, customName: String = "") = Profile(
        id = id, name = customName.ifBlank { sub.optString("title").ifBlank { Uri.parse(url).host ?: url } },
        kind = "subscription", url = url, title = sub.optString("title"), customName = customName,
        nodes = nodes, upload = sub.optLong("upload"), download = sub.optLong("download"),
        total = sub.optLong("total"), expire = sub.optLong("expire"),
        intervalHours = sub.optInt("updateIntervalHours"),
        supportUrl = sub.optString("supportUrl").ifBlank { sub.optString("webPageUrl") },
        updatedAt = sub.optLong("updatedAt"),
    )

    fun deleteProfile(id: String) = update { st ->
        val rest = st.profiles.filterNot { it.id == id }
        if (st.activeProfile == id) {
            st.copy(profiles = rest, activeProfile = rest.firstOrNull()?.id ?: "", selectedNode = "auto")
        } else {
            st.copy(profiles = rest)
        }
    }

    fun deleteNode(profileId: String, nodeId: String) = update { st ->
        st.copy(
            profiles = st.profiles.map { p -> if (p.id == profileId) p.copy(nodes = p.nodes.filterNot { it.id == nodeId }) else p },
            selectedNode = if (st.selectedNode == nodeId) "auto" else st.selectedNode,
        )
    }

    fun select(profileId: String, nodeId: String) = update { it.copy(activeProfile = profileId, selectedNode = nodeId) }

    fun setSettings(settings: Settings) = update { it.copy(settings = settings) }

    fun setFavorite(nodeId: String, on: Boolean) = update {
        it.copy(favorites = if (on) it.favorites + nodeId else it.favorites - nodeId)
    }

    fun setTheme(theme: String) = update { it.copy(theme = theme) }

    // ------------------------------------------------------------ backup

    /** Everything needed to set Vela up on another device, as text for a QR code. */
    fun exportBackup(): String {
        val st = _state.value
        val subs = JSONArray()
        st.profiles.filter { it.kind == "subscription" }.forEach {
            subs.put(JSONObject().put("url", it.url).put("name", it.customName))
        }
        val links = JSONArray()
        st.profiles.firstOrNull { it.id == MANUAL_ID }?.nodes?.forEach { n ->
            n.raw.optString("link").takeIf { it.isNotBlank() }?.let { links.put(it) }
        }
        val favs = JSONArray()
        st.profiles.flatMap { it.nodes }.filter { it.id in st.favorites }.forEach { n ->
            n.raw.optString("link").takeIf { it.isNotBlank() }?.let { favs.put(it) }
        }
        val b = JSONObject()
            .put("subscriptions", subs).put("links", links).put("favorites", favs)
            .put("settings", st.settings.toJson())
            .put("extra", JSONObject().put("theme", st.theme).put("platform", "android"))
        return Velacore.encodeBackup(b.toString())
    }

    private suspend fun restore(b: JSONObject): String {
        val lines = mutableListOf<String>()
        val subs = b.optJSONArray("subscriptions")
        if (subs != null) for (i in 0 until subs.length()) subs.optJSONObject(i)?.optString("url")?.let { if (it.isNotBlank()) lines += it }
        lines += b.optJSONArray("links").strings()
        val msg = if (lines.isNotEmpty()) import(lines.joinToString("\n")) else ""
        if (subs != null) {
            for (i in 0 until subs.length()) {
                val o = subs.optJSONObject(i) ?: continue
                val name = o.optString("name")
                if (name.isBlank()) continue
                _state.value.profiles.firstOrNull { it.kind == "subscription" && it.url == o.optString("url") }?.let { rename(it.id, name) }
            }
        }
        val favLinks = b.optJSONArray("favorites").strings().toSet()
        val extra = b.optJSONObject("extra")
        update { st ->
            var settings = st.settings
            b.optJSONObject("settings")?.let { o ->
                val incoming = Settings.from(o)
                // app lists only make sense on the same kind of device
                settings = if (extra?.optString("platform") == "android") incoming
                else incoming.copy(appMode = st.settings.appMode, apps = st.settings.apps)
            }
            val favIds = st.profiles.flatMap { it.nodes }.filter { it.raw.optString("link") in favLinks }.map { it.id }
            st.copy(
                settings = settings,
                favorites = st.favorites + favIds,
                theme = extra?.optString("theme")?.takeIf { it in setOf("system", "light", "dark") } ?: st.theme,
            )
        }
        return if (msg.isEmpty()) "Настройки восстановлены" else "Восстановлено. $msg"
    }

    fun setWasConnected(value: Boolean) = update { it.copy(wasConnected = value) }

    /** Builds the sing-box configuration for the active profile. */
    fun buildConfig(controllerPort: Int, controllerSecret: String): String {
        val st = _state.value
        val profile = st.active ?: error("Сначала добавьте сервер или подписку")
        if (profile.nodes.isEmpty()) error("В подписке нет серверов")
        val req = JSONObject()
            .put("platform", "android")
            .put("nodes", JSONArray().apply { profile.nodes.forEach { put(it.raw) } })
            .put("selected", if (profile.id == st.activeProfile) st.selectedNode else "auto")
            .put("settings", st.settings.toJson())
            .put("dataDir", dataDir.absolutePath)
            .put("controllerPort", controllerPort)
            .put("controllerSecret", controllerSecret)
            .put("logPath", logFile.absolutePath)
        // automatic selection: favorites first, then the fastest, never the dead ones
        val ids = profile.nodes.map { it.id }.toSet()
        val p = pings.value
        req.put("favorites", JSONArray(st.favorites.filter { it in ids }))
        req.put("ranked", JSONArray(p.filter { it.key in ids && it.value > 0 }.entries.sortedBy { it.value }.map { it.key }))
        req.put("failed", JSONArray(p.filter { it.key in ids && it.value < 0 }.keys.toList()))
        return Velacore.buildConfig(req.toString())
    }

    fun randomId(bytes: Int = 8): String {
        val b = ByteArray(bytes)
        SecureRandom().nextBytes(b)
        return b.joinToString("") { "%02x".format(it) }
    }
}
