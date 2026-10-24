#import <Foundation/Foundation.h>
#import <Security/Security.h>
#include <string.h>

static NSMutableDictionary *ob_credential_query(const char *account) {
    return [NSMutableDictionary dictionaryWithDictionary:@{
        (id)kSecClass: (id)kSecClassGenericPassword,
        (id)kSecAttrService: @"com.cardatlas.devices.camera-wifi",
        (id)kSecAttrAccount: [NSString stringWithUTF8String:account],
        (id)kSecAttrSynchronizable: @NO
    }];
}
int ob_credential_read(const char *account, int secret, char **value) {
    @autoreleasepool {
        NSMutableDictionary *query = ob_credential_query(account);
        query[(id)kSecMatchLimit] = (id)kSecMatchLimitOne;
        query[secret ? (id)kSecReturnData : (id)kSecReturnAttributes] = @YES;
        CFTypeRef result = NULL;
        OSStatus status = SecItemCopyMatching((CFDictionaryRef)query, &result);
        if (status == errSecSuccess && secret && value) {
            NSData *data = (NSData *)result;
            char *copy = calloc(data.length + 1, 1);
            if (copy) { memcpy(copy, data.bytes, data.length); *value = copy; }
            else status = errSecAllocate;
        }
        if (result) CFRelease(result);
        return (int)status;
    }
}
int ob_credential_save(const char *account, const char *password) {
    @autoreleasepool {
        NSMutableDictionary *query = ob_credential_query(account);
        NSData *data = [NSData dataWithBytes:password length:strlen(password)];
        NSDictionary *attributes = @{(id)kSecValueData: data, (id)kSecAttrLabel: @"CardAtlas · 相机 Wi-Fi 密码"};
        OSStatus status = SecItemUpdate((CFDictionaryRef)query, (CFDictionaryRef)attributes);
        if (status == errSecItemNotFound) {
            [query addEntriesFromDictionary:attributes];
            status = SecItemAdd((CFDictionaryRef)query, NULL);
        }
        return (int)status;
    }
}
int ob_credential_delete(const char *account) {
    @autoreleasepool { return (int)SecItemDelete((CFDictionaryRef)ob_credential_query(account)); }
}
