#import <React/RCTWebSocketModule.h>
#import <SocketRocket/SRSecurityPolicy.h>
#import <SocketRocket/SRWebSocket.h>
#import "TLPinStore.h"

/// Trust policy for a WebSocket to one pinned host:port: the server's key must
/// equal the pin, chain validation is replaced by that comparison.
@interface TLPinnedSecurityPolicy : SRSecurityPolicy
- (instancetype)initWithPort:(NSInteger)port;
@end

@implementation TLPinnedSecurityPolicy {
  NSInteger _port;
}

- (instancetype)initWithPort:(NSInteger)port {
  // Chain validation off: the self-signed gateway cert would otherwise fail the
  // CFStream handshake before evaluateServerTrust runs; the pin is the only check.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  if ((self = [super initWithCertificateChainValidationEnabled:NO])) _port = port;
#pragma clang diagnostic pop
  return self;
}

- (BOOL)evaluateServerTrust:(SecTrustRef)serverTrust forDomain:(NSString *)domain {
  return [TLPinStore trust:serverTrust matchesPinForHost:domain port:_port];
}

@end

/// Installs the provider at load, before React Native opens its first socket.
@interface TLPinnedWebSocketInstaller : NSObject
@end

@implementation TLPinnedWebSocketInstaller

+ (void)load {
  RCTSetCustomSRWebSocketProvider(^SRWebSocket *(NSURLRequest *request) {
    NSURL *url = request.URL;
    NSString *scheme = url.scheme.lowercaseString;
    if (url.host == nil || !([scheme isEqualToString:@"wss"] || [scheme isEqualToString:@"https"])) return nil;
    NSInteger port = url.port != nil ? url.port.integerValue : 443;
    if ([TLPinStore pinForHost:url.host port:port] == nil) return nil;
    return [[SRWebSocket alloc] initWithURLRequest:request
                                         protocols:nil
                                    securityPolicy:[[TLPinnedSecurityPolicy alloc] initWithPort:port]];
  });
}

@end
