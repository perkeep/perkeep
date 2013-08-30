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

#define STATUS_ITEM_VIEW_WIDTH 24.0

#pragma mark -

@class StatusItemView;

@interface MenubarController : NSObject {
@private
    StatusItemView *_statusItemView;
}

@property (nonatomic) BOOL hasActiveIcon;
@property (nonatomic, strong, readonly) NSStatusItem *statusItem;
@property (nonatomic, strong, readonly) StatusItemView *statusItemView;

@end
