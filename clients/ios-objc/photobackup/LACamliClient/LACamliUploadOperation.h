//
//  LACamliUploadOperation.h
//  photobackup
//
//  Created by Nick O'Neill on 11/29/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@class LACamliFile, LACamliClient;

@interface LACamliUploadOperation : NSOperation <NSURLSessionDelegate>

@property LACamliClient* client;
@property LACamliFile* file;
@property NSURLSession* session;
@property UIBackgroundTaskIdentifier taskID;

@property(readonly) BOOL failedTransfer;
@property(readonly) BOOL isExecuting;
@property(readonly) BOOL isFinished;

- (id)initWithFile:(LACamliFile*)file andClient:(LACamliClient*)client;
- (BOOL)isConcurrent;

- (NSString*)name;

@end
