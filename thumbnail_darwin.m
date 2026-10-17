//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <ImageIO/ImageIO.h>
#import <AVFoundation/AVFoundation.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>

// AVFoundation 的资源代理按需 pread 已验证的文件，支持大型视频的随机访问。
@interface CardAtlasVideoReader : NSObject <AVAssetResourceLoaderDelegate> {
    int _fd;
    int64_t _size;
    int64_t _readBytes;
    NSString *_contentType;
    CFAbsoluteTime _deadline;
}
- (instancetype)initWithFD:(int)fd extension:(NSString *)extension;
@end

@implementation CardAtlasVideoReader
- (instancetype)initWithFD:(int)fd extension:(NSString *)extension {
    self = [super init];
    if (self) {
        _fd = dup(fd);
        struct stat info;
        if (_fd < 0 || fstat(_fd, &info) != 0) { [self release]; return nil; }
        _size = info.st_size;
        _contentType = [[extension.lowercaseString isEqualToString:@".mov"] ? AVFileTypeQuickTimeMovie : AVFileTypeMPEG4 retain];
        _deadline = CFAbsoluteTimeGetCurrent() + 15;
    }
    return self;
}
- (BOOL)resourceLoader:(AVAssetResourceLoader *)loader shouldWaitForLoadingOfRequestedResource:(AVAssetResourceLoadingRequest *)request {
    (void)loader;
    @autoreleasepool {
        request.contentInformationRequest.contentType = _contentType;
        request.contentInformationRequest.contentLength = _size;
        request.contentInformationRequest.byteRangeAccessSupported = YES;
        AVAssetResourceLoadingDataRequest *data = request.dataRequest;
        if (data) {
            int64_t offset = MAX(data.currentOffset, data.requestedOffset);
            int64_t end = data.requestsAllDataToEndOfResource ? _size : MIN(_size, data.requestedOffset + MAX((int64_t)0, (int64_t)data.requestedLength));
            if (offset < 0 || offset > _size) {
                [request finishLoadingWithError:[NSError errorWithDomain:NSCocoaErrorDomain code:NSFileReadCorruptFileError userInfo:nil]];
                return YES;
            }
            unsigned char buffer[64 * 1024];
            while (offset < end && !request.isCancelled) {
                if (_readBytes >= 64 * 1024 * 1024 || CFAbsoluteTimeGetCurrent() > _deadline) {
                    [request finishLoadingWithError:[NSError errorWithDomain:NSCocoaErrorDomain code:NSFileReadUnknownError userInfo:nil]];
                    return YES;
                }
                ssize_t count = pread(_fd, buffer, (size_t)MIN((int64_t)sizeof(buffer), end - offset), offset);
                if (count <= 0) {
                    [request finishLoadingWithError:[NSError errorWithDomain:NSCocoaErrorDomain code:NSFileReadCorruptFileError userInfo:nil]];
                    return YES;
                }
                [data respondWithData:[NSData dataWithBytes:buffer length:(NSUInteger)count]];
                offset += count;
                _readBytes += count;
            }
        }
        if (!request.isCancelled) [request finishLoading];
        return YES;
    }
}
- (void)dealloc { if (_fd >= 0) close(_fd); [_contentType release]; [super dealloc]; }
@end

// 缩略图解码通过已验证的文件描述符读取，避免重新按用户路径打开文件。
unsigned char *cardatlas_thumbnail(int fd, int video, const char *extension, size_t *length) {
    @autoreleasepool {
        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithFormat:@"/dev/fd/%d", fd]];
        CGImageRef image = NULL;
        if (video) {
            NSString *ext = [NSString stringWithUTF8String:extension];
            CardAtlasVideoReader *reader = [[CardAtlasVideoReader alloc] initWithFD:fd extension:ext];
            if (!reader) return NULL;
            NSURL *videoURL = [NSURL URLWithString:[@"cardatlas-media://localhost/video" stringByAppendingString:ext]];
            AVURLAsset *asset = [[AVURLAsset alloc] initWithURL:videoURL options:nil];
            dispatch_queue_t queue = dispatch_queue_create("local.cardatlas.thumbnail", DISPATCH_QUEUE_SERIAL);
            [asset.resourceLoader setDelegate:reader queue:queue];
            AVAssetImageGenerator *generator = [[AVAssetImageGenerator alloc] initWithAsset:asset];
            generator.maximumSize = CGSizeMake(240, 240);
            generator.appliesPreferredTrackTransform = YES;
            // 兼容 macOS 13 的同步取帧 API 在独立 Go 工作线程调用，不阻塞界面线程。
            #pragma clang diagnostic push
            #pragma clang diagnostic ignored "-Wdeprecated-declarations"
            image = [generator copyCGImageAtTime:CMTimeMakeWithSeconds(0.5, 600) actualTime:NULL error:NULL];
            if (!image) image = [generator copyCGImageAtTime:kCMTimeZero actualTime:NULL error:NULL];
            #pragma clang diagnostic pop
            [generator release];
            [asset release];
            // 等待已派发的读取回调退出再释放文件描述符，避免取消时访问失效句柄。
            dispatch_sync(queue, ^{});
            dispatch_release(queue);
            [reader release];
        } else {
            CGImageSourceRef source = CGImageSourceCreateWithURL((CFURLRef)url, NULL);
            if (source) {
                NSDictionary *options = @{
                    (id)kCGImageSourceCreateThumbnailFromImageAlways: @YES,
                    (id)kCGImageSourceCreateThumbnailWithTransform: @YES,
                    (id)kCGImageSourceThumbnailMaxPixelSize: @240,
                    (id)kCGImageSourceShouldCacheImmediately: @YES
                };
                image = CGImageSourceCreateThumbnailAtIndex(source, 0, (CFDictionaryRef)options);
                CFRelease(source);
            }
        }
        if (!image) return NULL;
        NSMutableData *data = [NSMutableData data];
        CGImageDestinationRef destination = CGImageDestinationCreateWithData((CFMutableDataRef)data, CFSTR("public.jpeg"), 1, NULL);
        if (!destination) { CGImageRelease(image); return NULL; }
        CGImageDestinationAddImage(destination, image, (CFDictionaryRef)@{(id)kCGImageDestinationLossyCompressionQuality: @0.75});
        BOOL ok = CGImageDestinationFinalize(destination);
        CFRelease(destination);
        CGImageRelease(image);
        if (!ok || data.length > 512 * 1024) return NULL;
        *length = data.length;
        unsigned char *result = malloc(*length);
        if (result) memcpy(result, data.bytes, *length);
        return result;
    }
}
