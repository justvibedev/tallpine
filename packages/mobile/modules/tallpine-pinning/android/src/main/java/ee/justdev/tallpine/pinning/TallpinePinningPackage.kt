package ee.justdev.tallpine.pinning

import android.app.Application
import android.content.Context
import com.facebook.react.modules.network.OkHttpClientProvider
import expo.modules.core.interfaces.ApplicationLifecycleListener
import expo.modules.core.interfaces.Package

/** Swaps in the pinning client before React Native creates its first one. */
class TallpinePinningPackage : Package {
  override fun createApplicationLifecycleListeners(context: Context): List<ApplicationLifecycleListener> =
    listOf(object : ApplicationLifecycleListener {
      override fun onCreate(application: Application) {
        OkHttpClientProvider.setOkHttpClientFactory(PinnedOkHttpClientFactory(application))
      }
    })
}
