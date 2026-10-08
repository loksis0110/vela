package app.vela.client

import android.app.Application
import android.os.Build
import app.vela.client.data.Store
import app.vela.client.vpn.DefaultNetworkMonitor
import app.vela.libbox.Libbox
import app.vela.libbox.SetupOptions
import java.io.File

class VelaApp : Application() {
    override fun onCreate() {
        super.onCreate()
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
    }
}
