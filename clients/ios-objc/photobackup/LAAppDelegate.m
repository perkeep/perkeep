//
//  LAAppDelegate.m
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LAAppDelegate.h"
#import "LACamliFile.h"
#import "LAViewController.h"
#import <AssetsLibrary/AssetsLibrary.h>

@implementation LAAppDelegate

- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)launchOptions
{

    self.locationManager = [[CLLocationManager alloc] init];
    self.locationManager.delegate = self;
    [self.locationManager startMonitoringSignificantLocationChanges];
    
    NSString *credentialsPath = [[NSBundle mainBundle] pathForResource:@"credentials" ofType:@"plist"];
    NSDictionary *credentials = [NSDictionary dictionaryWithContentsOfFile:credentialsPath];
    
    NSAssert(credentials[@"camlistore_url"], @"no camlistore url specified");
    NSAssert(credentials[@"camlistore_username"], @"no camlistore username specified");
    NSAssert(credentials[@"camlistore_password"], @"no camlistore password specified");
    
    self.client = [[LACamliClient alloc] initWithServer:[NSURL URLWithString:credentials[@"camlistore_url"]] username:credentials[@"camlistore_username"] andPassword:credentials[@"camlistore_password"]];

    [(LAViewController *)self.window.rootViewController setClient:self.client];

    self.library = [[ALAssetsLibrary alloc] init];
    
    return YES;
}

- (void)locationManager:(CLLocationManager *)manager didUpdateLocations:(NSArray *)locations
{
    CLLocation *updatedLocation = [locations lastObject];
    LALog(@"updated location: %@",updatedLocation);

    NSString *documents = NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES)[0];
    NSString *updatesLocation = [documents stringByAppendingPathComponent:@"locations.plist"];

    NSMutableArray *locationArchive = [NSMutableArray arrayWithContentsOfFile:updatesLocation];
    if (!locationArchive) {
        locationArchive = [NSMutableArray array];
    }

    [locationArchive addObject:[updatedLocation timestamp]];
    [locationArchive writeToFile:updatesLocation atomically:YES];
    
    if ([ALAssetsLibrary authorizationStatus] == ALAuthorizationStatusAuthorized) {
        [self checkForUploads];
    }
}

- (void)checkForUploads
{
    NSInteger __block filesToUpload = 0;

    UIBackgroundTaskIdentifier assetCheckID = [[UIApplication sharedApplication] beginBackgroundTaskWithName:@"assetCheck" expirationHandler:^{
        LALog(@"asset check task expired");
    }];

    [self.library enumerateGroupsWithTypes:ALAssetsGroupSavedPhotos usingBlock:^(ALAssetsGroup *group, BOOL *stop) {
        [group enumerateAssetsUsingBlock:^(ALAsset *result, NSUInteger index, BOOL *stop) {
            
            if (result && [result valueForProperty:ALAssetPropertyType] != ALAssetTypeVideo) { // enumerate returns null after the last item
                LACamliFile *file = [[LACamliFile alloc] initWithAsset:result];
                
                if (![self.client fileAlreadyUploaded:file]) {
                    filesToUpload++;
                    [self.client addFile:file withCompletion:^{
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
