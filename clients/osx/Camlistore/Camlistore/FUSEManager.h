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

#import <Foundation/Foundation.h>

#define MIN_FUSE_LIFETIME 10

@protocol FUSEManagerDelegate
- (void) fuseMounted;
- (void) fuseDismounted;
@end

@interface FUSEManager : NSObject {
@private
    BOOL shouldBeMounted;
    BOOL mounted;
    NSString *mountPoint;

    time_t startTime;
    NSTask *task;
    NSPipe *in, *out;

    IBOutlet id<FUSEManagerDelegate> delegate;
    IBOutlet NSMenuItem *mountMenu;
}

- (NSString *)mountPath;
- (BOOL) isMounted;
- (void) mount;
- (void) dismount;



@end
