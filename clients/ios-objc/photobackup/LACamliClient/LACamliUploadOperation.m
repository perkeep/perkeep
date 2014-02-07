//
//  LACamliUploadOperation.m
//  photobackup
//
//  Created by Nick O'Neill on 11/29/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LACamliUploadOperation.h"
#import "LACamliFile.h"
#import "LACamliClient.h"
#import "LACamliUtil.h"

static NSUInteger const camliVersion = 1;
static NSString* const multipartBoundary = @"Qe43VdbVVaGtkkMd";

@implementation LACamliUploadOperation

- (id)initWithFile:(LACamliFile*)file andClient:(LACamliClient*)client
{
    NSParameterAssert(file);
    NSParameterAssert(client);

    if (self = [super init]) {
        _file = file;
        _client = client;
        _isExecuting = NO;
        _isFinished = NO;
        _failedTransfer = NO;
        _session = [NSURLSession sessionWithConfiguration:_client.sessionConfig
                                                 delegate:self
                                            delegateQueue:nil];
    }

    return self;
}

- (BOOL)isConcurrent
{
    return YES;
}

#pragma mark - convenience

- (NSString*)name
{
    return _file.blobRef;
}

#pragma mark - operation flow

// request stats for each chunk, making sure the server doesn't already have the chunk
- (void)start
{
    [LACamliUtil statusText:@[
                                @"performing stat..."
                            ]];

    _taskID = [[UIApplication sharedApplication] beginBackgroundTaskWithName:@"uploadtask"
                                                           expirationHandler:^{
        LALog(@"upload task expired");
                                                           }];

    if (_client.backgroundID) {
        [[UIApplication sharedApplication] endBackgroundTask:_client.backgroundID];
    }

    [self willChangeValueForKey:@"isExecuting"];
    _isExecuting = YES;
    [self didChangeValueForKey:@"isExecuting"];

    NSMutableDictionary* params = [NSMutableDictionary dictionary];
    [params setObject:[NSNumber numberWithInt:camliVersion]
               forKey:@"camliversion"];

    int i = 1;
    for (NSString* blobRef in _file.allBlobRefs) {
        [params setObject:blobRef
                   forKey:[NSString stringWithFormat:@"blob%d", i]];
        i++;
    }

    NSString* formValues = @"";
    for (NSString* key in params) {
        formValues = [formValues stringByAppendingString:[NSString stringWithFormat:@"%@=%@&", key, params[key]]];
    }

    LALog(@"uploading to %@", [_client statURL]);
    NSMutableURLRequest* req = [NSMutableURLRequest requestWithURL:[_client statURL]];
    [req setHTTPMethod:@"POST"];
    [req setHTTPBody:[formValues dataUsingEncoding:NSUTF8StringEncoding]];

    NSURLSessionDataTask *statTask = [_session dataTaskWithRequest:req completionHandler:^(NSData *data, NSURLResponse *response, NSError *error)
    {

        if (!error) {
            //            LALog(@"data: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);

            // we can remove any chunks that the server claims it already has
            NSError* err;
            NSMutableDictionary* resObj = [NSJSONSerialization JSONObjectWithData:data
                                                                          options:0
                                                                            error:&err];
            if (err) {
                LALog(@"error getting json: %@", err);
            }

            if (resObj[@"stat"] != [NSNull null]) {
                for (NSDictionary* stat in resObj[@"stat"]) {
                    for (NSString* blobRef in _file.allBlobRefs) {
                        if ([stat[@"blobRef"] isEqualToString:blobRef]) {
                            [_file.uploadMarks replaceObjectAtIndex:[_file.allBlobRefs indexOfObject:blobRef]
                                                         withObject:@NO];
                        }
                    }
                }
            }

            BOOL allUploaded = YES;
            for (NSNumber* upload in _file.uploadMarks) {
                if ([upload boolValue]) {
                    allUploaded = NO;
                }
            }

            // TODO: there's a posibility all chunks have been uploaded but no permanode exists
            if (allUploaded) {
                LALog(@"everything's been uploaded already for this file");
                [LACamliUtil logText:@[
                                         @"everything already uploaded for ",
                                         _file.blobRef
                                     ]];
                [self finished];
                return;
            }

            [self uploadChunks];
        } else {
            if ([error code] == NSURLErrorNotConnectedToInternet || [error code] == NSURLErrorNetworkConnectionLost) {
                LALog(@"connection lost or unavailable");
                [LACamliUtil statusText:@[
                                            @"internet connection appears offline"
                                        ]];
            } else {
                LALog(@"failed stat: %@", error);
                [LACamliUtil errorText:@[
                                           @"failed to stat: ",
                                           [error description]
                                       ]];
                [LACamliUtil logText:@[
                                         [NSString stringWithFormat:@"failed to stat: %@", error]
                                     ]];
            }

            _failedTransfer = YES;
            [self finished];
        }
    }];

    [statTask resume];
}

- (void)uploadChunks
{
    [LACamliUtil statusText:@[
                                @"uploading..."
                            ]];

    NSMutableURLRequest* uploadReq = [NSMutableURLRequest requestWithURL:[_client uploadURL]];
    [uploadReq setHTTPMethod:@"POST"];
    [uploadReq setValue:[NSString stringWithFormat:@"multipart/form-data; boundary=%@", multipartBoundary]
        forHTTPHeaderField:@"Content-Type"];

    NSMutableData* uploadData = [self multipartDataForChunks];

    NSURLSessionUploadTask *upload = [_session uploadTaskWithRequest:uploadReq fromData:uploadData completionHandler:^(NSData *data, NSURLResponse *response, NSError *error)
    {

        //        LALog(@"upload response: %@",[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding]);

        if (error) {
            if ([error code] == NSURLErrorNotConnectedToInternet || [error code] == NSURLErrorNetworkConnectionLost) {
                LALog(@"connection lost or unavailable");
                [LACamliUtil statusText:@[
                                            @"internet connection appears offline"
                                        ]];
            } else {
                LALog(@"upload error: %@", error);
                [LACamliUtil errorText:@[
                                           @"error uploading: ",
                                           error
                                       ]];
            }
            _failedTransfer = YES;
            [self finished];
        } else {
            [self vivifyChunks];
        }
    }];

    [upload resume];
}

// ask the server to vivify the blobrefs into a file
- (void)vivifyChunks
{
    [LACamliUtil statusText:@[
                                @"vivify"
                            ]];

    NSMutableURLRequest* req = [NSMutableURLRequest requestWithURL:[_client uploadURL]];
    [req setHTTPMethod:@"POST"];
    [req setValue:[NSString stringWithFormat:@"multipart/form-data; boundary=%@", multipartBoundary]
        forHTTPHeaderField:@"Content-Type"];
    [req addValue:@"1"
        forHTTPHeaderField:@"X-Camlistore-Vivify"];

    NSMutableData* vivifyData = [self multipartVivifyDataForChunks];

    NSURLSessionUploadTask *vivify = [_session uploadTaskWithRequest:req fromData:vivifyData completionHandler:^(NSData *data, NSURLResponse *response, NSError *error)
    {
        if (error) {
            LALog(@"error vivifying: %@", error);
            [LACamliUtil errorText:@[
                                       @"error vivify: ",
                                       [error description]
                                   ]];
            _failedTransfer = YES;
        }

        [self finished];
    }];

    [vivify resume];
}

- (void)finished
{
    [LACamliUtil statusText:@[
                                @"cleaning up..."
                            ]];

    _client.backgroundID = [[UIApplication sharedApplication] beginBackgroundTaskWithName:@"queuesync"
                                                                        expirationHandler:^{
        LALog(@"queue sync task expired");
                                                                        }];

    [[UIApplication sharedApplication] endBackgroundTask:_taskID];

    LALog(@"finished op %@", _file.blobRef);

    // There's an extra retain on this operation that I cannot find,
    // this mitigates the issue so the leak is tiny
    _file.allBlobs = nil;

    [self willChangeValueForKey:@"isExecuting"];
    [self willChangeValueForKey:@"isFinished"];

    _isExecuting = NO;
    _isFinished = YES;

    [self didChangeValueForKey:@"isExecuting"];
    [self didChangeValueForKey:@"isFinished"];
}

#pragma mark - nsurlsession delegate

- (void)URLSession:(NSURLSession*)session task:(NSURLSessionTask*)task didSendBodyData:(int64_t)bytesSent totalBytesSent:(int64_t)totalBytesSent totalBytesExpectedToSend:(int64_t)totalBytesExpectedToSend
{
    if ([_client.delegate respondsToSelector:@selector(uploadProgress:
                                                         forOperation:)]) {
        float progress = (float)totalBytesSent / (float)totalBytesExpectedToSend;

        dispatch_async(dispatch_get_main_queue(), ^{
            [_client.delegate uploadProgress:progress forOperation:self];
        });
    }
}

#pragma mark - multipart bits

- (NSMutableData*)multipartDataForChunks
{
    NSMutableData* data = [NSMutableData data];

    for (NSData* chunk in [_file blobsToUpload]) {
        [data appendData:[[NSString stringWithFormat:@"--%@\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
        // server ignores this filename and mimetype, it doesn't matter what it is
        [data appendData:[[NSString stringWithFormat:@"Content-Disposition: form-data; name=\"%@\"; filename=\"image.jpg\"\r\n", [LACamliUtil blobRef:chunk]] dataUsingEncoding:NSUTF8StringEncoding]];
        [data appendData:[@"Content-Type: image/jpeg\r\n\r\n" dataUsingEncoding:NSUTF8StringEncoding]];
        [data appendData:chunk];
        [data appendData:[[NSString stringWithFormat:@"\r\n"] dataUsingEncoding:NSUTF8StringEncoding]];
    }

    [data appendData:[[NSString stringWithFormat:@"--%@--\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];

    return data;
}

- (NSMutableData*)multipartVivifyDataForChunks
{
    NSMutableData* data = [NSMutableData data];

    NSMutableDictionary* schemaBlob = [@{
                                           @"camliVersion" : @1,
                                           @"camliType" : @"file",
                                           @"unixMTime" : [LACamliUtil rfc3339StringFromDate:_file.creation],
                                           @"fileName" : _file.name
                                       } mutableCopy];

    NSMutableArray* parts = [NSMutableArray array];
    int i = 0;
    for (NSString* blobRef in _file.allBlobRefs) {
        [parts addObject:@{
                             @"blobRef" : blobRef, @"size" : [NSNumber numberWithInteger:[[_file.allBlobs objectAtIndex:i] length]]
                         }];
        i++;
    }
    [schemaBlob setObject:parts
                   forKey:@"parts"];

    NSData* schemaData = [NSJSONSerialization dataWithJSONObject:schemaBlob
                                                         options:NSJSONWritingPrettyPrinted
                                                           error:nil];

    [data appendData:[[NSString stringWithFormat:@"--%@\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:[[NSString stringWithFormat:@"Content-Disposition: form-data; name=\"%@\"; filename=\"json\"\r\n", [LACamliUtil blobRef:schemaData]] dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:[@"Content-Type: application/json\r\n\r\n" dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:schemaData];
    [data appendData:[[NSString stringWithFormat:@"\r\n"] dataUsingEncoding:NSUTF8StringEncoding]];

    [data appendData:[[NSString stringWithFormat:@"--%@--\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];

    return data;
}

@end
