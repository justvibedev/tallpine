#import "TLPinnedHTTPRequestHandler.h"
#import "TLPinStore.h"

@implementation TLPinnedHTTPRequestHandler

RCT_EXPORT_MODULE()

- (float)handlerPriority {
  return 10; // above the stock handler (0) for the requests we claim
}

- (BOOL)canHandleRequest:(NSURLRequest *)request {
  NSURL *url = request.URL;
  if (![url.scheme.lowercaseString isEqualToString:@"https"] || url.host == nil) return NO;
  NSInteger port = url.port != nil ? url.port.integerValue : 443;
  BOOL claim = [TLPinStore pinForHost:url.host port:port] != nil;
  if (claim) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{ NSLog(@"[TallpinePinning] pinned handler claims https %@:%ld", url.host, (long)port); });
  }
  return claim;
}

- (void)URLSession:(NSURLSession *)session
    didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge
      completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  NSURLProtectionSpace *space = challenge.protectionSpace;
  if (![space.authenticationMethod isEqualToString:NSURLAuthenticationMethodServerTrust] || space.serverTrust == NULL) {
    completionHandler(NSURLSessionAuthChallengePerformDefaultHandling, nil);
    return;
  }
  BOOL match = [TLPinStore trust:space.serverTrust matchesPinForHost:space.host port:space.port];
  // Pin challenges are rare (one per new connection) and the only way to see why a pin was refused on a device.
  NSString *got = [TLPinStore spkiPinForTrust:space.serverTrust];
  NSLog(@"[TallpinePinning] challenge %@:%ld pin match=%d got=%@", space.host, (long)space.port, match, got.length >= 12 ? [got substringToIndex:12] : @"(unsupported key)");
  if (match) {
    completionHandler(NSURLSessionAuthChallengeUseCredential, [NSURLCredential credentialForTrust:space.serverTrust]);
  } else {
    completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, nil);
  }
}

@end
