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

NSString *const CamliNotificationUploadStart = @"camli-upload-start";
NSString *const CamliNotificationUploadProgress = @"camli-upload-progress";
NSString *const CamliNotificationUploadEnd = @"camli-upload-end";

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
        self.totalUploads = 0;

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

- (void)discoveryWithUsername:(NSString *)user andPassword:(NSString *)pass
{
    self.authorizing = YES;

    NSURLSessionConfiguration *discoverConfig = [NSURLSessionConfiguration defaultSessionConfiguration];
    discoverConfig.HTTPAdditionalHeaders = @{@"Accept": @"text/x-camli-configuration", @"Authorization": [NSString stringWithFormat:@"Basic %@",[self encodedAuth]]};
    NSURLSession *discoverSession = [NSURLSession sessionWithConfiguration:discoverConfig delegate:self delegateQueue:nil];

    NSURLSessionDataTask *data = [discoverSession dataTaskWithURL:self.serverURL completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {

        if (error) {
            LALog(@"error discovery: %@",error);
        } else {
            NSHTTPURLResponse *res = (NSHTTPURLResponse *)response;

            if (res.statusCode != 200) {
                LALog(@"error with discovery: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
                [LACamliUtil logText:[NSString stringWithFormat:@"server said: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]]];

                [[NSNotificationCenter defaultCenter] postNotificationName:CamliNotificationUploadEnd object:nil];
            } else {
                NSError *err;
                NSDictionary *config = [NSJSONSerialization JSONObjectWithData:data options:0 error:&err];
                if (!err) {
                    self.blobRootComponent = config[@"blobRoot"];
                    [LACamliUtil logText:[NSString stringWithFormat:@"Welcome to %@'s camlistore",config[@"ownerName"]]];

                    self.isAuthorized = YES;
                    [self.uploadQueue setSuspended:NO];

                    LALog(@"good discovery: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
                } else {

                    LALog(@"couldn't deserialize discovery json");
                    [LACamliUtil logText:[NSString stringWithFormat:@"json from discovery: %@",err]];
                }
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

    if (self.totalUploads == 0) {
        [[NSNotificationCenter defaultCenter] postNotificationName:CamliNotificationUploadStart object:nil];
    }

    self.totalUploads++;

    if (![self isAuthorized]) {
        [self.uploadQueue setSuspended:YES];

        if (!self.authorizing) {
            [self discoveryWithUsername:self.username andPassword:self.password];
        }
    }

    LACamliUploadOperation *op = [[LACamliUploadOperation alloc] initWithFile:file andClient:self];
    __block LACamliUploadOperation *weakOp = op;
    op.completionBlock = ^{
        LALog(@"finished op %@",file.blobRef);

        if (weakOp.failedTransfer) {
            LALog(@"failed transfer");
        } else {
            [self.uploadedBlobRefs addObject:file.blobRef];
            [self.uploadedBlobRefs writeToFile:[self uploadedBlobRefArchivePath] atomically:YES];
        }
        weakOp = nil;

        // let others know about upload progress
        [[NSNotificationCenter defaultCenter] postNotificationName:CamliNotificationUploadProgress object:nil userInfo:@{@"total": @(self.totalUploads), @"remain": @([self.uploadQueue operationCount])}];

        if (![self.uploadQueue operationCount]) {
            self.totalUploads = 0;
            [[NSNotificationCenter defaultCenter] postNotificationName:CamliNotificationUploadEnd object:nil];
        }
        if (completion) {
            completion();
        }
    };

    [self.uploadQueue addOperation:op];
}

- (NSURL *)statUrl
{
    return [[self blobRoot] URLByAppendingPathComponent:@"camli/stat"];
}

#pragma mark - getting stuff

- (void)getRecentItemsWithCompletion:(void (^)(NSArray *objects))completion
{
    NSMutableArray *objects = [NSMutableArray array];

    NSURL *recentUrl = [[[[self.serverURL URLByAppendingPathComponent:@"my-search"] URLByAppendingPathComponent:@"camli"] URLByAppendingPathComponent:@"search"] URLByAppendingPathComponent:@"query"];

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

- (NSURL *)blobRoot
{
    return [self.serverURL URLByAppendingPathComponent:self.blobRootComponent];
}

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
