package httpd

import "time"

// farFuture keeps a test's legacy bearer valid for the whole run.
var farFuture = time.Now().Add(24 * time.Hour)

// armTestBearer makes hash both the pairing hash and a valid legacy bearer on
// st, which is what the pre-device-token tests meant by "the password".
func armTestBearer(st *authState, hash string) {
	st.setHash(hash)
	st.legacy.Store(&legacyCred{hash: hash, until: farFuture})
}

// armTestBearerLAN is armTestBearer for a LANManager; replacing a bearer closes
// the connections that used the old one, like the rotation these tests cover.
func armTestBearerLAN(m *LANManager, hash string) {
	m.SetPasswordHash(hash)
	m.SetLegacyPassword(hash, farFuture)
}
