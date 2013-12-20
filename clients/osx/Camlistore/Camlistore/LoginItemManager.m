/*
Copyright 2013 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

#import "LoginItemManager.h"


@implementation LoginItemManager

- (id)init
{
    self = [super init];
    if (self) {
        // Initialization code here.
    }

    return self;
}

- (BOOL)loginItemExistsWithLoginItemReference:(LSSharedFileListRef)theLoginItemsRefs forPath:(CFURLRef)thePath {
    BOOL exists = NO;

    return exists;
}

- (BOOL) inLoginItems {
    BOOL exists = NO;
    UInt32 seedValue;

    LSSharedFileListRef theLoginItemsRefs = LSSharedFileListCreate(NULL, kLSSharedFileListSessionLoginItems, NULL);
    CFURLRef thePath = (CFURLRef)CFBridgingRetain([[NSBundle mainBundle] bundlePath]);

    // We're going to grab the contents of the shared file list (LSSharedFileListItemRef objects)
    // and pop it in an array so we can iterate through it to find our item.
    NSArray  *loginItemsArray = (NSArray *)CFBridgingRelease(LSSharedFileListCopySnapshot(theLoginItemsRefs, &seedValue));
    for (id item in loginItemsArray) {
        LSSharedFileListItemRef itemRef = (LSSharedFileListItemRef)CFBridgingRetain(item);
        if (LSSharedFileListItemResolve(itemRef, 0, (CFURLRef*) &thePath, NULL) == noErr) {
            if ([[(NSURL *)CFBridgingRelease(thePath) path] hasPrefix:[[NSBundle mainBundle] bundlePath]])
                exists = YES;
        }
    }
    return exists;
}

- (void) removeLoginItem:(id)sender {
    UInt32 seedValue;

    LSSharedFileListRef theLoginItemsRefs = LSSharedFileListCreate(NULL, kLSSharedFileListSessionLoginItems, NULL);
    CFURLRef thePath = (CFURLRef)CFBridgingRetain([[NSBundle mainBundle] bundlePath]);

    // We're going to grab the contents of the shared file list (LSSharedFileListItemRef objects)
    // and pop it in an array so we can iterate through it to find our item.
    NSArray  *loginItemsArray = (NSArray *)CFBridgingRelease(LSSharedFileListCopySnapshot(theLoginItemsRefs, &seedValue));
    for (id item in loginItemsArray) {
        LSSharedFileListItemRef itemRef = (LSSharedFileListItemRef)CFBridgingRetain(item);
        if (LSSharedFileListItemResolve(itemRef, 0, (CFURLRef*) &thePath, NULL) == noErr) {
            if ([[(NSURL *)CFBridgingRelease(thePath) path] hasPrefix:[[NSBundle mainBundle] bundlePath]]) {
                LSSharedFileListItemRemove(theLoginItemsRefs, itemRef);
            }
        }
    }
}

- (void)addToLoginItems:(id)sender {
    [self removeLoginItem: self];

    LSSharedFileListRef theLoginItemsRefs = LSSharedFileListCreate(NULL, kLSSharedFileListSessionLoginItems, NULL);

    // CFURLRef to the insertable item.
    CFURLRef url = (CFURLRef)CFBridgingRetain([NSURL fileURLWithPath:[[NSBundle mainBundle] bundlePath]]);

    // Actual insertion of an item.
    LSSharedFileListInsertItemURL(theLoginItemsRefs, kLSSharedFileListItemLast, NULL, NULL, url, NULL, NULL);
}

@end
