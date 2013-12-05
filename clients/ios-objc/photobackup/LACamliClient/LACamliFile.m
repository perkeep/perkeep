//
//  LACamliFile.m
//
//  Created by Nick O'Neill on 1/13/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LACamliFile.h"
#import "LACamliUtil.h"
#import <AssetsLibrary/AssetsLibrary.h>

@implementation LACamliFile

@synthesize allBlobs = _allBlobs;
@synthesize allBlobRefs = _allBlobRefs;

static NSUInteger const ChunkSize = 64000;

- (id)initWithAsset:(ALAsset *)asset
{
    if (self = [super init]) {
        self.asset = asset;
        
        [self setBlobRef:[LACamliUtil blobRef:[self fileData]]];
        
        float chunkCount = (float)[self size] / (float)ChunkSize;

        self.uploadMarks = [NSMutableArray array];
        for (int i = 0; i < chunkCount; i++) {
            [self.uploadMarks addObject:@YES];
        }
    }
    
    return self;
}

- (id)initWithPath:(NSString *)path
{
    // TODO, can init from random path to file
    
    if (self = [super init]) {
//        [self setBlobRef:[LACamliClient blobRef:data]];
//        [self setFileData:data];
        
        // set time, size and other properties here?
    }
    
    return self;
}

#pragma mark - convenience

- (NSData *)fileData
{
    ALAssetRepresentation *rep = [self.asset defaultRepresentation];
    Byte *buf = (Byte*)malloc((int)rep.size);
    NSUInteger bufferLength = [rep getBytes:buf fromOffset:0.0 length:(int)rep.size error:nil];
    
    return [NSData dataWithBytesNoCopy:buf length:bufferLength freeWhenDone:YES];
}

- (long long)size
{
    return [self.asset defaultRepresentation].size;
}

- (NSDate *)creation
{
    return [self.asset valueForProperty:ALAssetPropertyDate];
}

- (NSArray *)blobsToUpload
{
    NSMutableArray *blobs = [NSMutableArray array];
    
    int i = 0;
    for (NSData *blob in self.allBlobs) {
        if ([[self.uploadMarks objectAtIndex:i] boolValue]) {
            [blobs addObject:blob];
        }
        i++;
    }
    
    return blobs;
}

#pragma mark - delayed creation methods

- (void)setAllBlobs:(NSMutableArray *)allBlobs
{
    _allBlobs = allBlobs;
}

- (NSMutableArray *)allBlobs
{
    if (!_allBlobs) {
        [self makeBlobsAndRefs];
    }

    // not a huge fan of how this doesn't obviously assign to _allBlobs
    return _allBlobs;
}

- (void)setAllBlobRefs:(NSArray *)allBlobRefs
{
    _allBlobRefs = allBlobRefs;
}

- (NSArray *)allBlobRefs
{
    if (!_allBlobRefs) {
        [self makeBlobsAndRefs];
    }
    
    // not a huge fan of how this doesn't obviously assign to _allBlobRefs
    return _allBlobRefs;
}


- (void)makeBlobsAndRefs
{
    LALog(@"making blob refs");
    
    NSMutableArray *chunks = [NSMutableArray array];
    NSMutableArray *blobRefs = [NSMutableArray array];

    float chunkCount = (float)self.size / (float)ChunkSize;

    NSData *fileData = [self fileData];
    
    for (int i = 0; i < chunkCount; i++) {
        
        // ChunkSize size chunks, unless the last one is less
        NSData *chunkData;
        if (ChunkSize*(i+1) <= [self size]) {
            chunkData = [fileData subdataWithRange:NSMakeRange(ChunkSize*i, ChunkSize)];
        } else {
            chunkData = [fileData subdataWithRange:NSMakeRange(ChunkSize*i, (int)[self size]-(ChunkSize*i))];
        }
        
        [chunks addObject:chunkData];
        [blobRefs addObject:[LACamliUtil blobRef:chunkData]];
    }
    
    self.allBlobs = chunks;
    self.allBlobRefs = blobRefs;
}

@end
