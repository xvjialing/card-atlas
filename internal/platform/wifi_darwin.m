#import <Foundation/Foundation.h>
#import <CoreWLAN/CoreWLAN.h>
#import <CoreLocation/CoreLocation.h>
#import <dispatch/dispatch.h>
#include <string.h>

// 原生错误元数据单独返回给日志层；界面仍显示可操作的中文提示。
static char *ob_wifi_error(NSString *message, NSString *stage, NSError *error) {
    NSDictionary *details = @{
        @"message": message,
        @"stage": stage,
        @"domain": error.domain ?: @"CoreWLAN",
        @"code": @(error ? error.code : 0),
        @"reason": error.localizedDescription ?: @"系统未提供具体错误"
    };
    NSData *data = [NSJSONSerialization dataWithJSONObject:details options:0 error:NULL];
    NSString *json = [[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease];
    return strdup([json UTF8String]);
}

char *ob_wifi_ssid(void) {
    @autoreleasepool {
        NSString *ssid = [[[CWWiFiClient sharedWiFiClient] interface] ssid];
        return ssid ? strdup([ssid UTF8String]) : NULL;
    }
}

char *ob_wifi_interface(void) {
    @autoreleasepool {
        NSString *name = [[[CWWiFiClient sharedWiFiClient] interface] interfaceName];
        return name ? strdup([name UTF8String]) : NULL;
    }
}

char *ob_wifi_join(const char *name, const char *password) {
    @autoreleasepool {
        CWInterface *interface = [[CWWiFiClient sharedWiFiClient] interface];
        if (!interface) return ob_wifi_error(@"没有可用的 Wi-Fi 网卡", @"interface", nil);
        NSString *ssid = [NSString stringWithUTF8String:name];
        NSError *error = nil;
        NSSet<CWNetwork *> *networks = [interface scanForNetworksWithSSID:[ssid dataUsingEncoding:NSUTF8StringEncoding] error:&error];
        CWNetwork *network = nil;
        for (CWNetwork *candidate in networks) {
            if (!network || candidate.rssiValue > network.rssiValue) network = candidate;
        }
        if (!network) return ob_wifi_error(@"未找到相机热点，请稍后重试；也可从 macOS Wi-Fi 菜单手动加入，再选择已连接热点", @"scan", error);
        BOOL ok = [interface associateToNetwork:network password:[NSString stringWithUTF8String:password] error:&error];
        if (!ok) return ob_wifi_error(@"加入热点失败，请核对相机无线信息中的密码并重试；也可在 macOS Wi-Fi 菜单连接", @"associate", error);
        return NULL;
    }
}

char *ob_wifi_scan(char **failure) {
    @autoreleasepool {
        CWInterface *interface = [[CWWiFiClient sharedWiFiClient] interface];
        if (!interface) { *failure = ob_wifi_error(@"没有可用的 Wi-Fi 网卡", @"interface", nil); return NULL; }
        NSError *error = nil;
        NSSet<CWNetwork *> *networks = [interface scanForNetworksWithSSID:nil error:&error];
        if (!networks) { *failure = ob_wifi_error(@"扫描热点失败，请检查 Wi-Fi 是否开启及系统定位权限，再重试扫描", @"scan", error); return NULL; }
        NSMutableArray *items = [NSMutableArray array];
        for (CWNetwork *network in networks) {
            if (!network.ssid.length || [network.ssid isEqualToString:@"<redacted>"]) continue;
            NSString *security = @"不支持的认证";
            BOOL supported = YES;
            if ([network supportsSecurity:kCWSecurityWPA3Transition]) security = @"WPA2/WPA3";
            else if ([network supportsSecurity:kCWSecurityWPA3Personal]) security = @"WPA3";
            else if ([network supportsSecurity:kCWSecurityWPA2Personal]) security = @"WPA2";
            else if ([network supportsSecurity:kCWSecurityWPAPersonal] || [network supportsSecurity:kCWSecurityWPAPersonalMixed]) security = @"WPA";
            else { supported = NO; if ([network supportsSecurity:kCWSecurityNone]) security = @"开放网络"; }
            [items addObject:@{@"ssid": network.ssid, @"rssi": @(network.rssiValue), @"channel": @(network.wlanChannel.channelNumber), @"security": security, @"supported": @(supported)}];
        }
        NSData *data = [NSJSONSerialization dataWithJSONObject:items options:0 error:NULL];
        NSString *json = [[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease];
        return strdup(json.UTF8String);
    }
}

// 只接收授权变化，不采集经纬度；对象由主线程持有至应用退出。
@interface OBWiFiLocationDelegate : NSObject <CLLocationManagerDelegate>
@end
@implementation OBWiFiLocationDelegate
- (void)locationManagerDidChangeAuthorization:(CLLocationManager *)manager {}
@end
int ob_wifi_authorization(int request) {
    @autoreleasepool {
        if (![CLLocationManager locationServicesEnabled]) return -1;
        __block int status = 0;
        void (^check)(void) = ^{
            static CLLocationManager *manager = nil;
            static OBWiFiLocationDelegate *delegate = nil;
            if (!manager) {
                manager = [[CLLocationManager alloc] init];
                delegate = [[OBWiFiLocationDelegate alloc] init];
                manager.delegate = delegate;
            }
            status = (int)manager.authorizationStatus;
            if (request && status == kCLAuthorizationStatusNotDetermined) [manager requestWhenInUseAuthorization];
        };
        if ([NSThread isMainThread]) check();
        else dispatch_sync(dispatch_get_main_queue(), check);
        return status;
    }
}
