//
//  LACamliFile.h
//
//  Created by Nick O'Neill on 1/13/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@class ALAsset;

@interface LACamliFile : NSObject

@property ALAsset* asset;
@property NSMutableArray* allBlobs;
@property NSMutableArray* uploadMarks;
@property NSArray* allBlobRefs;

@property NSString* blobRef;

- (id)initWithAsset:(ALAsset*)asset;
- (NSArray*)blobsToUpload;

- (long long)size;
- (NSString *)name;
- (NSDate*)creation;
- (UIImage*)thumbnail;

@end
