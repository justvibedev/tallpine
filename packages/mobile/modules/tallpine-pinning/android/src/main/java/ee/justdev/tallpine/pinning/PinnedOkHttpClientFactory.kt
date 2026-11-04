package ee.justdev.tallpine.pinning

import android.content.Context
import android.util.Log
import com.facebook.react.modules.network.OkHttpClientFactory
import com.facebook.react.modules.network.OkHttpClientProvider
import okhttp3.OkHttpClient
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext

/**
 * React Native's OkHttp client carries both fetch and WebSocket, so this one
 * trust decision pins both. Hosts without a pin keep system CA validation.
 */
class PinnedOkHttpClientFactory(private val context: Context) : OkHttpClientFactory {
  override fun createNewNetworkModuleClient(): OkHttpClient {
    Log.i("TallpinePinning", "building pinned OkHttp client")
    val trust = PinningTrustManager(PinningTrustManager.systemTrustManager())
    val ssl = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }
    return OkHttpClientProvider.createClientBuilder(context)
      .sslSocketFactory(ssl.socketFactory, trust)
      .hostnameVerifier(PinnedHostnameVerifier(HttpsURLConnection.getDefaultHostnameVerifier()))
      .build()
  }
}
