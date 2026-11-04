package ee.justdev.tallpine.pinning

import expo.modules.kotlin.Promise
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

class TallpinePinningModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("TallpinePinning")

    // Synchronous: the pins must be in place before the request that follows.
    Function("setPins") { entries: List<String> -> PinStore.addEntries(entries) }

    AsyncFunction("probe") { host: String, port: Int, promise: Promise ->
      Thread {
        try {
          var seen: Array<X509Certificate> = emptyArray()
          val recorder = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {}
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) { seen = chain }
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
          }
          val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(recorder), null) }
          val socket = ctx.socketFactory.createSocket() as javax.net.ssl.SSLSocket
          socket.connect(java.net.InetSocketAddress(host, port), 6000)
          socket.soTimeout = 6000
          socket.startHandshake()
          val nameOk = javax.net.ssl.HttpsURLConnection.getDefaultHostnameVerifier().verify(host, socket.session)
          socket.close()
          if (seen.isEmpty()) throw IllegalStateException("no certificate presented")
          val trusted = try {
            PinningTrustManager.systemTrustManager().checkServerTrusted(seen, if (seen[0].publicKey.algorithm == "RSA") "ECDHE_RSA" else "ECDHE_ECDSA")
            nameOk
          } catch (e: Exception) { false }
          promise.resolve(mapOf("pin" to PinStore.spkiPin(seen[0]), "trusted" to trusted))
        } catch (e: Exception) {
          promise.reject("E_PROBE", e.message ?: "probe failed", e)
        }
      }.start()
    }
  }
}
