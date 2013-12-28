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

#import <Cocoa/Cocoa.h>

#import "LoginItemManager.h"
#import "TimeTravelWindowController.h"
#import "FUSEManager.h"

#define MIN_LIFETIME 10

@interface AppDelegate : NSObject <FUSEManagerDelegate> {
    NSStatusItem *statusBar;
    IBOutlet NSMenu *statusMenu;

    IBOutlet NSMenuItem *launchBrowserItem;
    IBOutlet NSMenuItem *launchAtStartupItem;
    IBOutlet LoginItemManager *loginItems;
    IBOutlet FUSEManager *fuseManager;
    IBOutlet NSMenuItem *fuseMountItem;

    NSTask *task;
    NSPipe *in, *out;

    BOOL hasSeenStart;
    time_t startTime;

    BOOL terminatingApp;
    int shutdownWaitEvents;
    NSTimer *taskKiller;

    NSString *logPath;
    FILE *logFile;

    TimeTravelWindowController *timeTraveler;
}

- (IBAction)browse:(id)sender;

- (void)launchServer;
- (void)stop;
- (void)openUI;
- (void)taskTerminated:(NSNotification *)note;
- (void)cleanup;

- (void)updateAddItemButtonState;

- (IBAction)setLaunchPref:(id)sender;
- (IBAction)changeLoginItems:(id)sender;

- (IBAction)showAboutPanel:(id)sender;
- (IBAction)showLogs:(id)sender;
- (IBAction)showTechSupport:(id)sender;

- (void)applicationWillTerminate:(NSNotification *)notification;
- (IBAction)toggleMount:(id)sender;

- (void) fuseMounted;
- (void) fuseDismounted;

- (IBAction)openFinder:(id)sender;
- (IBAction)openFinderAsOf:(id)sender;


@end
