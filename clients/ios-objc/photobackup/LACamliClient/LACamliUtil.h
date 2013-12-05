//
//  LACamliUtil.h
//  photobackup
//
//  Created by Nick O'Neill on 11/29/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@interface LACamliUtil : NSObject

+ (NSString *)base64EncodedStringFromString:(NSString *)string;
+ (NSString *)blobRef:(NSData *)data;
+ (NSString *)rfc3339StringFromDate:(NSDate *)date;

@end
