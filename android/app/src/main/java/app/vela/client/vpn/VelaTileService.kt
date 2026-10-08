package app.vela.client.vpn

import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import app.vela.client.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** Quick Settings tile: connect / disconnect from the notification shade. */
class VelaTileService : TileService() {
    private var job: Job? = null

    override fun onStartListening() {
        job = CoroutineScope(Dispatchers.Main).launch {
            VpnManager.status.collect { render(it.state) }
        }
    }

    override fun onStopListening() {
        job?.cancel()
    }

    private fun render(state: ConnState) {
        val tile = qsTile ?: return
        tile.state = when (state) {
            ConnState.Connected, ConnState.Connecting -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        if (Build.VERSION.SDK_INT >= 29) {
            tile.subtitle = when (state) {
                ConnState.Connected -> "Подключено"
                ConnState.Connecting -> "Подключение…"
                else -> "Выключено"
            }
        }
        tile.updateTile()
    }

    override fun onClick() {
        when (VpnManager.status.value.state) {
            ConnState.Connected, ConnState.Connecting -> VpnManager.stop(this)
            else -> {
                if (VpnManager.prepare(this) != null) {
                    // VPN permission must be granted from the app once
                    val intent = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    if (Build.VERSION.SDK_INT >= 34) {
                        startActivityAndCollapse(
                            android.app.PendingIntent.getActivity(this, 0, intent, android.app.PendingIntent.FLAG_IMMUTABLE),
                        )
                    } else {
                        @Suppress("DEPRECATION")
                        startActivityAndCollapse(intent)
                    }
                } else {
                    VpnManager.start(this)
                }
            }
        }
    }
}
