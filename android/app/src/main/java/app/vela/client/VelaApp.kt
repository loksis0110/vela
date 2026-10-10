package app.vela.client

import android.app.Application
import android.content.res.Configuration
import android.os.Build
import app.vela.client.notify.Reminders
import app.vela.client.data.Store
import app.vela.client.vpn.VpnManager
import app.vela.client.widget.VelaWidget
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import app.vela.client.vpn.DefaultNetworkMonitor
import app.vela.libbox.Libbox
import app.vela.libbox.SetupOptions
import java.io.File

class VelaApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Lang.init(this)
        Store.init(this)
        DefaultNetworkMonitor.init(this)
        val work = File(filesDir, "core").apply { mkdirs() }
        Libbox.setup(
            SetupOptions().also {
                it.basePath = filesDir.path
                it.workingPath = work.path
                it.tempPath = cacheDir.path
                // https://github.com/golang/go/issues/68760
                it.fixAndroidStack = Build.VERSION.SDK_INT in 24..25 || Build.VERSION.SDK_INT >= 28
                it.logMaxLines = 1000
                it.debug = BuildConfig.DEBUG
                it.appVersion = BuildConfig.VERSION_CODE.toString()
                it.appMarketingVersion = BuildConfig.VERSION_NAME
            },
        )
        Lang.applyToCore()
        Reminders.ensureChannel(this)
        Reminders.onWake(this)
        // keep the home screen widget in sync with the connection
        CoroutineScope(SupervisorJob() + Dispatchers.Main).launch {
            combine(VpnManager.status, Store.state) { s, st -> Triple(s.state, s.activeNode, st.active?.id to st.selectedNode) }
                .distinctUntilChanged()
                .collect { runCatching { VelaWidget.updateAll(this@VelaApp) } }
        }
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        // the system language may have changed: texts of notifications, widget, core
        Lang.onConfigurationChanged()
        Reminders.ensureChannel(this)
        runCatching { VelaWidget.updateAll(this) }
    }
}
