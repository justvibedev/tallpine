#import <Foundation/Foundation.h>
#import <Security/Security.h>

NS_ASSUME_NONNULL_BEGIN

/// Process-wide registry of `host:port` -> SHA-256 of the server's public key.
/// A host that is in the registry is trusted for that key and nothing else.
@interface TLPinStore : NSObject

/// Adds entries of the form `host:port=<64 hex>`. Malformed entries are ignored.
+ (void)addEntries:(NSArray<NSString *> *)entries;
+ (nullable NSString *)pinForHost:(NSString *)host port:(NSInteger)port;
/// Lowercase hex SHA-256 of the leaf's SubjectPublicKeyInfo, or nil when the
/// key is not P-256 (the only key type the gateway uses).
+ (nullable NSString *)spkiPinForTrust:(SecTrustRef)trust;
/// YES only when the leaf's key matches the pin registered for host:port.
+ (BOOL)trust:(SecTrustRef)trust matchesPinForHost:(NSString *)host port:(NSInteger)port;

@end

NS_ASSUME_NONNULL_END
