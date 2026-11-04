import ExpoModulesCore

public class TallpinePinningModule: Module {
  public func definition() -> ModuleDefinition {
    Name("TallpinePinning")

    // Synchronous: the pins must be in place before the request that follows.
    Function("setPins") { (entries: [String]) in
      TLPinStore.addEntries(entries)
    }

    AsyncFunction("probe") { (host: String, port: Int, promise: Promise) in
      TLTLSProbe.probeHost(host, port: port) { pin, trusted, error in
        if let error = error {
          promise.reject("E_PROBE", error.localizedDescription)
        } else {
          promise.resolve(["pin": pin ?? "", "trusted": trusted])
        }
      }
    }
  }
}
