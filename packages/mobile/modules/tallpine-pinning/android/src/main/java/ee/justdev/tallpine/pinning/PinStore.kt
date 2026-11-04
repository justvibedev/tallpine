package ee.justdev.tallpine.pinning

import android.util.Log
import java.security.MessageDigest
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap

/** Registry of `host:port` -> SHA-256 (hex) of the server's SubjectPublicKeyInfo. */
object PinStore {
  private val pins = ConcurrentHashMap<String, String>()
  private val entry = Regex("^(.+:[0-9]+)=([0-9a-f]{64})$")

  fun addEntries(entries: List<String>) {
    for (e in entries) {
      val m = entry.matchEntire(e) ?: continue
      pins[m.groupValues[1].lowercase()] = m.groupValues[2]
      Log.i("TallpinePinning", "registered pin for ${m.groupValues[1].lowercase()}")
    }
  }

  fun pinFor(host: String, port: Int): String? = pins["${host.lowercase()}:$port"]

  fun spkiPin(cert: X509Certificate): String =
    MessageDigest.getInstance("SHA-256").digest(cert.publicKey.encoded).joinToString("") { "%02x".format(it) }

  fun matches(chain: Array<X509Certificate>, host: String, port: Int): Boolean {
    val want = pinFor(host, port) ?: return false
    return chain.isNotEmpty() && spkiPin(chain[0]) == want
  }

  /** For WebView SSL errors (reached reflectively): true only for a pinned host:port whose leaf key matches. */
  @JvmStatic
  fun matchesCertificate(cert: X509Certificate?, host: String, port: Int): Boolean {
    val want = pinFor(host, port) ?: return false
    return cert != null && spkiPin(cert) == want
  }
}
