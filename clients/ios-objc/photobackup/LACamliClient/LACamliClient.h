//
//  LACamliClient.h
//
//  Created by Nick O'Neill on 1/10/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@class LACamliFile, LACamliUploadOperation;

@protocol LACamliStatusDelegate <NSObject>

@optional
- (void)finishedDiscovery:(NSDictionary*)config;
- (void)addedUploadOperation:(LACamliUploadOperation*)op;
- (void)finishedUploadOperation:(LACamliUploadOperation*)op;
- (void)uploadProgress:(float)pct forOperation:(LACamliUploadOperation*)op;
@end

@interface LACamliClient : NSObject <NSURLSessionDelegate>

@property NSURLSessionConfiguration* sessionConfig;
@property id delegate;

@property NSURL* serverURL;
@property NSString* username;
@property NSString* password;

@property NSString* blobRootComponent;
@property NSOperationQueue* uploadQueue;
@property NSUInteger totalUploads;

@property NSMutableArray* uploadedFileNames;
@property UIBackgroundTaskIdentifier backgroundID;

@property BOOL isAuthorized;
@property BOOL authorizing;

- (id)initWithServer:(NSURL*)server username:(NSString*)username andPassword:(NSString*)password;
- (BOOL)readyToUpload;
- (void)discoveryWithUsername:(NSString*)user andPassword:(NSString*)pass;

- (BOOL)fileAlreadyUploaded:(NSString*)filename;
- (void)addFile:(LACamliFile*)file withCompletion:(void (^)())completion;

- (NSURL*)statURL;
- (NSURL*)uploadURL;

@end
