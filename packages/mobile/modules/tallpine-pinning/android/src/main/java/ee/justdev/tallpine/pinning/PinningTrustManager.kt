package ee.justdev.tallpine.pinning

import android.util.Log
import java.net.Socket
import java.security.KeyStore
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509ExtendedTrustManager
import javax.net.ssl.X509TrustManager

/**
 * Accepts a pinned host:port only if its key matches the pin, and defers every
 * other host to the system trust manager. The host and port come from the
 * socket or engine, which is why this is the Extended variant.
 */
class PinningTrustManager(private val system: X509ExtendedTrustManager) : X509ExtendedTrustManager() {
  private fun check(chain: Array<X509Certificate>, authType: String, host: String?, port: Int, fallback: () -> Unit) {
    val pinned = host != null && PinStore.pinFor(host, port) != null
    if (pinned) Log.i("TallpinePinning", "server trust $host:$port got=${chain.firstOrNull()?.let { PinStore.spkiPin(it).take(12) }}")
    if (host != null && pinned) {
      if (!PinStore.matches(chain, host, port)) throw CertificateException("certificate does not match the pinned key for $host:$port")
      return
    }
    fallback()
  }

  override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String, socket: Socket) =
    check(chain, authType, requestedHost(socket), socket.port) {
      system.checkServerTrusted(chain, authType, socket)
    }

  override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String, engine: SSLEngine) =
    check(chain, authType, engine.peerHost, engine.peerPort) { system.checkServerTrusted(chain, authType, engine) }

  override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) = system.checkServerTrusted(chain, authType)
  override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String, socket: Socket) = system.checkClientTrusted(chain, authType, socket)
  override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String, engine: SSLEngine) = system.checkClientTrusted(chain, authType, engine)
  override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = system.checkClientTrusted(chain, authType)
  override fun getAcceptedIssuers(): Array<X509Certificate> = system.acceptedIssuers

  /** The host OkHttp asked for; never getHostName(), which reverse-resolves an IP literal. */
  private fun requestedHost(socket: Socket): String? =
    (socket as? SSLSocket)?.handshakeSession?.peerHost ?: socket.inetAddress?.hostAddress

  companion object {
    fun systemTrustManager(): X509ExtendedTrustManager {
      val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
      factory.init(null as KeyStore?)
      return factory.trustManagers.filterIsInstance<X509ExtendedTrustManager>().first()
    }
  }
}
