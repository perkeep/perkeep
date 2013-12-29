//
//  ProgressViewController.h
//  photobackup
//
//  Created by Nick O'Neill on 12/23/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <UIKit/UIKit.h>

@interface ProgressViewController : UIViewController

@property IBOutlet UILabel *uploadLabel;
@property IBOutlet UIProgressView *uploadProgress;

@end
