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
#import <AssetsLibrary/AssetsLibrary.h>

@implementation LAAppDelegate

- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)launchOptions
{
    self.locationManager = [[CLLocationManager alloc] init];
    self.locationManager.delegate = self;
    [self.locationManager startMonitoringSignificantLocationChanges];

    [self loadCredentials];

    self.library = [[ALAssetsLibrary alloc] init];
    // TODO: request access to the library first

    return YES;
}

- (void)locationManager:(CLLocationManager *)manager didUpdateLocations:(NSArray *)locations
{
    [self checkForUploads];
}

- (void)loadCredentials
{
    NSURL *serverURL = [NSURL URLWithString:[[NSUserDefaults standardUserDefaults] stringForKey:CamliServerKey]];
    NSString *username = [[NSUserDefaults standardUserDefaults] stringForKey:CamliUsernameKey];

    NSString *password = nil;
    if (username) {
        password = [LACamliUtil passwordForUsername:username];
    }

    if (serverURL && username && password) {
        self.client = [[LACamliClient alloc] initWithServer:serverURL username:username andPassword:password];
    }

    [self checkForUploads];
}

- (void)checkForUploads
{
    UIBackgroundTaskIdentifier assetCheckID = [[UIApplication sharedApplication] beginBackgroundTaskWithName:@"assetCheck" expirationHandler:^{
        LALog(@"asset check task expired");
    }];

    if (self.client && [self.client readyToUpload]) {
        NSInteger __block filesToUpload = 0;

        __block LAAppDelegate *weakSelf = self;
        [self.library enumerateGroupsWithTypes:ALAssetsGroupSavedPhotos usingBlock:^(ALAssetsGroup *group, BOOL *stop) {
            [group enumerateAssetsUsingBlock:^(ALAsset *result, NSUInteger index, BOOL *stop) {

                if (result && [result valueForProperty:ALAssetPropertyType] != ALAssetTypeVideo) { // enumerate returns null after the last item
                    LACamliFile *file = [[LACamliFile alloc] initWithAsset:result];

                    if (![weakSelf.client fileAlreadyUploaded:file]) {
                        filesToUpload++;
                        [weakSelf.client addFile:file withCompletion:^{
                            [UIApplication sharedApplication].applicationIconBadgeNumber--;
                        }];
                    } else {
                        LALog(@"file already uploaded: %@",file.blobRef);
                    }
                }
            }];

            [UIApplication sharedApplication].applicationIconBadgeNumber = filesToUpload;

        } failureBlock:^(NSError *error) {
            LALog(@"failed enumerate: %@",error);
        }];
    }

    [[UIApplication sharedApplication] endBackgroundTask:assetCheckID];
}

- (void)applicationWillResignActive:(UIApplication *)application
{
    // Sent when the application is about to move from active to inactive state. This can occur for certain types of temporary interruptions (such as an incoming phone call or SMS message) or when the user quits the application and it begins the transition to the background state.
    // Use this method to pause ongoing tasks, disable timers, and throttle down OpenGL ES frame rates. Games should use this method to pause the game.
}

- (void)applicationDidEnterBackground:(UIApplication *)application
{
    // Use this method to release shared resources, save user data, invalidate timers, and store enough application state information to restore your application to its current state in case it is terminated later. 
    // If your application supports background execution, this method is called instead of applicationWillTerminate: when the user quits.
}

- (void)applicationWillEnterForeground:(UIApplication *)application
{
    // Called as part of the transition from the background to the inactive state; here you can undo many of the changes made on entering the background.
}

- (void)applicationDidBecomeActive:(UIApplication *)application
{
    // Restart any tasks that were paused (or not yet started) while the application was inactive. If the application was previously in the background, optionally refresh the user interface.
}

- (void)applicationWillTerminate:(UIApplication *)application
{
    // Called when the application is about to terminate. Save data if appropriate. See also applicationDidEnterBackground:.
}

@end
