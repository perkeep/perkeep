//
//  CAAppDelegate.h
//  camlistart
//
//  Created by Nick O'Neill on 7/7/13.
//  Copyright (c) 2013 Camlistore. All rights reserved.
//

#import <Cocoa/Cocoa.h>
#import "PanelController.h"
#import "MenubarController.h"

@interface CAAppDelegate : NSObject <NSApplicationDelegate,PanelControllerDelegate>

@property (nonatomic, strong) MenubarController *menubarController;
@property (nonatomic, strong, readonly) PanelController *panelController;

- (IBAction)togglePanel:(id)sender;

@end
