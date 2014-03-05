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
                fileExistsAtPath:[self uploadedFilenamesArchivePath]]) {
            self.uploadedFileNames = [NSMutableArray
                arrayWithContentsOfFile:[self uploadedFilenamesArchivePath]];
        }

        if (!self.uploadedFileNames) {
            self.uploadedFileNames = [NSMutableArray array];
        }

        [LACamliUtil logText:@[
                                 @"uploads in cache: ",
                                 [NSString stringWithFormat:@"%lu", (unsigned long)
                                                            [self.uploadedFileNames count]]
                             ]];

        self.uploadQueue = [[NSOperationQueue alloc] init];
        self.uploadQueue.maxConcurrentOperationCount = 1;
        self.totalUploads = 0;

        self.isAuthorized = false;
        self.authorizing = false;

        self.sessionConfig = [NSURLSessionConfiguration defaultSessionConfiguration];
        self.sessionConfig.HTTPAdditionalHeaders = @{
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
    if (!self.username || !self.password || !self.serverURL) {
        [LACamliUtil logText:@[
                                 @"not ready: no u/p/s"
                             ]];
        return NO;
    }

    // don't want to start a new upload if we're already going
    if ([self.uploadQueue operationCount] > 0) {
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
    self.authorizing = YES;

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

    NSURLSessionDataTask *data = [discoverSession dataTaskWithURL:self.serverURL completionHandler:^(NSData *data, NSURLResponse *response, NSError *error)
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

                if ([self.delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                    [self.delegate finishedDiscovery:@{
                                                     @"error" : serverSaid
                                                 }];
                }
            } else {
                NSError* err;
                NSDictionary* config = [NSJSONSerialization JSONObjectWithData:data
                                                                       options:0
                                                                         error:&err];
                if (!err) {
                    self.blobRootComponent = config[@"blobRoot"];
                    self.isAuthorized = YES;
                    [self.uploadQueue setSuspended:NO];

                    // files may have already been rejected for being previously uploaded when
                    // dicovery returns, this doesn't kick off a new check for files. The next
                    // file check will catch anything that was missed by timing

                    // if the storage generation changes, zero the saved array
                    if (![[self storageToken] isEqualToString:config[@"storageGeneration"]]) {
                        self.uploadedFileNames = [NSMutableArray array];
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

                    if ([self.delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                        [self.delegate finishedDiscovery:config];
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

                    if ([self.delegate respondsToSelector:@selector(finishedDiscovery:)]) {
                        [self.delegate finishedDiscovery:@{
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

- (BOOL)fileAlreadyUploaded:(NSString*)filename
{
    NSParameterAssert(filename);

    if ([self.uploadedFileNames containsObject:filename]) {
        return YES;
    }

    return NO;
}

// starts uploading immediately
- (void)addFile:(LACamliFile*)file withCompletion:(void (^)())completion
{
    NSParameterAssert(file);

    self.totalUploads++;

    if (![self isAuthorized]) {
        [self.uploadQueue setSuspended:YES];

        if (!self.authorizing) {
            [self discoveryWithUsername:self.username
                            andPassword:self.password];
        }
    }

    LACamliUploadOperation* op =
        [[LACamliUploadOperation alloc] initWithFile:file
                                           andClient:self];

    __block LACamliUploadOperation* weakOp = op;
    op.completionBlock = ^{
        LALog(@"finished op %@", file.blobRef);
        if ([self.delegate respondsToSelector:@selector(finishedUploadOperation:)]) {
            [self.delegate performSelector:@selector(finishedUploadOperation:)
                              onThread:[NSThread mainThread]
                            withObject:weakOp
                         waitUntilDone:NO];
        }

        if (weakOp.failedTransfer) {
            LALog(@"failed transfer");
        } else {
            [self.uploadedFileNames addObject:file.name];
            [self.uploadedFileNames writeToFile:[self uploadedFilenamesArchivePath]
                                atomically:YES];
        }

        if (![self.uploadQueue operationCount]) {
            self.totalUploads = 0;
            [LACamliUtil statusText:@[@"done uploading"]];
        }

        if (completion) {
            completion();
        }
    };

    if ([self.delegate respondsToSelector:@selector(addedUploadOperation:)]) {
        [self.delegate performSelector:@selector(addedUploadOperation:)
                          onThread:[NSThread mainThread]
                        withObject:op
                     waitUntilDone:NO];
    }

    [self.uploadQueue addOperation:op];
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
    return [self.serverURL URLByAppendingPathComponent:self.blobRootComponent];
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
    NSString* auth = [NSString stringWithFormat:@"%@:%@", self.username, self.password];

    return [LACamliUtil base64EncodedStringFromString:auth];
}

- (NSString*)uploadedFilenamesArchivePath
{
    NSString* documents = NSSearchPathForDirectoriesInDomains(
        NSDocumentDirectory, NSUserDomainMask, YES)[0];

    return [documents stringByAppendingPathComponent:@"uploadedFilenames.plist"];
}

@end
