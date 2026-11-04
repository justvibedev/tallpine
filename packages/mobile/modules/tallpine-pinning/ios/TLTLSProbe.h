#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Asks a host what certificate it presents, without sending anything secret.
@interface TLTLSProbe : NSObject
/// `pin` is the P-256 SPKI SHA-256 (nil if the key is another type); `trusted`
/// says whether the system CA store accepts the chain.
+ (void)probeHost:(NSString *)host
             port:(NSInteger)port
       completion:(void (^)(NSString *_Nullable pin, BOOL trusted, NSError *_Nullable error))completion;
@end

NS_ASSUME_NONNULL_END
