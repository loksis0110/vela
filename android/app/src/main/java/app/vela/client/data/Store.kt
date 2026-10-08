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
    return (0 until length()).map { Node.from(getJSONObject(it)) }
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
) {
    fun toJson(): JSONObject = JSONObject()
        .put("mode", "tun").put("routing", routing).put("blockAds", blockAds)
        .put("ipv6", ipv6).put("stack", stack).put("remoteDns", remoteDns)
        .put("directDns", directDns)
        .put("directDomains", JSONArray(directDomains))
        .put("proxyDomains", JSONArray(proxyDomains))
        .put("logLevel", "warn")

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
    }

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
        )
    }.getOrDefault(AppState())

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
                    val fresh = nodes.filter { it.raw.optString("link") !in known }
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
        return Velacore.buildConfig(req.toString())
    }

    fun randomId(bytes: Int = 8): String {
        val b = ByteArray(bytes)
        SecureRandom().nextBytes(b)
        return b.joinToString("") { "%02x".format(it) }
    }
}
