//
//  LACamliUploadOperation.h
//  photobackup
//
//  Created by Nick O'Neill on 11/29/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@class LACamliFile,LACamliClient;

@interface LACamliUploadOperation : NSOperation

@property LACamliClient *client;
@property LACamliFile *file;
@property UIBackgroundTaskIdentifier taskID;

@property (readonly) BOOL isExecuting;
@property (readonly) BOOL isFinished;

- (BOOL)isConcurrent;
- (id)initWithFile:(LACamliFile *)file andClient:(LACamliClient *)client;

@end
