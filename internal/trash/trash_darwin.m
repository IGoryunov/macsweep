#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// macsweep_trash moves the item at path to the Trash using NSFileManager.
// This is deliberately the only removal primitive in the project: it never
// unlinks, and Finder's "Put Back" works for everything it moves.
int macsweep_trash(const char *path, char **out, char **errmsg) {
    @autoreleasepool {
        *out = NULL;
        *errmsg = NULL;
        NSString *p = [NSString stringWithUTF8String:path];
        if (p == nil) {
            *errmsg = strdup("path is not valid UTF-8");
            return 1;
        }
        NSURL *url = [NSURL fileURLWithPath:p];
        NSURL *result = nil;
        NSError *err = nil;
        BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:url
                                                 resultingItemURL:&result
                                                            error:&err];
        if (!ok) {
            const char *msg = err ? [[err localizedDescription] UTF8String] : "trashItemAtURL failed";
            *errmsg = strdup(msg ? msg : "trashItemAtURL failed");
            return 1;
        }
        const char *dest = result ? [[result path] UTF8String] : "";
        *out = strdup(dest ? dest : "");
        return 0;
    }
}
