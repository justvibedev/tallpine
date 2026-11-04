#import "TLPinStore.h"
#import <CommonCrypto/CommonDigest.h>

static NSMutableDictionary<NSString *, NSString *> *TLPins(void) {
  static NSMutableDictionary *pins;
  static dispatch_once_t once;
  dispatch_once(&once, ^{ pins = [NSMutableDictionary dictionary]; });
  return pins;
}

static NSString *TLKey(NSString *host, NSInteger port) {
  return [NSString stringWithFormat:@"%@:%ld", host.lowercaseString, (long)port];
}

@implementation TLPinStore

+ (void)addEntries:(NSArray<NSString *> *)entries {
  NSRegularExpression *re = [NSRegularExpression regularExpressionWithPattern:@"^(.+:[0-9]+)=([0-9a-f]{64})$" options:0 error:nil];
  @synchronized(TLPins()) {
    for (NSString *entry in entries) {
      NSTextCheckingResult *m = [re firstMatchInString:entry options:0 range:NSMakeRange(0, entry.length)];
      if (m == nil) continue;
      TLPins()[[[entry substringWithRange:[m rangeAtIndex:1]] lowercaseString]] = [entry substringWithRange:[m rangeAtIndex:2]];
      NSLog(@"[TallpinePinning] registered pin for %@", [[entry substringWithRange:[m rangeAtIndex:1]] lowercaseString]);
    }
  }
}

+ (NSString *)pinForHost:(NSString *)host port:(NSInteger)port {
  @synchronized(TLPins()) { return TLPins()[TLKey(host, port)]; }
}

+ (NSString *)spkiPinForTrust:(SecTrustRef)trust {
  if (SecTrustGetCertificateCount(trust) < 1) return nil;
  SecCertificateRef leaf = (__bridge SecCertificateRef)((__bridge_transfer NSArray *)SecTrustCopyCertificateChain(trust))[0];
  SecKeyRef key = SecCertificateCopyKey(leaf);
  if (key == NULL) return nil;
  NSDictionary *attrs = (__bridge_transfer NSDictionary *)SecKeyCopyAttributes(key);
  CFErrorRef err = NULL;
  NSData *raw = (__bridge_transfer NSData *)SecKeyCopyExternalRepresentation(key, &err);
  CFRelease(key);
  if (raw == nil || raw.length != 65) return nil;
  if (![attrs[(__bridge id)kSecAttrKeyType] isEqual:(__bridge id)kSecAttrKeyTypeECSECPrimeRandom]) return nil;
  // DER header of a P-256 SubjectPublicKeyInfo, followed by the 65-byte point.
  static const uint8_t header[] = {0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
                                   0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00};
  NSMutableData *spki = [NSMutableData dataWithBytes:header length:sizeof(header)];
  [spki appendData:raw];
  uint8_t digest[CC_SHA256_DIGEST_LENGTH];
  CC_SHA256(spki.bytes, (CC_LONG)spki.length, digest);
  NSMutableString *hex = [NSMutableString stringWithCapacity:64];
  for (int i = 0; i < CC_SHA256_DIGEST_LENGTH; i++) [hex appendFormat:@"%02x", digest[i]];
  return hex;
}

+ (BOOL)trust:(SecTrustRef)trust matchesPinForHost:(NSString *)host port:(NSInteger)port {
  NSString *want = [self pinForHost:host port:port];
  NSString *got = [self spkiPinForTrust:trust];
  return want != nil && got != nil && [want isEqualToString:got];
}

@end
