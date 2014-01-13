//
//  LAViewController.h
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <UIKit/UIKit.h>

@class LACamliClient,ProgressViewController;

@interface LAViewController : UIViewController

@property LACamliClient *client;
@property IBOutlet UITextView *logtext;
@property ProgressViewController *progress;

- (void)dismissSettings;

@end
