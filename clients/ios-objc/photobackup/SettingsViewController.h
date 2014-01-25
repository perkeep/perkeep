//
//  SettingsViewController.h
//  photobackup
//
//  Created by Nick O'Neill on 12/16/13.
//  Copyright (c) 2013 Nick O'Neill. All rights reserved.
//

#import <UIKit/UIKit.h>

@class LAViewController;

@interface SettingsViewController : UIViewController

@property(weak) LAViewController* parent;
@property IBOutlet UILabel* errors;
@property IBOutlet UITextField* server;
@property IBOutlet UITextField* username;
@property IBOutlet UITextField* password;

- (IBAction)validate;

@end
