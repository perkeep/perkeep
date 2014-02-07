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

NSString* const CamliStorageGenerationKey = @"org.camlistore.storagetoken";

- (id)initWithServer:(NSURL*)server
            username:(NSString*)username
         andPassword:(NSString*)password
{
    NSParameterAssert(server);
    NSParameterAssert(username);
    NSParameterAssert(password);

    if (self = [super init]) {
        _serverURL = server;
        _username = username;
        _password = password;

        if ([[NSFileManager defaultManager]
                fileExistsAtPath:[self uploadedBlobRefArchivePath]]) {
            _uploadedBlobRefs = [NSMutableArray
                arrayWithContentsOfFile:[self uploadedBlobRefArchivePath]];
        }

        if (!_uploadedBlobRefs) {
            _uploadedBlobRefs = [NSMutableArray array];
        }

        [LACamliUtil logText:@[
                                 @"uploads in cache: ",
                                 [NSString stringWithFormat:@"%lu", (unsigned long)
                                                            [_uploadedBlobRefs count]]
                             ]];

        _uploadQueue = [[NSOperationQueue alloc] init];
        _uploadQueue.maxConcurrentOperationCount = 1;
        _totalUploads = 0;

        _isAuthorized = false;
        _authorizing = false;

        _sessionConfig = [NSURLSessionConfiguration defaultSessionConfiguration];
        _sessionConfig.HTTPAdditionalHeaders = @{
            @"Authorization" :
            [NSString stringWithFormat:@"Basic %@", [self encodedAuth]]
        };
    }

    return self;
}

#pragma mark - ready state

- (BOOL)readyToUpload
{
    // can't upload if we don't have credentials
    if (!_username || !_password || !_serverURL) {
        [LACamliUtil logText:@[
                                 @"not ready: no u/p/s"
                             ]];
        return NO;
    }

    // don't want to start a new upload if we're already going
    if ([_uploadQueue operationCount] > 0) {
        [LACamliUtil logText:@[
                                 @"not ready: already uploading"
                             ]];
        return NO;
    }

    [LACamliUtil logText:@[
                             @"starting upload"
                         ]];
    return YES;
}

#pragma mark - discovery

// discovery is done on demand when we have a new file to upload
- (void)discoveryWithUsername:(NSString*)user andPassword:(NSString*)pass
{
    [LACamliUtil statusText:@[
                                @"discovering..."
                            ]];
    _authorizing = YES;

    NSURLSessionConfiguration* discoverConfig =
        [NSURLSessionConfiguration defaultSessionConfiguration];
    discoverConfig.HTTPAdditionalHeaders = @{
        @"Accept" : @"text/x-camli-configuration",
        @"Authorization" :
        [NSString stringWithFormat:@"Basic %@", [self encodedAuth]]
    };
    NSURLSession* discoverSession =
        [NSURLSession sessionWithConfiguration:discoverConfig
                                      delegate:self
                                 delegateQueue:nil];

    NSURLSessionDataTask *data = [discoverSession dataTaskWithURL:_serverURL completionHandler:^(NSData *data, NSURLResponse *response, NSError *error)
    {

        if (error) {
            if ([error code] == NSURLErrorNotConnectedToInternet || [error code] == NSURLErrorNetworkConnectionLost) {
                LALog(@"connection lost or unavailable");
                [LACamliUtil statusText:@[
                                            @"internet connection appears offline"
                                        ]];
            } else if ([error code] == NSURLErrorCannotConnectToHost || [error code] == NSURLErrorCannotFindHost) {
                LALog(@"can't connect to server");
                [LACamliUtil statusText:@[
                                            @"can't connect to server"
                                        ]];

            } else {
                LALog(@"error discovery: %@", error);
                [LACamliUtil errorText:@[
                                           @"discovery error: ",
                                           [error description]
                                       ]];
            }

        } else {
            NSHTTPURLResponse* res = (NSHTTPURLResponse*)response;

            if (res.statusCode != 200) {
                NSString* serverSaid = [[NSString alloc]
                    initWithData:data
                        encoding:NSUTF8StringEncoding];

                [LACamliUtil
                    errorText:@[
                                  @"error discovery: ",
                                  serverSaid
                              ]];
                [LACamliUtil
                    logText:@[
                                [NSString stringWithFormat:
                                              @"server said: %@",
                                              serverSaid]
                            ]];

                if ([_delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                    [_delegate finishedDiscovery:@{
                                                     @"error" : serverSaid
                                                 }];
                }
            } else {
                NSError* err;
                NSDictionary* config = [NSJSONSerialization JSONObjectWithData:data
                                                                       options:0
                                                                         error:&err];
                if (!err) {
                    _blobRootComponent = config[@"blobRoot"];
                    _isAuthorized = YES;
                    [_uploadQueue setSuspended:NO];

                    // files may have already been rejected for being previously uploaded when
                    // dicovery returns, this doesn't kick off a new check for files. The next
                    // file check will catch anything that was missed by timing

                    // if the storage generation changes, zero the saved array
                    if (![[self storageToken] isEqualToString:config[@"storageGeneration"]]) {
                        _uploadedBlobRefs = [NSMutableArray array];
                        [self saveStorageToken:config[@"storageGeneration"]];
                    }

                    [LACamliUtil
                        logText:
                            @[
                                [NSString stringWithFormat:@"Welcome to %@'s camlistore",
                                                           config[@"ownerName"]]
                            ]];

                    [LACamliUtil statusText:@[
                                                @"discovery OK"
                                            ]];

                    if ([_delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                        [_delegate finishedDiscovery:config];
                    }
                } else {
                    [LACamliUtil
                        errorText:@[
                                      @"bad json from discovery",
                                      [err description]
                                  ]];
                    [LACamliUtil
                        logText:@[
                                    @"json from discovery: ",
                                    [err description]
                                ]];

                    if ([_delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                        [_delegate finishedDiscovery:@{
                                                         @"error" : [err description]
                                                     }];
                    }
                }
            }
        }
    }];

    [data resume];
}

#pragma mark - upload methods

- (BOOL)fileAlreadyUploaded:(LACamliFile*)file
{
    NSParameterAssert(file);

    if ([_uploadedBlobRefs containsObject:file.blobRef]) {
        return YES;
    }

    // also check to make sure it's not in the queue waiting
    for (LACamliUploadOperation* op in [_uploadQueue operations]) {
        if ([op.file.blobRef isEqualToString:file.blobRef]) {
            return YES;
        }
    }

    return NO;
}

// starts uploading immediately
- (void)addFile:(LACamliFile*)file withCompletion:(void (^)())completion
{
    NSParameterAssert(file);

    _totalUploads++;

    if (![self isAuthorized]) {
        [_uploadQueue setSuspended:YES];

        if (!_authorizing) {
            [self discoveryWithUsername:_username
                            andPassword:_password];
        }
    }

    LACamliUploadOperation* op =
        [[LACamliUploadOperation alloc] initWithFile:file
                                           andClient:self];

    __block LACamliUploadOperation* weakOp = op;
    op.completionBlock = ^{
        LALog(@"finished op %@", file.blobRef);
        if ([_delegate respondsToSelector:@selector(finishedUploadOperation:)]) {
            [_delegate performSelector:@selector(finishedUploadOperation:)
                              onThread:[NSThread mainThread]
                            withObject:weakOp
                         waitUntilDone:NO];
        }

        if (weakOp.failedTransfer) {
            LALog(@"failed transfer");
        } else {
            [_uploadedBlobRefs addObject:file.blobRef];
            [_uploadedBlobRefs writeToFile:[self uploadedBlobRefArchivePath]
                                atomically:YES];
        }

        if (![_uploadQueue operationCount]) {
            _totalUploads = 0;
            [LACamliUtil statusText:@[@"done uploading"]];
        }

        if (completion) {
            completion();
        }
    };

    if ([_delegate respondsToSelector:@selector(addedUploadOperation:)]) {
        [_delegate performSelector:@selector(addedUploadOperation:)
                          onThread:[NSThread mainThread]
                        withObject:op
                     waitUntilDone:NO];
    }

    [_uploadQueue addOperation:op];
}

#pragma mark - utility

- (NSString*)storageToken
{
    NSUserDefaults* defaults = [NSUserDefaults standardUserDefaults];
    if ([defaults objectForKey:CamliStorageGenerationKey]) {
        return [defaults objectForKey:CamliStorageGenerationKey];
    }

    return nil;
}

- (void)saveStorageToken:(NSString*)token
{
    NSUserDefaults* defaults = [NSUserDefaults standardUserDefaults];
    [defaults setObject:token
                 forKey:CamliStorageGenerationKey];
    [defaults synchronize];
}

- (NSURL*)blobRoot
{
    return [_serverURL URLByAppendingPathComponent:_blobRootComponent];
}

- (NSURL*)statURL
{
    return [[self blobRoot] URLByAppendingPathComponent:@"camli/stat"];
}

- (NSURL*)uploadURL
{
    return [[self blobRoot] URLByAppendingPathComponent:@"camli/upload"];
}

- (NSString*)encodedAuth
{
    NSString* auth = [NSString stringWithFormat:@"%@:%@", _username, _password];

    return [LACamliUtil base64EncodedStringFromString:auth];
}

- (NSString*)uploadedBlobRefArchivePath
{
    NSString* documents = NSSearchPathForDirectoriesInDomains(
        NSDocumentDirectory, NSUserDomainMask, YES)[0];

    return [documents stringByAppendingPathComponent:@"uploadedRefs.plist"];
}

@end
