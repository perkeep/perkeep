//
//  UploadTaskCell.h
//  photobackup
//
//  Created by Nick O'Neill on 1/6/14.
//  Copyright (c) 2014 Nick O'Neill. All rights reserved.
//

#import <UIKit/UIKit.h>

@interface UploadTaskCell : UITableViewCell

@property IBOutlet UILabel* displayText;
@property IBOutlet UIImageView* preview;
@property IBOutlet UIProgressView* progress;

@end
