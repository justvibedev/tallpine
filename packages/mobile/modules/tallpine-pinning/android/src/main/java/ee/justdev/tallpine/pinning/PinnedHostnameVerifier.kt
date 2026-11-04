package ee.justdev.tallpine.pinning

import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLSession

/**
 * The gateway's self-signed certificate names no host (the phone reaches it by
 * LAN IP or MagicDNS name), so the usual name check can never pass. For a
 * host:port with a registered pin the key comparison in [PinningTrustManager]
 * is the authentication; every other host keeps the default name check.
 */
class PinnedHostnameVerifier(private val fallback: HostnameVerifier) : HostnameVerifier {
  override fun verify(hostname: String, session: SSLSession): Boolean =
    if (PinStore.pinFor(hostname, session.peerPort) != null) true else fallback.verify(hostname, session)
}
