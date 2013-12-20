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

    mounted = YES;
    [delegate fuseMounted];
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

- (void)taskTerminated:(NSNotification *)note
{
    int status = [[note object] terminationStatus];
    NSLog(@"Task terminated with status %d", status);
    [self cleanup];
    [delegate fuseDismounted];
    NSLog(@"Terminated with status %d\n", status);

    if (shouldBeMounted) {
        time_t now = time(NULL);
        if (now - startTime < MIN_FUSE_LIFETIME) {
            NSInteger b = NSRunAlertPanel(@"Problem Mounting Camlistore FUSE",
                                          @"I'm having trouble mounting the FUSE filesystem.  "
                                          @"Check Console logs for more details.", @"Retry", @"Don't Mount", nil);
            if (b == NSAlertAlternateReturn) {
                shouldBeMounted = NO;
            }
        }

        // Relaunch the server task...
        [NSTimer scheduledTimerWithTimeInterval:1.0
                                         target:self selector:@selector(mount)
                                       userInfo:nil
                                        repeats:NO];
    } else {
        [[NSWorkspace sharedWorkspace] performFileOperation:NSWorkspaceRecycleOperation
                                                     source:[[self mountPath] stringByDeletingLastPathComponent]
                                                destination:@""
                                                      files:[NSArray arrayWithObject:[[self mountPath] lastPathComponent]]
                                                        tag:nil];
        mounted = NO;
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
