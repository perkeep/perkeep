//
//  CAAppDelegate.m
//  camlistart
//
//  Created by Nick O'Neill on 7/7/13.
//  Copyright (c) 2013 Camlistore. All rights reserved.
//
//  Based on code from Vadim Shpakovski
//  https://github.com/shpakovski/Popup
//

#import "CAAppDelegate.h"

@implementation CAAppDelegate

@synthesize panelController = _panelController;
@synthesize menubarController = _menubarController;

- (void)applicationDidFinishLaunching:(NSNotification *)aNotification
{
    self.menubarController = [[MenubarController alloc] init];
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender
{
    // Explicitly remove the icon from the menu bar
    self.menubarController = nil;
    return NSTerminateNow;
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
