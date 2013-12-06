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

- (BOOL)fileAlreadyUploaded:(LACamliFile *)file
{
    NSParameterAssert(file);
    
    if ([self.uploadedBlobRefs containsObject:file.blobRef]) {
        return YES;
    }
    
    return NO;
}

// starts uploading immediately
- (void)addFile:(LACamliFile *)file
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
    };
    
    [self.uploadQueue addOperation:op];
}

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

- (NSURL *)statUrl
{
    return [[self.serverURL URLByAppendingPathComponent:self.blobRoot] URLByAppendingPathComponent:@"camli/stat"];
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
