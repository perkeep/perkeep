//
//  LACamliRecentFile.h
//  photobackup
//
//  Created by Nick O'Neill on 12/4/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <Foundation/Foundation.h>

@interface LACamliRecentFile : NSObject

@property NSString *blobRef;

- (NSString *)localThumbPath;

@end
