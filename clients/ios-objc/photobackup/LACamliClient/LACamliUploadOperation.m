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
static NSString *const multipartBoundary = @"Qe43VdbVVaGtkkMd";

@implementation LACamliUploadOperation

- (id)initWithFile:(LACamliFile *)file andClient:(LACamliClient *)client
{
    NSParameterAssert(file);
    NSParameterAssert(client);
    
    if (self = [super init]) {
        self.file = file;
        self.client = client;
        _isExecuting = NO;
        _isFinished = NO;
    }
    
    return self;
}

- (BOOL)isConcurrent
{
    return YES;
}

// request stats for each chunk, making sure the server doesn't already have the chunk
- (void)start
{
    self.taskID =[[UIApplication sharedApplication] beginBackgroundTaskWithName:@"uploadtask" expirationHandler:^{
        LALog(@"upload task expired");
    }];

    if (self.client.backgroundID) {
        [[UIApplication sharedApplication] endBackgroundTask:self.client.backgroundID];
    }

    
    [self willChangeValueForKey:@"isExecuting"];
    _isExecuting = YES;
    [self didChangeValueForKey:@"isExecuting"];
    
    NSMutableDictionary *params = [NSMutableDictionary dictionary];
    [params setObject:[NSNumber numberWithInt:camliVersion] forKey:@"camliversion"];
    
    int i = 1;
    for (NSString *blobRef in self.file.allBlobRefs) {
        [params setObject:blobRef forKey:[NSString stringWithFormat:@"blob%d",i]];
        i++;
    }
    
    NSString *formValues = @"";
    for (NSString *key in params) {
        formValues = [formValues stringByAppendingString:[NSString stringWithFormat:@"%@=%@&",key,params[key]]];
    }
    
    NSMutableURLRequest *req = [NSMutableURLRequest requestWithURL:[self.client statUrl]];
    [req setHTTPMethod:@"POST"];
    [req setHTTPBody:[formValues dataUsingEncoding:NSUTF8StringEncoding]];
    
    NSURLSessionDataTask *statTask = [self.client.session dataTaskWithRequest:req completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
        
        if (!error) {
            LALog(@"data: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
            
            // we can remove any chunks that the server claims it already has
            NSError *err;
            NSMutableDictionary *resObj = [NSJSONSerialization JSONObjectWithData:data options:0 error:&err];
            if (err) {
                LALog(@"error serializing json: %@",err);
            }
            
            for (NSDictionary *stat in resObj[@"stat"]) {
                for (NSString *blobRef in self.file.allBlobRefs) {
                    if ([stat[@"blobRef"] isEqualToString:blobRef]) {
                        [self.file.uploadMarks replaceObjectAtIndex:[self.file.allBlobRefs indexOfObject:blobRef] withObject:@NO];
                    }
                }
            }
            
            BOOL allUploaded = YES;
            for (NSNumber *upload in self.file.uploadMarks) {
                if ([upload boolValue]) {
                    allUploaded = NO;
                }
            }
            
            // TODO: there's a posibility all chunks have been uploaded but no permanode exists
            if (allUploaded) {
                LALog(@"everything's been uploaded already for this file");
                [self finished];
                return;
            }
            
            self.client.uploadUrl = [NSURL URLWithString:resObj[@"uploadUrl"]];
            
            LALog(@"stat end");
            
            [self uploadChunks];
        } else {
            LALog(@"failed stat: %@",error);
            [self finished];
        }
    }];
    
    [statTask resume];
}

// post the chunks in a multipart request
//
- (void)uploadChunks
{
    NSMutableURLRequest *uploadReq = [NSMutableURLRequest requestWithURL:self.client.uploadUrl];
    [uploadReq setHTTPMethod:@"POST"];
    [uploadReq setValue:[NSString stringWithFormat:@"multipart/form-data; boundary=%@", multipartBoundary] forHTTPHeaderField:@"Content-Type"];
    
    NSMutableData *uploadData = [self multipartDataForChunks];
    
    NSURLSessionUploadTask *upload = [self.client.session uploadTaskWithRequest:uploadReq fromData:uploadData completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
        
//        LALog(@"upload response: %@",[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding]);
        
        if (error) {
            LALog(@"upload error: %@",error);
            [self finished];
        } else {
            [self vivifyChunks];
        }
    }];
    
    [upload resume];

}

- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didSendBodyData:(int64_t)bytesSent totalBytesSent:(int64_t)totalBytesSent totalBytesExpectedToSend:(int64_t)totalBytesExpectedToSend
{
    //    LALog(@"%lld %lld upload progress",totalBytesSent,totalBytesExpectedToSend);
}

// ask the server to vivify the blobrefs into a file
- (void)vivifyChunks
{
    NSMutableURLRequest *req = [NSMutableURLRequest requestWithURL:self.client.uploadUrl];
    [req setHTTPMethod:@"POST"];
    [req setValue:[NSString stringWithFormat:@"multipart/form-data; boundary=%@", multipartBoundary] forHTTPHeaderField:@"Content-Type"];
    [req addValue:@"1" forHTTPHeaderField:@"X-Camlistore-Vivify"];
    
    NSMutableData *vivifyData = [self multipartVivifyDataForChunks];
    
    NSURLSessionUploadTask *vivify = [self.client.session uploadTaskWithRequest:req fromData:vivifyData completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
        
//        LALog(@"response: %@",[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);
        
        if (error) {
            LALog(@"error vivifying: %@",error);
        }
        
        [self finished];
    }];
    
    [vivify resume];
}

- (void)finished
{
    self.client.backgroundID = [[UIApplication sharedApplication] beginBackgroundTaskWithName:@"queuesync" expirationHandler:^{
        LALog(@"queue sync task expired");
    }];

    [[UIApplication sharedApplication] endBackgroundTask:self.taskID];

    LALog(@"finished op %@",self.file.blobRef);

    [self willChangeValueForKey:@"isExecuting"];
    [self willChangeValueForKey:@"isFinished"];
    
    _isExecuting = NO;
    _isFinished = YES;
    
    [self didChangeValueForKey:@"isExecuting"];
    [self didChangeValueForKey:@"isFinished"];
}

#pragma mark - multipart bits

- (NSMutableData *)multipartDataForChunks
{
    NSMutableData *data = [NSMutableData data];
    
    for (NSData *chunk in [self.file blobsToUpload]) {
        [data appendData:[[NSString stringWithFormat:@"--%@\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
        // TODO change this image/jpeg to something, even though the server ignores it
        [data appendData:[[NSString stringWithFormat:@"Content-Disposition: form-data; name=\"%@\"; filename=\"image.jpg\"\r\n", [LACamliUtil blobRef:chunk]] dataUsingEncoding:NSUTF8StringEncoding]];
        [data appendData:[@"Content-Type: image/jpeg\r\n\r\n" dataUsingEncoding:NSUTF8StringEncoding]];
        [data appendData:chunk];
        [data appendData:[[NSString stringWithFormat:@"\r\n"] dataUsingEncoding:NSUTF8StringEncoding]];
    }
    
    [data appendData:[[NSString stringWithFormat:@"--%@--\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
    
    return data;
}

- (NSMutableData *)multipartVivifyDataForChunks
{
    NSMutableData *data = [NSMutableData data];
    
    NSMutableDictionary *schemaBlob = [NSMutableDictionary dictionaryWithObjectsAndKeys:@1, @"camliVersion", @"file", @"camliType", [LACamliUtil rfc3339StringFromDate:self.file.creation], @"unixMTime", nil];
    
    NSMutableArray *parts = [NSMutableArray array];
    int i = 0;
    for (NSString *blobRef in self.file.allBlobRefs) {
        [parts addObject:@{@"blobRef":blobRef,@"size":[NSNumber numberWithInteger:[[self.file.allBlobs objectAtIndex:i] length]]}];
        i++;
    }
    [schemaBlob setObject:parts forKey:@"parts"];
    
    NSData *schemaData = [NSJSONSerialization dataWithJSONObject:schemaBlob options:NSJSONWritingPrettyPrinted error:nil];
    
//    LALog(@"schema: %@",[[NSString alloc] initWithData:schemaData encoding:NSUTF8StringEncoding]);
    
    [data appendData:[[NSString stringWithFormat:@"--%@\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:[[NSString stringWithFormat:@"Content-Disposition: form-data; name=\"%@\"; filename=\"json\"\r\n", [LACamliUtil blobRef:schemaData]] dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:[@"Content-Type: application/json\r\n\r\n" dataUsingEncoding:NSUTF8StringEncoding]];
    [data appendData:schemaData];
    [data appendData:[[NSString stringWithFormat:@"\r\n"] dataUsingEncoding:NSUTF8StringEncoding]];
    
    [data appendData:[[NSString stringWithFormat:@"--%@--\r\n", multipartBoundary] dataUsingEncoding:NSUTF8StringEncoding]];
    
    return data;
}

@end
