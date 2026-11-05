#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <UIKit/UIKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <objc/runtime.h>

// Matches the composer's 10 MB per-image limit; anything larger is reported by size, unsent.
static const NSUInteger TallpineMaxImageBytes = 10 * 1024 * 1024;
// The composer's batch limits (8 attachments, 25 MB of images). Applied here too, so
// a large multi-photo copy isn't read and encoded only to be discarded in JS.
static const NSUInteger TallpineMaxImages = 8;
static const NSUInteger TallpineMaxBatchBytes = 25 * 1024 * 1024;
static char TallpineImagePasteTagKey;
static char TallpineImagePasteModuleKey;
static IMP TallpineOriginalPaste;
static IMP TallpineOriginalCanPerformAction;

@interface TallpineImagePaste : RCTEventEmitter <RCTBridgeModule>
- (void)pasteImagesForTag:(NSNumber *)tag;
@end

/** Weak box: an associated object can't be weak, and the module may go away first. */
@interface TallpineImagePasteModuleRef : NSObject
@property (nonatomic, weak) TallpineImagePaste *module;
@end
@implementation TallpineImagePasteModuleRef
@end

static BOOL TallpineAcceptsImagePaste(UITextView *view)
{
  if (!objc_getAssociatedObject(view, &TallpineImagePasteTagKey) || !view.isEditable) return NO;
  if ([[view valueForKey:@"contextMenuHidden"] boolValue]) return NO;
  // Only an image-only clipboard. Spreadsheet cells and rich copies carry an
  // image rendering beside their text, and those must still paste as text.
  UIPasteboard *pasteboard = UIPasteboard.generalPasteboard;
  if (pasteboard.hasStrings) return NO;
  if (pasteboard.hasImages) return YES;
  // `hasImages` doesn't count every image type (HEIC, for one). Type names are
  // detection-only, so checking them raises no paste prompt.
  for (NSString *identifier in pasteboard.pasteboardTypes) {
    if ([[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) return YES;
  }
  return NO;
}

// RCTUITextView's own `paste:`. Images go to JS as attachments; text pastes as usual.
static void TallpinePaste(UITextView *self, SEL _cmd, id sender)
{
  NSNumber *tag = objc_getAssociatedObject(self, &TallpineImagePasteTagKey);
  TallpineImagePaste *module = ((TallpineImagePasteModuleRef *)objc_getAssociatedObject(self, &TallpineImagePasteModuleKey)).module;
  if (tag && module && TallpineAcceptsImagePaste(self)) {
    [module pasteImagesForTag:tag];
    return;
  }
  ((void (*)(id, SEL, id))TallpineOriginalPaste)(self, _cmd, sender);
}

// UITextView only offers Paste for text it can insert; an image-only clipboard needs saying yes.
static BOOL TallpineCanPerformAction(UITextView *self, SEL _cmd, SEL action, id sender)
{
  BOOL original = ((BOOL (*)(id, SEL, SEL, id))TallpineOriginalCanPerformAction)(self, _cmd, action, sender);
  if (original || action != @selector(paste:)) return original;
  return TallpineAcceptsImagePaste(self);
}

static void TallpineInstallPasteHooks(void)
{
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    Class textView = NSClassFromString(@"RCTUITextView");
    Method paste = class_getInstanceMethod(textView, @selector(paste:));
    Method canPerform = class_getInstanceMethod(textView, @selector(canPerformAction:withSender:));
    if (!paste || !canPerform) return;
    TallpineOriginalPaste = method_setImplementation(paste, (IMP)TallpinePaste);
    TallpineOriginalCanPerformAction = method_setImplementation(canPerform, (IMP)TallpineCanPerformAction);
  });
}

static UITextView *TallpineFindTextView(UIView *view)
{
  Class textView = NSClassFromString(@"RCTUITextView");
  if ([view isKindOfClass:textView]) return (UITextView *)view;
  for (UIView *child in view.subviews) {
    UITextView *found = TallpineFindTextView(child);
    if (found) return found;
  }
  return nil;
}

static NSString *TallpinePassthroughMimeType(NSString *identifier)
{
  static NSDictionary<NSString *, NSString *> *types;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    types = @{
      UTTypePNG.identifier : @"image/png",
      UTTypeJPEG.identifier : @"image/jpeg",
      UTTypeGIF.identifier : @"image/gif",
      UTTypeWebP.identifier : @"image/webp",
      UTTypeBMP.identifier : @"image/bmp",
    };
  });
  return types[identifier];
}

/** Whether an item holds an image, from its type names alone: nothing is read. */
static BOOL TallpineItemHasImage(UIPasteboard *pasteboard, NSInteger index)
{
  for (NSString *identifier in [pasteboard pasteboardTypesForItemSet:[NSIndexSet indexSetWithIndex:index]].firstObject ?: @[]) {
    if ([[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) return YES;
  }
  return NO;
}

/**
 * Reads one item on main, inside the user's Paste, and only as far as needed:
 * representations are produced on demand, and some are large (the TIFF of a Retina
 * screenshot over Universal Clipboard). Walks the types in the pasteboard's order
 * and stops at the first one the agents take as-is that fits. Failing that, keeps
 * one source to re-encode as JPEG: the first oversized one already read, else the
 * item's first image type (HEIC, TIFF). nil when the item holds no image.
 */
static NSDictionary *TallpineReadImageItem(UIPasteboard *pasteboard, NSInteger index)
{
  NSIndexSet *item = [NSIndexSet indexSetWithIndex:index];
  NSString *firstImageType = nil;
  NSData *source = nil;
  NSUInteger oversized = 0;
  for (NSString *identifier in [pasteboard pasteboardTypesForItemSet:item].firstObject ?: @[]) {
    if (![[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) continue;
    if (!firstImageType) firstImageType = identifier;
    NSString *mimeType = TallpinePassthroughMimeType(identifier);
    if (!mimeType) continue;
    NSData *data = [pasteboard dataForPasteboardType:identifier inItemSet:item].firstObject;
    if (![data isKindOfClass:NSData.class]) continue;
    if (data.length <= TallpineMaxImageBytes) return @{@"mimeType" : mimeType, @"data" : data};
    oversized = oversized ? MIN(oversized, data.length) : data.length;
    if (!source) source = data;
  }
  if (!firstImageType) return nil;
  if (!source) {
    NSData *data = [pasteboard dataForPasteboardType:firstImageType inItemSet:item].firstObject;
    if ([data isKindOfClass:NSData.class]) source = data;
  }
  NSMutableDictionary *read = [@{@"oversized" : @(oversized)} mutableCopy];
  if (source) read[@"source"] = source;
  return read;
}

/**
 * `{ mimeType, bytes, data? }` for one read item; no data means too large or
 * unreadable. Runs off main: base64, and for a re-encode the decode and JPEG.
 */
static NSDictionary *TallpineEncodeImage(NSDictionary *read)
{
  NSData *data = read[@"data"];
  if (data) return @{@"mimeType" : read[@"mimeType"], @"bytes" : @(data.length), @"data" : [data base64EncodedStringWithOptions:0]};
  UIImage *image = read[@"source"] ? [UIImage imageWithData:read[@"source"]] : nil;
  NSData *jpeg = image ? UIImageJPEGRepresentation(image, 0.9) : nil;
  if (jpeg.length && jpeg.length <= TallpineMaxImageBytes) return @{@"mimeType" : @"image/jpeg", @"bytes" : @(jpeg.length), @"data" : [jpeg base64EncodedStringWithOptions:0]};
  NSUInteger oversized = [read[@"oversized"] unsignedIntegerValue];
  NSUInteger bytes = jpeg.length ? (oversized ? MIN(oversized, jpeg.length) : jpeg.length) : oversized;
  return @{@"mimeType" : @"image/jpeg", @"bytes" : @(bytes)};
}

@implementation TallpineImagePaste {
  NSMapTable<NSNumber *, UITextView *> *_bindings;
}

@synthesize viewRegistry_DEPRECATED = _viewRegistry_DEPRECATED;

RCT_EXPORT_MODULE(TallpineImagePaste)

+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSArray<NSString *> *)supportedEvents { return @[ @"TallpineImagePaste" ]; }

- (instancetype)init
{
  if (self = [super init]) _bindings = [NSMapTable strongToWeakObjectsMapTable];
  return self;
}

RCT_EXPORT_METHOD(attach:(nonnull NSNumber *)tag)
{
  TallpineInstallPasteHooks();
  UITextView *view = TallpineFindTextView([self.viewRegistry_DEPRECATED viewForReactTag:tag]);
  if (!view) return;
  [self detachTag:tag];
  TallpineImagePasteModuleRef *ref = [TallpineImagePasteModuleRef new];
  ref.module = self;
  objc_setAssociatedObject(view, &TallpineImagePasteTagKey, tag, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  objc_setAssociatedObject(view, &TallpineImagePasteModuleKey, ref, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  [_bindings setObject:view forKey:tag];
}

RCT_EXPORT_METHOD(detach:(nonnull NSNumber *)tag)
{
  [self detachTag:tag];
}

- (void)detachTag:(NSNumber *)tag
{
  UITextView *view = [_bindings objectForKey:tag];
  [_bindings removeObjectForKey:tag];
  // Fabric recycles text inputs; only clear the binding if it is still ours.
  if (view && [objc_getAssociatedObject(view, &TallpineImagePasteTagKey) isEqual:tag]) {
    objc_setAssociatedObject(view, &TallpineImagePasteTagKey, nil, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    objc_setAssociatedObject(view, &TallpineImagePasteModuleKey, nil, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  }
}

- (void)pasteImagesForTag:(NSNumber *)tag
{
  UIPasteboard *pasteboard = UIPasteboard.generalPasteboard;
  NSMutableArray<NSDictionary *> *items = [NSMutableArray new];
  NSUInteger held = 0;
  __block NSString *overflow = nil;
  for (NSInteger index = 0; index < pasteboard.numberOfItems; index++) {
    if (!TallpineItemHasImage(pasteboard, index)) continue;
    if (items.count >= TallpineMaxImages) { overflow = @"count"; break; }
    NSDictionary *read = TallpineReadImageItem(pasteboard, index);
    if (!read) continue;
    NSUInteger bytes = ((NSData *)(read[@"data"] ?: read[@"source"])).length;
    // The first image is kept whatever its size: it is already read, and its JPEG may fit.
    if (items.count && held + bytes > TallpineMaxBatchBytes) { overflow = @"size"; break; }
    held += bytes;
    [items addObject:read];
  }
  if (!items.count) return;
  // Decoding, re-encoding and base64 can take a moment on a large photo; keep them off the UI.
  __weak TallpineImagePaste *weakSelf = self;
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
    NSMutableArray<NSDictionary *> *images = [NSMutableArray new];
    NSUInteger sent = 0;
    for (NSDictionary *read in items) {
      NSDictionary *image = TallpineEncodeImage(read);
      NSUInteger bytes = image[@"data"] ? [image[@"bytes"] unsignedIntegerValue] : 0;
      if (images.count && sent + bytes > TallpineMaxBatchBytes) { overflow = overflow ?: @"size"; break; }
      sent += bytes;
      [images addObject:image];
    }
    NSMutableDictionary *body = [@{@"tag" : tag, @"images" : images} mutableCopy];
    if (overflow) body[@"overflow"] = overflow;
    dispatch_async(dispatch_get_main_queue(), ^{
      [weakSelf sendEventWithName:@"TallpineImagePaste" body:body];
    });
  });
}

- (void)invalidate
{
  for (NSNumber *tag in _bindings.keyEnumerator.allObjects) [self detachTag:tag];
  [super invalidate];
}

@end
