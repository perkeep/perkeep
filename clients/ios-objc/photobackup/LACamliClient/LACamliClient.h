//
//  LACamliClient.h
//
//  Created by Nick O'Neill on 1/10/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@class LACamliFile;

@interface LACamliClient : NSObject <NSURLSessionDelegate>

@property NSURLSession *session;

@property NSURL *serverURL;
@property NSString *username;
@property NSString *password;

@property NSString *blobRoot;
@property NSURL *uploadUrl;
@property NSOperationQueue *uploadQueue;

@property NSMutableArray *uploadedBlobRefs;
@property UIBackgroundTaskIdentifier backgroundID;

@property BOOL isAuthorized;
@property BOOL authorizing;

- (id)initWithServer:(NSURL *)server username:(NSString *)username andPassword:(NSString *)password;

- (void)discoveryWithUsername:(NSString *)user andPassword:(NSString *)pass;

- (BOOL)fileAlreadyUploaded:(LACamliFile *)file;
- (void)addFile:(LACamliFile *)file withCompletion:(void (^)())completion;

- (NSURL *)statUrl;

@end
