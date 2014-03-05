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
#import <HockeySDK/HockeySDK.h>

@class ALAssetsLibrary;

static NSString* const CamliUsernameKey = @"org.camlistore.username";
static NSString* const CamliServerKey = @"org.camlistore.serverurl";
static NSString* const CamliCredentialsKey = @"org.camlistore.credentials";

@interface LAAppDelegate : UIResponder <UIApplicationDelegate, CLLocationManagerDelegate, BITHockeyManagerDelegate>

@property(strong, nonatomic) UIWindow* window;
@property CLLocationManager* locationManager;

@property LACamliClient* client;
@property ALAssetsLibrary* library;

- (void)loadCredentials;
- (void)checkForUploads;

@end
