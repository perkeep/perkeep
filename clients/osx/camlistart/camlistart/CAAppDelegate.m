//
//  CAAppDelegate.m
//  camlistart
//
//  Created by Nick O'Neill on 7/7/13.
//  Copyright (c) 2013 Camlistore. All rights reserved.
//

#import "CAAppDelegate.h"

@implementation CAAppDelegate

NSString * const plistFile = @"org.camlistore.plist";

@synthesize panelController = _panelController;
@synthesize menubarController = _menubarController;

- (void)applicationDidFinishLaunching:(NSNotification *)aNotification
{
    self.menubarController = [[MenubarController alloc] init];
    
    // start the camlistored process
    [self launchctlLoad];
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender
{
    // stop the camlistored process
    [self launchctlUnload];

    // Explicitly remove the icon from the menu bar
    self.menubarController = nil;
    
    return NSTerminateNow;
}

#pragma mark - launchctl stuff

// tell launchctl to load the plist when we start, it can handle double-starts and other nonsense
- (void)launchctlLoad
{
    NSString *launchPlist = [[self supportDirectory] stringByAppendingPathComponent:plistFile];
    
    // remove the existing plist for each launch, we can't count on the bundle always being in the same place
    if ([[NSFileManager defaultManager] fileExistsAtPath:launchPlist]) {
        [[NSFileManager defaultManager] removeItemAtPath:launchPlist error:nil];
    }
    
    NSString *bundlePath = [[NSBundle mainBundle] bundlePath];
    NSString *camlistoredPath = [bundlePath stringByAppendingPathComponent:@"Contents/Resources/camlistored"];
    
    // plist for launchctl, don't open the browser
    NSDictionary *plist = @{
                            @"Label": @"org.camlistore.plist",
                            @"ProgramArguments": @[camlistoredPath, @"--openbrowser=false"],
                            @"RunAtLoad": @YES,
                            @"KeepAlive": @YES
                            };
    
    [[NSFileManager defaultManager] createDirectoryAtPath:[self supportDirectory] withIntermediateDirectories:YES attributes:nil error:nil];
    [plist writeToFile:launchPlist atomically:YES];
    
    NSTask *task = [[NSTask alloc] init];
    [task setLaunchPath:@"/bin/launchctl"];
    [task setCurrentDirectoryPath:[self supportDirectory]];
    
    NSArray *args = @[@"load", plistFile];
    [task setArguments:args];
    
    [task launch];
}

// unload when we quit, this'll stop camlistored
- (void)launchctlUnload
{
    NSTask *task = [[NSTask alloc] init];
    [task setLaunchPath:@"/bin/launchctl"];
    [task setCurrentDirectoryPath:[self supportDirectory]];
    
    NSArray *args = @[@"unload", plistFile];
    [task setArguments:args];
    
    [task launch];
}

#pragma mark - support directory

- (NSString *)supportDirectory
{
    NSArray *paths = NSSearchPathForDirectoriesInDomains(NSApplicationSupportDirectory, NSUserDomainMask, YES);
    NSString *applicationSupportDirectory = [paths objectAtIndex:0];
    
    return [applicationSupportDirectory stringByAppendingPathComponent:@"camlistore"];
}

#pragma mark - kvo for selection

void *kContextActivePanel = &kContextActivePanel;

- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context
{
    if (context == kContextActivePanel) {
        self.menubarController.hasActiveIcon = self.panelController.hasActivePanel;
    }
    else {
        [super observeValueForKeyPath:keyPath ofObject:object change:change context:context];
    }
}

#pragma mark - Actions

- (IBAction)togglePanel:(id)sender
{
    self.menubarController.hasActiveIcon = !self.menubarController.hasActiveIcon;
    self.panelController.hasActivePanel = self.menubarController.hasActiveIcon;
}

#pragma mark - Public accessors

- (PanelController *)panelController
{
    if (_panelController == nil) {
        _panelController = [[PanelController alloc] initWithDelegate:self];
        [_panelController addObserver:self forKeyPath:@"hasActivePanel" options:0 context:kContextActivePanel];
    }
    return _panelController;
}

#pragma mark - PanelControllerDelegate

- (StatusItemView *)statusItemViewForPanelController:(PanelController *)controller
{
    return self.menubarController.statusItemView;
}

- (void)dealloc
{
    [_panelController removeObserver:self forKeyPath:@"hasActivePanel"];
}


@end
