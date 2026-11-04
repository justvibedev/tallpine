# react-native-webview (patched, see patches/) reaches the pin store reflectively
# when a WebView reports an SSL error; R8 must not rename or drop it.
-keep class ee.justdev.tallpine.pinning.PinStore { *; }
