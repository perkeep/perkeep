//
//  LAViewController.h
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import <UIKit/UIKit.h>
#import "LACamliClient.h"

@class ProgressViewController;

@interface LAViewController : UIViewController <UITableViewDataSource, UITableViewDelegate, LACamliStatusDelegate>

@property IBOutlet UITableView* table;
@property NSMutableArray* operations;
@property ProgressViewController* progress;

- (void)dismissSettings;

@end
