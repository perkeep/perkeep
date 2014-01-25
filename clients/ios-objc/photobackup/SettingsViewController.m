//
//  SettingsViewController.m
//  photobackup
//
//  Created by Nick O'Neill on 12/16/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "SettingsViewController.h"
#import "LAViewController.h"
#import "LACamliUtil.h"
#import "LAAppDelegate.h"

@interface SettingsViewController ()

@end

@implementation SettingsViewController

- (id)initWithNibName:(NSString*)nibNameOrNil bundle:(NSBundle*)nibBundleOrNil
{
    self = [super initWithNibName:nibNameOrNil
                           bundle:nibBundleOrNil];
    if (self) {
        // Custom initialization
    }
    return self;
}

- (void)viewDidLoad
{
    [super viewDidLoad];

    NSString* serverUrl = [[NSUserDefaults standardUserDefaults] stringForKey:CamliServerKey];
    if (serverUrl) {
        self.server.text = serverUrl;
    }

    NSString* username = [[NSUserDefaults standardUserDefaults] stringForKey:CamliUsernameKey];
    if (username) {
        self.username.text = username;

        NSString* password = [LACamliUtil passwordForUsername:username];
        if (password) {
            self.password.text = password;
        }
    }
}

#pragma mark - uitextfield delegate

- (BOOL)textFieldShouldReturn:(UITextField*)textField
{
    LALog(@"text field return %@", textField);

    [self.server resignFirstResponder];
    [self.username resignFirstResponder];
    [self.password resignFirstResponder];

    if (textField == self.server) {
        [self.username becomeFirstResponder];
    } else if (textField == self.username) {
        [self.password becomeFirstResponder];
    }

    return YES;
}

#pragma mark - done

- (IBAction)validate
{
    self.errors.text = @"";

    BOOL hasErrors = NO;

    NSURL* serverUrl = [NSURL URLWithString:self.server.text];

    if (!serverUrl || !serverUrl.scheme || !serverUrl.host) {
        hasErrors = YES;
        self.errors.text = @"bad url :(";
    }

    if (!self.username.text || [self.username.text isEqualToString:@""]) {
        hasErrors = YES;
        self.errors.text = [self.errors.text stringByAppendingString:@"type a username :("];
    }

    if (!self.password.text || [self.password.text isEqualToString:@""]) {
        hasErrors = YES;
        self.errors.text = [self.errors.text stringByAppendingString:@"type a password :("];
    }

    if (!hasErrors) {
        [self saveValues];
    }
}

- (void)saveValues
{
    [LACamliUtil savePassword:self.password.text forUsername:self.username.text];

    [[NSUserDefaults standardUserDefaults] setObject:self.username.text
                                              forKey:CamliUsernameKey];
    [[NSUserDefaults standardUserDefaults] setObject:self.server.text
                                              forKey:CamliServerKey];
    [[NSUserDefaults standardUserDefaults] synchronize];

    [LACamliUtil errorText:@[
                               @""
                           ]];

    [self.parent dismissSettings];
}

- (void)didReceiveMemoryWarning
{
    [super didReceiveMemoryWarning];
    // Dispose of any resources that can be recreated.
}

@end
