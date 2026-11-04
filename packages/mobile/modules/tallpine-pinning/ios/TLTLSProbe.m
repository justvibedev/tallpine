#import "TLTLSProbe.h"
#import "TLPinStore.h"

@interface TLTLSProbe () <NSURLSessionDelegate>
@property(nonatomic, copy) void (^completion)(NSString *, BOOL, NSError *);
@property(nonatomic, strong) NSURLSession *session;
@property(nonatomic) BOOL finished;
@end

@implementation TLTLSProbe

+ (void)probeHost:(NSString *)host port:(NSInteger)port completion:(void (^)(NSString *, BOOL, NSError *))completion {
  TLTLSProbe *probe = [TLTLSProbe new];
  probe.completion = completion;
  NSURLSessionConfiguration *config = [NSURLSessionConfiguration ephemeralSessionConfiguration];
  config.timeoutIntervalForRequest = 6;
  probe.session = [NSURLSession sessionWithConfiguration:config delegate:probe delegateQueue:nil];
  NSURLComponents *c = [NSURLComponents new];
  c.scheme = @"https";
  c.host = host;
  c.port = @(port);
  c.path = @"/api/v1/identity";
  [[probe.session dataTaskWithURL:c.URL completionHandler:^(NSData *d, NSURLResponse *r, NSError *e) {
    [probe finishWithPin:nil trusted:NO error:e ?: [NSError errorWithDomain:@"TallpinePinning" code:1 userInfo:@{NSLocalizedDescriptionKey : @"no certificate presented"}]];
  }] resume];
}

- (void)finishWithPin:(NSString *)pin trusted:(BOOL)trusted error:(NSError *)error {
  @synchronized(self) {
    if (self.finished) return;
    self.finished = YES;
  }
  [self.session invalidateAndCancel];
  self.completion(pin, trusted, error);
}

- (void)URLSession:(NSURLSession *)session
    didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge
      completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  SecTrustRef trust = challenge.protectionSpace.serverTrust;
  if (![challenge.protectionSpace.authenticationMethod isEqualToString:NSURLAuthenticationMethodServerTrust] || trust == NULL) {
    completionHandler(NSURLSessionAuthChallengePerformDefaultHandling, nil);
    return;
  }
  BOOL trusted = SecTrustEvaluateWithError(trust, NULL);
  NSString *pin = [TLPinStore spkiPinForTrust:trust];
  completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, nil);
  if (pin != nil) {
    [self finishWithPin:pin trusted:trusted error:nil];
  } else if (trusted) {
    [self finishWithPin:@"" trusted:YES error:nil];
  } else {
    [self finishWithPin:nil trusted:NO error:[NSError errorWithDomain:@"TallpinePinning" code:2 userInfo:@{NSLocalizedDescriptionKey : @"unsupported certificate key"}]];
  }
}

@end
