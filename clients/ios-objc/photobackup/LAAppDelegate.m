//
//  LAAppDelegate.m
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LAAppDelegate.h"
#import "LACamliUtil.h"
#import "LACamliFile.h"
#import "LAViewController.h"
#import <BugshotKit.h>
#import <AssetsLibrary/AssetsLibrary.h>
#import <HockeySDK/HockeySDK.h>

@implementation LAAppDelegate

- (BOOL)application:(UIApplication*)application didFinishLaunchingWithOptions:(NSDictionary*)launchOptions
{
    [[BITHockeyManager sharedHockeyManager] configureWithIdentifier:@"de94cf9f0f0ad2ea0b19b2ad18ebe11f"
                                                           delegate:self];
    [[BITHockeyManager sharedHockeyManager] startManager];
    [[BITHockeyManager sharedHockeyManager].updateManager setDelegate:self];
    [[BITHockeyManager sharedHockeyManager].updateManager checkForUpdate];

    [BugshotKit enableWithNumberOfTouches:1
                       performingGestures:BSKInvocationGestureNone
                     feedbackEmailAddress:@"nick.oneill@gmail.com"];

    self.locationManager = [[CLLocationManager alloc] init];
    self.locationManager.delegate = self;
    [self.locationManager startMonitoringSignificantLocationChanges];

    [self loadCredentials];

    self.library = [[ALAssetsLibrary alloc] init];

    return YES;
}

- (void)locationManager:(CLLocationManager*)manager didUpdateLocations:(NSArray*)locations
{
    [self checkForUploads];
}

- (void)loadCredentials
{
    NSURL* serverURL = [NSURL URLWithString:[[NSUserDefaults standardUserDefaults] stringForKey:CamliServerKey]];
    NSString* username = [[NSUserDefaults standardUserDefaults] stringForKey:CamliUsernameKey];

    NSString* password = nil;
    if (username) {
        password = [LACamliUtil passwordForUsername:username];
    }

    if (serverURL && username && password) {
        [LACamliUtil statusText:@[
                                    @"found credentials"
                                ]];
        [LACamliUtil logText:@[
                                 @"found credentials"
                             ]];
        self.client = [[LACamliClient alloc] initWithServer:serverURL
                                                   username:username
                                                andPassword:password];

        // TODO there must be a better way to get the current instance of this
        LAViewController* mainView = (LAViewController*)[(UINavigationController*)self.window.rootViewController topViewController];
        [self.client setDelegate:mainView];
    } else {
        [LACamliUtil statusText:@[
                                    @"credentials or server not found"
                                ]];
    }

    [self checkForUploads];
}

- (void)checkForUploads
{
    if (self.client && [self.client readyToUpload]) {
        NSInteger __block filesToUpload = 0;

        [LACamliUtil statusText:@[
                                    @"looking for new files..."
                                ]];

        // checking all assets can take some time
        dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0), ^{
            [self.library enumerateGroupsWithTypes:ALAssetsGroupSavedPhotos usingBlock:^(ALAssetsGroup *group, BOOL *stop) {

                [group enumerateAssetsUsingBlock:^(ALAsset *result, NSUInteger index, BOOL *stop) {
                    if (result && [result valueForProperty:ALAssetPropertyType] != ALAssetTypeVideo) { // enumerate returns null after the last item

                        NSString *filename = [[result defaultRepresentation] filename];

                        @synchronized(self.client){
                            if (![self.client fileAlreadyUploaded:filename]) {
                                filesToUpload++;

                                [LACamliUtil logText:@[[NSString stringWithFormat:@"found %ld files",(long)filesToUpload]]];

                                __block LACamliClient *weakClient = self.client;

                                LACamliFile *file = [[LACamliFile alloc] initWithAsset:result];
                                [self.client addFile:file withCompletion:^{
                                    [UIApplication sharedApplication].applicationIconBadgeNumber = [weakClient.uploadQueue operationCount];
                                }];
                            }
                        }
                    }
                }];

                if (filesToUpload == 0) {
                    [LACamliUtil statusText:@[@"no new files to upload"]];
                }

                [UIApplication sharedApplication].applicationIconBadgeNumber = filesToUpload;

            } failureBlock:^(NSError *error) {
                [LACamliUtil errorText:@[@"failed enumerate: ",[error description]]];
            }];
        });
    }
}

- (void)applicationWillResignActive:(UIApplication*)application
{
    // Sent when the application is about to move from active to inactive state. This can occur for certain types of temporary interruptions (such as an incoming phone call or SMS message) or when the user quits the application and it begins the transition to the background state.
    // Use this method to pause ongoing tasks, disable timers, and throttle down OpenGL ES frame rates. Games should use this method to pause the game.
}

- (void)applicationDidEnterBackground:(UIApplication*)application
{
    // Use this method to release shared resources, save user data, invalidate timers, and store enough application state information to restore your application to its current state in case it is terminated later.
    // If your application supports background execution, this method is called instead of applicationWillTerminate: when the user quits.
}

- (void)applicationWillEnterForeground:(UIApplication*)application
{
    // Called as part of the transition from the background to the inactive state; here you can undo many of the changes made on entering the background.
}

- (void)applicationDidBecomeActive:(UIApplication*)application
{
    // Restart any tasks that were paused (or not yet started) while the application was inactive. If the application was previously in the background, optionally refresh the user interface.
    [self checkForUploads];
}

- (void)applicationWillTerminate:(UIApplication*)application
{
    // Called when the application is about to terminate. Save data if appropriate. See also applicationDidEnterBackground:.
}

@end
