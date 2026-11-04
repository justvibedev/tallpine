#import <React/RCTHTTPRequestHandler.h>

/// Handles https requests to hosts that have a registered pin; every other
/// request keeps React Native's stock handler and CA validation.
@interface TLPinnedHTTPRequestHandler : RCTHTTPRequestHandler
@end
