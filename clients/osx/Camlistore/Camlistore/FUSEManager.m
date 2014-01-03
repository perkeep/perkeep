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

#import <Foundation/NSFileManager.h>

#import "FUSEManager.h"

@implementation FUSEManager

- (BOOL) isMounted
{
    return mounted;
}

- (void)justMounted
{
    mounted = YES;
    [delegate fuseMounted];
    [mountMenu setState:NSOnState];
}

- (void)justUnmounted
{
    mounted = NO;
    [delegate fuseDismounted];
    [mountMenu setState:NSOffState];
}

- (NSString*) mountPath
{
    NSArray* paths = NSSearchPathForDirectoriesInDomains(NSDesktopDirectory, NSUserDomainMask, YES );
    return [NSString stringWithFormat: @"%@/camlistore", [paths objectAtIndex:0]];
}

- (void) mount
{
    shouldBeMounted = YES;

    in = [[NSPipe alloc] init];
    out = [[NSPipe alloc] init];
    task = [[NSTask alloc] init];

    startTime = time(NULL);

    NSMutableString *launchPath = [NSMutableString string];
    [launchPath appendString:[[NSBundle mainBundle] resourcePath]];
    [task setCurrentDirectoryPath:launchPath];

    [launchPath appendString:@"/cammount"];

    NSString *mountDir = [self mountPath];
    [[NSFileManager defaultManager] createDirectoryAtPath:mountDir
                              withIntermediateDirectories:YES
                                               attributes:nil
                                                    error:nil];

    NSDictionary *env = [NSDictionary dictionaryWithObjectsAndKeys:
                         NSHomeDirectory(), @"HOME",
                         NSUserName(), @"USER",
                         @"/bin:/usr/bin:/sbin:/usr/sbin", @"PATH",
                         nil, nil];
    [task setEnvironment:env];

    NSLog(@"Launching '%@'\n", launchPath);
    [task setLaunchPath:launchPath];
    [task setArguments:[NSArray arrayWithObjects:@"-open", [self mountPath], nil]];
    [task setStandardInput:in];
    [task setStandardOutput:out];
    [task setStandardError:out];

    NSFileHandle *fh = [out fileHandleForReading];
    NSNotificationCenter *nc;
    nc = [NSNotificationCenter defaultCenter];

    [nc addObserver:self
           selector:@selector(dataReady:)
               name:NSFileHandleReadCompletionNotification
             object:fh];

    [nc addObserver:self
           selector:@selector(taskTerminated:)
               name:NSTaskDidTerminateNotification
             object:task];

    [task launch];
    [fh readInBackgroundAndNotify];
    NSLog(@"Launched server task -- pid = %d", task.processIdentifier);

    [self justMounted];
}

- (void)dataReady:(NSNotification *)n
{
    NSData *d;
    d = [[n userInfo] valueForKey:NSFileHandleNotificationDataItem];
    if ([d length]) {
        NSString *s = [[NSString alloc] initWithData: d
                                            encoding: NSUTF8StringEncoding];
        NSLog(@"%@", s);
    }
    if (task) {
        [[out fileHandleForReading] readInBackgroundAndNotify];
    }
}

- (void)cleanup
{
    task = nil;

    in = nil;
    out = nil;

    [[NSNotificationCenter defaultCenter] removeObserver:self];
}

- (BOOL)hasFUSE
{
    return [[NSFileManager defaultManager] fileExistsAtPath:@"/Library/Filesystems/osxfusefs.fs"];
}

- (BOOL)hasClientConfig
{
    NSString *confFile = [NSString stringWithFormat:@"%@/.config/camlistore/client-config.json", NSHomeDirectory()];
    return [[NSFileManager defaultManager] fileExistsAtPath:confFile];
}

- (void)createClientConfig
{
    NSTask *put = [[NSTask alloc] init];

    NSMutableString *launchPath = [NSMutableString string];
    [launchPath appendString:[[NSBundle mainBundle] resourcePath]];
    [put setCurrentDirectoryPath:launchPath];
    [launchPath appendString:@"/camput"];
    NSDictionary *env = [NSDictionary dictionaryWithObjectsAndKeys:
                         NSHomeDirectory(), @"HOME",
                         NSUserName(), @"USER",
                         @"/bin:/usr/bin:/sbin:/usr/sbin", @"PATH",
                         nil, nil];
    [put setEnvironment:env];
    [put setLaunchPath:launchPath];
    [put setArguments:[NSArray arrayWithObjects:@"init", nil]];
    [put launch];
    [put waitUntilExit];
}

// If YES is returned, try to remount, otherwise stop
- (BOOL)resolveMountProblemAndRemount
{
    time_t now = time(NULL);
    if (now - startTime < MIN_FUSE_LIFETIME) {
        // See if we can guide the user to a solution
        if (![self hasFUSE]) {
            NSRunAlertPanel(@"Problem Mounting Camlistore FUSE",
                            @"You don't seem to have osxfuse installed. "
                            @"Please go here, install, and try again:\n\n"
                            @"http://osxfuse.github.io/", @"OK", nil, nil);
            return NO;
        } else if (![self hasClientConfig]) {
            NSInteger b = NSRunAlertPanel(@"Problem Mounting Camlistore FUSE",
                                          @"You don't have a camlistore client config. "
                                          @"Would you like me to make you one?",
                                          @"Make Client Config", @"Don't Mount", nil);
            if (b == NSAlertDefaultReturn) {
                [self createClientConfig];
            } else {
                return NO;
            }
        } else {
            NSInteger b = NSRunAlertPanel(@"Problem Mounting Camlistore FUSE",
                                          @"I'm having trouble mounting the FUSE filesystem. "
                                          @"Check Console logs for more details.",
                                          @"Retry", @"Don't Mount", nil);
            return b == NSAlertDefaultReturn;
        }

    }
    return YES;
}

- (void)taskTerminated:(NSNotification *)note
{
    int status = [[note object] terminationStatus];
    NSLog(@"Task terminated with status %d", status);
    [self cleanup];
    [self justUnmounted];
    NSLog(@"Terminated with status %d\n", status);

    if (shouldBeMounted) {
        // Relaunch the server task...
        if ([self resolveMountProblemAndRemount]) {
            NSLog(@"Remounting");
            [NSTimer scheduledTimerWithTimeInterval:1.0
                                             target:self selector:@selector(mount)
                                           userInfo:nil
                                            repeats:NO];
        } else {
            NSLog(@"Should no longer be mounted");
            shouldBeMounted = NO;
        }
    }
    if (!shouldBeMounted) {
        [[NSWorkspace sharedWorkspace] performFileOperation:NSWorkspaceRecycleOperation
                                                     source:[[self mountPath] stringByDeletingLastPathComponent]
                                                destination:@""
                                                      files:[NSArray arrayWithObject:[[self mountPath] lastPathComponent]]
                                                        tag:nil];
    }
}

- (void) dismount
{
    NSLog(@"Unmounting");
    shouldBeMounted = NO;
    NSFileHandle *writer;
    writer = [in fileHandleForWriting];
    [writer writeData:[@"q\n" dataUsingEncoding:NSASCIIStringEncoding]];
    [writer closeFile];
}

@end
