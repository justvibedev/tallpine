package specgen

// The Connect Mobile status shapes live in mobilebridge/service, next to the
// bridge that fills them in. These entries keep their published schema names
// exactly as they were when the types lived in httpd/controllers.
func init() {
	schemaNames["ServiceMobileStatusResponse"] = "MobileStatusResponse"
	schemaNames["ServiceKeepAwakeStatus"] = "ControllersKeepAwakeStatus"
	schemaNames["ServiceSecurePairingStatus"] = "ControllersSecurePairingStatus"
}
