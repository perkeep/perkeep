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

#import "AppDelegate.h"
#import "TimeTravelWindowController.h"

#define FORCEKILL_INTERVAL 15.0     // How long to wait for the server task to exit, on quit

@implementation AppDelegate

- (IBAction)showAboutPanel:(id)sender
{
    [NSApp activateIgnoringOtherApps:YES];
    [[NSApplication sharedApplication] orderFrontStandardAboutPanel:sender];
}

- (void)logMessage:(NSString*)msg
{
    const char *str = [msg cStringUsingEncoding:NSUTF8StringEncoding];
    if (str) {
        fwrite(str, strlen(str), 1, logFile);
    }
}

- (void)flushLog
{
    fflush(logFile);
}

- (NSString *)logFilePath:(NSString*)logName
{
    NSArray *URLs = [[NSFileManager defaultManager] URLsForDirectory:NSLibraryDirectory
                                                           inDomains:NSUserDomainMask];
    NSURL *logsURL = [[URLs lastObject] URLByAppendingPathComponent:@"Logs"];
    NSString *logDir = [logsURL path];
    return [logDir stringByAppendingPathComponent:logName];
}

- (void)awakeFromNib
{
    hasSeenStart = NO;

    logPath = [self logFilePath:@"Camlistored.log"];
    const char *logPathC = [logPath cStringUsingEncoding:NSUTF8StringEncoding];

    NSString *oldLogFileString = [self logFilePath:@"Camlistored.log.old"];
    const char *oldLogPath = [oldLogFileString cStringUsingEncoding:NSUTF8StringEncoding];
    rename(logPathC, oldLogPath); // This will fail the first time.

    // Now our logs go to a private file.
    logFile = fopen(logPathC, "w");

    [NSTimer scheduledTimerWithTimeInterval:1.0
                                     target:self selector:@selector(flushLog)
                                   userInfo:nil
                                    repeats:YES];

    [[NSUserDefaults standardUserDefaults]
     registerDefaults: [NSDictionary dictionaryWithObjectsAndKeys:
                        [NSNumber numberWithBool:YES], @"browseAtStart",
                        nil, nil]];
    NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];

    statusBar=[[NSStatusBar systemStatusBar] statusItemWithLength: 26.0];
    [statusBar setAlternateImage: [NSImage imageNamed:@"menuicon-selected"]];
    [statusBar setImage: [NSImage imageNamed:@"menuicon"]];
    [statusBar setMenu: statusMenu];
    [statusBar setEnabled:YES];
    [statusBar setHighlightMode:YES];

    // Fix up the masks for all the alt items.
    for (int i = 0; i < [statusMenu numberOfItems]; ++i) {
        NSMenuItem *itm = [statusMenu itemAtIndex:i];
        if ([itm isAlternate]) {
            [itm setKeyEquivalentModifierMask:NSAlternateKeyMask];
        }
    }

    [launchBrowserItem setState:([defaults boolForKey:@"browseAtStart"] ? NSOnState : NSOffState)];
    [self updateAddItemButtonState];

    [self launchServer];
}

- (void)stop
{
    NSFileHandle *writer;
    writer = [in fileHandleForWriting];
    [writer closeFile];
    [task terminate];
}

- (void)launchServer
{
    in = [[NSPipe alloc] init];
    out = [[NSPipe alloc] init];
    task = [[NSTask alloc] init];

    startTime = time(NULL);

    NSMutableString *launchPath = [NSMutableString string];
    [launchPath appendString:[[NSBundle mainBundle] resourcePath]];
    [task setCurrentDirectoryPath:launchPath];

    [launchPath appendString:@"/camlistored"];

    NSDictionary *env = [NSDictionary dictionaryWithObjectsAndKeys:
                         NSHomeDirectory(), @"HOME",
                         NSUserName(), @"USER",
                         nil, nil];
    [task setEnvironment:env];

    [self logMessage:[NSString stringWithFormat:@"Launching '%@'\n", launchPath]];
    [task setLaunchPath:launchPath];
    [task setArguments:[NSArray arrayWithObjects:@"-openbrowser=false", nil]];
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
}

- (void) shutdownEvent {
    shutdownWaitEvents--;
    NSLog(@"Received a shutdown event.  %d to go", shutdownWaitEvents);
    if (shutdownWaitEvents == 0) {
        NSLog(@"Received last shutdown event.  bye");
        [NSApp replyToApplicationShouldTerminate:NSTerminateNow];
    }
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    NSLog(@"Asking if we should terminate...");
    BOOL isRunning = [task isRunning];
    if (isRunning) {
        terminatingApp = YES;
        [self stopTask];
        shutdownWaitEvents = 1;
        if ([fuseManager isMounted]) {
            [fuseManager dismount];
            shutdownWaitEvents++;
        }
        return NSTerminateLater;
    }
    return NSTerminateNow;
}

- (void)applicationWillTerminate:(NSNotification *)notification
{
    NSLog(@"Terminating.");
}

- (void)stopTask
{
    if (taskKiller) {
        return; // Already shutting down.
    }
    NSLog(@"Telling server task to stop...");
    NSFileHandle *writer;
    writer = [in fileHandleForWriting];
    [task terminate];
    [writer closeFile];
    taskKiller = [NSTimer scheduledTimerWithTimeInterval:FORCEKILL_INTERVAL
                                                  target:self
                                                selector:@selector(killTask)
                                                userInfo:nil
                                                 repeats:NO];
}

- (void)killTask
{
    NSLog(@"Force terminating task");
    [task terminate];
}

- (void)taskTerminated:(NSNotification *)note
{
    int status = [[note object] terminationStatus];
    NSLog(@"Task terminated with status %d", status);
    [self cleanup];
    [self logMessage: [NSString stringWithFormat:@"Terminated with status %d\n",
                       status]];

    if (terminatingApp) {
        // I was just waiting for the task to exit before quitting
        [self shutdownEvent];
    } else {
        time_t now = time(NULL);
        if (now - startTime < MIN_LIFETIME) {
            NSInteger b = NSRunAlertPanel(@"Problem Running Camlistore",
                                          @"camlistored doesn't seem to be operating properly.  "
                                          @"Check Console logs for more details.", @"Retry", @"Quit", nil);
            if (b == NSAlertAlternateReturn) {
                [NSApp terminate:self];
            }
        }

        // Relaunch the server task...
        [NSTimer scheduledTimerWithTimeInterval:1.0
                                         target:self selector:@selector(launchServer)
                                       userInfo:nil
                                        repeats:NO];
    }
}

- (void)cleanup
{
    [taskKiller invalidate];
    taskKiller = nil;

    task = nil;

    in = nil;
    out = nil;

    [[NSNotificationCenter defaultCenter] removeObserver:self];
}

- (void)openUI
{
    NSDictionary *info = [[NSBundle mainBundle] infoDictionary];
    NSString *homePage = [info objectForKey:@"HomePage"];
    NSURL *url=[NSURL URLWithString:homePage];
    [[NSWorkspace sharedWorkspace] openURL:url];
}

- (IBAction)browse:(id)sender
{
    [self openUI];
}

- (void)appendData:(NSData *)d
{
    NSString *s = [[NSString alloc] initWithData: d
                                        encoding: NSUTF8StringEncoding];
    if (!hasSeenStart) {
        if ([s rangeOfString:@"Available on http"].location != NSNotFound) {
            NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
            if ([defaults boolForKey:@"browseAtStart"]) {
                [self openUI];
            }
            hasSeenStart = YES;
        }
    }

    [self logMessage:s];
}

- (void)dataReady:(NSNotification *)n
{
    NSData *d;
    d = [[n userInfo] valueForKey:NSFileHandleNotificationDataItem];
    if ([d length]) {
        [self appendData:d];
    }
    if (task) {
        [[out fileHandleForReading] readInBackgroundAndNotify];
    }
}

- (IBAction)setLaunchPref:(id)sender {
    NSCellStateValue stateVal = [sender state];
    stateVal = (stateVal == NSOnState) ? NSOffState : NSOnState;

    NSLog(@"Setting launch pref to %s", stateVal == NSOnState ? "on" : "off");

    [[NSUserDefaults standardUserDefaults]
     setBool:(stateVal == NSOnState)
     forKey:@"browseAtStart"];

    [launchBrowserItem setState:([[NSUserDefaults standardUserDefaults]
                                  boolForKey:@"browseAtStart"] ? NSOnState : NSOffState)];

    [[NSUserDefaults standardUserDefaults] synchronize];
}

- (void) updateAddItemButtonState
{
    [launchAtStartupItem setState:[loginItems inLoginItems] ? NSOnState : NSOffState];
}

- (IBAction)changeLoginItems:(id)sender
{
    if([sender state] == NSOffState) {
        [loginItems addToLoginItems:self];
    } else {
        [loginItems removeLoginItem:self];
    }
    [self updateAddItemButtonState];
}


- (IBAction)showTechSupport:(id)sender
{
    NSDictionary *info = [[NSBundle mainBundle] infoDictionary];
    NSString *homePage = [info objectForKey:@"SupportPage"];
    NSURL *url=[NSURL URLWithString:homePage];
    [[NSWorkspace sharedWorkspace] openURL:url];

}

- (IBAction)showLogs:(id)sender
{
    if (![[NSWorkspace sharedWorkspace] openFile:logPath]) {
        NSRunAlertPanel(@"Cannot Find Logfile",
                        @"I've been looking for logs in all the wrong places.", nil, nil, nil);
        return;
    }
}

- (IBAction)toggleMount:(id)sender {
    NSLog(@"Toggling mount");
    if ([fuseManager isMounted]) {
        [fuseManager dismount];
    } else {
        [fuseManager mount];
    }
}

- (void) fuseDismounted {
    NSLog(@"FUSE dismounted");
    if (terminatingApp) {
        [self shutdownEvent];
    }
}

- (void) fuseMounted {
    NSLog(@"FUSE mounted");
}

- (IBAction)openFinder:(id)sender
{
    if (![[NSWorkspace sharedWorkspace] openFile:[fuseManager mountPath]]) {
        NSRunAlertPanel(@"Cannot Open Finder Window",
                        @"Can't find mount path or something.", nil, nil, nil);
        return;
    }
}

- (IBAction)openFinderAsOf:(id)sender
{
    [NSApp activateIgnoringOtherApps:YES];

    if (timeTraveler == nil) {
        timeTraveler = [[TimeTravelWindowController alloc]
                        initWithWindowNibName:@"TimeTravelWindowController"];
        [timeTraveler setMountPath:[fuseManager mountPath]];
    }
    [timeTraveler showWindow:self];
}


@end
