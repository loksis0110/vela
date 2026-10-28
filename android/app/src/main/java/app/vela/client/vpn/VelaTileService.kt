package app.vela.client.vpn

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import app.vela.client.Lang
import app.vela.client.R
import app.vela.client.data.Store
import app.vela.client.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/** Quick Settings tile: connect / disconnect from the notification shade. */
class VelaTileService : TileService() {
    private var job: Job? = null

    override fun attachBaseContext(base: Context) {
        super.attachBaseContext(Lang.wrap(base))
    }

    override fun onStartListening() {
        job?.cancel()
        job = CoroutineScope(Dispatchers.Main).launch {
            // the status changes every second (traffic): redraw only on a new state
            VpnManager.status.map { it.state }.distinctUntilChanged().collect { render(it) }
        }
    }

    override fun onStopListening() {
        job?.cancel()
        job = null
    }

    private fun render(state: ConnState) {
        val tile = qsTile ?: return
        tile.state = when (state) {
            ConnState.Connected, ConnState.Connecting -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        if (Build.VERSION.SDK_INT >= 29) {
            tile.subtitle = when (state) {
                ConnState.Connected -> Lang.str(R.string.notif_connected)
                ConnState.Connecting -> Lang.str(R.string.notif_connecting)
                else -> Lang.str(R.string.tile_off)
            }
        }
        tile.updateTile()
    }

    override fun onClick() {
        when (VpnManager.status.value.state) {
            ConnState.Connected, ConnState.Connecting -> VpnManager.stop(this)
            ConnState.Disconnecting -> {}
            ConnState.Disconnected -> {
                val noServers = Store.state.value.active?.nodes.isNullOrEmpty()
                if (noServers || runCatching { VpnManager.prepare(this) }.getOrNull() != null) {
                    // nothing to connect to yet, or VPN permission must be granted in
                    // the app once: open it (it connects right away when it can)
                    openApp(if (noServers) null else MainActivity.ACTION_CONNECT)
                } else {
                    VpnManager.start(this)
                }
            }
        }
    }

    private fun openApp(action: String?) {
        val intent = Intent(this, MainActivity::class.java).setAction(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching {
            if (Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(
                    PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT),
                )
            } else {
                @Suppress("DEPRECATION")
                startActivityAndCollapse(intent)
            }
        }
    }
}
