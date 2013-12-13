//
//  LACamliClient.m
//
//  Created by Nick O'Neill on 1/10/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LACamliClient.h"
#import "LACamliUploadOperation.h"
#import "LACamliFile.h"
#import "LACamliUtil.h"

@implementation LACamliClient

- (id)initWithServer:(NSURL *)server username:(NSString *)username andPassword:(NSString *)password
{
    NSParameterAssert(server);
    NSParameterAssert(username);
    NSParameterAssert(password);
    
    if (self = [super init]) {
        self.serverURL = server;
        self.username = username;
        self.password = password;
        
        if ([[NSFileManager defaultManager] fileExistsAtPath:[self uploadedBlobRefArchivePath]]) {
            self.uploadedBlobRefs = [NSMutableArray arrayWithContentsOfFile:[self uploadedBlobRefArchivePath]];
        }
        if (!self.uploadedBlobRefs) {
            self.uploadedBlobRefs = [NSMutableArray array];
        }
        
        self.uploadQueue = [[NSOperationQueue alloc] init];
        self.uploadQueue.maxConcurrentOperationCount = 1;

        self.isAuthorized = false;
        self.authorizing = false;
        
        NSURLSessionConfiguration *config = [NSURLSessionConfiguration defaultSessionConfiguration];
        config.HTTPAdditionalHeaders = @{@"Authorization": [NSString stringWithFormat:@"Basic %@",[self encodedAuth]]};
        self.session = [NSURLSession sessionWithConfiguration:config delegate:self delegateQueue:nil];
    }
    
    return self;
}

#pragma mark - ready state

- (BOOL)readyToUpload
{
    // can't upload if we don't have credentials
    if (!self.username || !self.password || !self.serverURL) {
        return NO;
    }

    // don't want to start a new upload if we're already going
    if ([self.uploadQueue operationCount] > 0) {
        return NO;
    }

    return YES;
}

#pragma mark - discovery

// if we don't have blobroot with which to make these requests, we need to find it first
- (void)discoveryWithUsername:(NSString *)user andPassword:(NSString *)pass
{
    self.authorizing = YES;

    NSURLSessionConfiguration *discoverConfig = [NSURLSessionConfiguration defaultSessionConfiguration];
    discoverConfig.HTTPAdditionalHeaders = @{@"Accept": @"text/x-camli-configuration", @"Authorization": [NSString stringWithFormat:@"Basic %@",[self encodedAuth]]};
    NSURLSession *discoverSession = [NSURLSession sessionWithConfiguration:discoverConfig delegate:self delegateQueue:nil];

    NSURL *discoveryURL = [self.serverURL URLByAppendingPathComponent:@"ui/"];

    NSURLSessionDataTask *data = [discoverSession dataTaskWithURL:discoveryURL completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
        self.authorizing = NO;

        if (error) {
            LALog(@"error discovery: %@",error);
        } else {

            NSHTTPURLResponse *res = (NSHTTPURLResponse *)response;

            if (res.statusCode != 200) {
                LALog(@"error with discovery: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
            } else {
                NSError *parseError;
                NSDictionary *json = [NSJSONSerialization JSONObjectWithData:data options:0 error:&parseError];

                self.blobRoot = json[@"blobRoot"];
                self.isAuthorized = YES;
                [self.uploadQueue setSuspended:NO];

                LALog(@"good discovery");
            }
        }
    }];

    [data resume];
}

#pragma mark - upload methods

- (BOOL)fileAlreadyUploaded:(LACamliFile *)file
{
    NSParameterAssert(file);
    
    if ([self.uploadedBlobRefs containsObject:file.blobRef]) {
        return YES;
    }
    
    return NO;
}

// starts uploading immediately
- (void)addFile:(LACamliFile *)file withCompletion:(void (^)())completion
{
    NSParameterAssert(file);
    
    if (![self isAuthorized]) {
        [self.uploadQueue setSuspended:YES];
        
        if (!self.authorizing) {
            [self discoveryWithUsername:self.username andPassword:self.password];
        }
    }

    LACamliUploadOperation *op = [[LACamliUploadOperation alloc] initWithFile:file andClient:self];
    op.completionBlock = ^{
        LALog(@"finished op %@",file.blobRef);
        [self.uploadedBlobRefs addObject:file.blobRef];
        [self.uploadedBlobRefs writeToFile:[self uploadedBlobRefArchivePath] atomically:YES];

        completion();
    };

    [self.uploadQueue addOperation:op];
}

- (NSURL *)statUrl
{
    return [[self.serverURL URLByAppendingPathComponent:self.blobRoot] URLByAppendingPathComponent:@"camli/stat"];
}

#pragma mark - getting stuff

- (void)getRecentItemsWithCompletion:(void (^)(NSArray *objects))completion
{
    NSMutableArray *objects = [NSMutableArray array];

    NSURL *recentUrl = [[[[self.serverURL URLByAppendingPathComponent:@"my-search"] URLByAppendingPathComponent:@"camli"] URLByAppendingPathComponent:@"search"] URLByAppendingPathComponent:@"query"];

    LALog(@"reent url: %@",recentUrl);

    NSDictionary *formData = @{@"describe": @{@"thumbnailSize": @"200"}, @"sort": @"1", @"limit": @"50", @"constraint": @{@"logical": @{@"op": @"and", @"a":@{@"camliType":@"permanode"}, @"b":@{@"permanode": @{@"modTime": @{}}}}}};

    LALog(@"form data: %@",formData);

    NSMutableURLRequest *recentRequest = [NSMutableURLRequest requestWithURL:recentUrl];
    [recentRequest setHTTPMethod:@"POST"];
    [recentRequest setHTTPBody:[NSKeyedArchiver archivedDataWithRootObject:formData]];

    NSURLSessionDataTask *recentData = [self.session dataTaskWithRequest:recentRequest completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
        LALog(@"got some response: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
    }];

    [recentData resume];

//    completion([NSArray arrayWithArray:objects]);
}

#pragma mark - utility

- (NSString *)encodedAuth
{
    NSString *auth = [NSString stringWithFormat:@"%@:%@",self.username,self.password];
    
    return [LACamliUtil base64EncodedStringFromString:auth];
}

- (NSString *)uploadedBlobRefArchivePath
{
    NSString *documents = NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES)[0];
    
    return [documents stringByAppendingPathComponent:@"uploadedRefs.plist"];
}

@end
