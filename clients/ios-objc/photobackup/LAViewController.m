//
//  LAViewController.m
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LAViewController.h"
#import "LACamliClient.h"
#import "LAAppDelegate.h"
#import "LACamliUtil.h"
#import "SettingsViewController.h"

@interface LAViewController ()

@end

@implementation LAViewController

- (void)viewDidLoad
{
    [super viewDidLoad];

    UIBarButtonItem *settingsItem = [[UIBarButtonItem alloc] initWithBarButtonSystemItem:UIBarButtonSystemItemEdit target:self action:@selector(showSettings)];

    [self.navigationItem setRightBarButtonItem:settingsItem];

    // show the
    NSURL *serverURL = [NSURL URLWithString:[[NSUserDefaults standardUserDefaults] stringForKey:CamliServerKey]];
    NSString *username = [[NSUserDefaults standardUserDefaults] stringForKey:CamliUsernameKey];

    NSString *password = nil;
    if (username) {
        password = [LACamliUtil passwordForUsername:username];
    }

    if (!serverURL || !username || !password) {
        [self showSettings];
    }

//    [self.client getRecentItemsWithCompletion:^(NSArray *objects) {
//        LALog(@"got objects: %@",objects);
//    }];
}

- (void)showSettings
{
    SettingsViewController *settings = [self.storyboard instantiateViewControllerWithIdentifier:@"settings"];
    [settings setParent:self];

    [self presentViewController:settings animated:YES completion:nil];
}

- (void)dismissSettings
{
    [self dismissViewControllerAnimated:YES completion:nil];

    [(LAAppDelegate *)[[UIApplication sharedApplication] delegate] loadCredentials];
}

#pragma mark - collection methods

- (NSInteger)collectionView:(UICollectionView *)collectionView numberOfItemsInSection:(NSInteger)section
{
    return 5;
}

- (NSInteger)numberOfSectionsInCollectionView:(UICollectionView *)collectionView
{
    return 1;
}

- (UICollectionViewCell *)collectionView:(UICollectionView *)collectionView cellForItemAtIndexPath:(NSIndexPath *)indexPath
{
    UICollectionViewCell *cell = [collectionView dequeueReusableCellWithReuseIdentifier:@"collectionCell" forIndexPath:indexPath];
    
    cell.backgroundColor = [UIColor redColor];
    
    return cell;
}

- (void)didReceiveMemoryWarning
{
    [super didReceiveMemoryWarning];
    // Dispose of any resources that can be recreated.
}

@end
