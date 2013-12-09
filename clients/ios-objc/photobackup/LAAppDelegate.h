//
//  LAAppDelegate.h
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <UIKit/UIKit.h>
#import <CoreLocation/CoreLocation.h>
#import "LACamliClient.h"

@class ALAssetsLibrary;

@interface LAAppDelegate : UIResponder <UIApplicationDelegate,CLLocationManagerDelegate>

@property (strong, nonatomic) UIWindow *window;
@property CLLocationManager *locationManager;

@property LACamliClient *client;
// kicked out of the library if we don't have a reference and still want to play with the books
@property ALAssetsLibrary *library;


@end
