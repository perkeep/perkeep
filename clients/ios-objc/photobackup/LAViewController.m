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
#import "ProgressViewController.h"

@implementation LAViewController

- (void)viewDidLoad
{
    [super viewDidLoad];

    UIBarButtonItem *settingsItem = [[UIBarButtonItem alloc] initWithBarButtonSystemItem:UIBarButtonSystemItemEdit target:self action:@selector(showSettings)];

    [self.navigationItem setRightBarButtonItem:settingsItem];

    NSURL *serverURL = [NSURL URLWithString:[[NSUserDefaults standardUserDefaults] stringForKey:CamliServerKey]];
    NSString *username = [[NSUserDefaults standardUserDefaults] stringForKey:CamliUsernameKey];

    NSString *password = nil;
    if (username) {
        password = [LACamliUtil passwordForUsername:username];
    }

    if (!serverURL || !username || !password) {
        [self showSettings];
    }

    self.progress = [[self storyboard] instantiateViewControllerWithIdentifier:@"uploadBar"];
    self.progress.view.frame = CGRectMake(0, [UIScreen mainScreen].bounds.size.height+self.progress.view.frame.size.height, self.progress.view.frame.size.width, self.progress.view.frame.size.height);

    [self.view addSubview:self.progress.view];

    [[NSNotificationCenter defaultCenter] addObserverForName:CamliNotificationUploadStart object:nil queue:nil usingBlock:^(NSNotification *note) {

        dispatch_async(dispatch_get_main_queue(), ^{
            [UIView animateWithDuration:1.0 animations:^{
                self.progress.view.frame = CGRectMake(0, [UIScreen mainScreen].bounds.size.height-self.progress.view.frame.size.height, self.progress.view.frame.size.width, self.progress.view.frame.size.height);
            }];
        });
    }];

    [[NSNotificationCenter defaultCenter] addObserverForName:CamliNotificationUploadProgress object:nil queue:nil usingBlock:^(NSNotification *note) {
        LALog(@"got progress %@ %@",note.userInfo[@"total"],note.userInfo[@"remain"]);

        NSUInteger total = [note.userInfo[@"total"] intValue];
        NSUInteger remain = [note.userInfo[@"remain"] intValue];

        dispatch_async(dispatch_get_main_queue(), ^{
            self.progress.uploadLabel.text = [NSString stringWithFormat:@"Uploading %d of %d",total-remain,total];
            self.progress.uploadProgress.progress = (float)(total-remain)/(float)total;
        });
    }];

    [[NSNotificationCenter defaultCenter] addObserverForName:CamliNotificationUploadEnd object:nil queue:nil usingBlock:^(NSNotification *note) {

        dispatch_async(dispatch_get_main_queue(), ^{
            [UIView animateWithDuration:1.0 animations:^{
                self.progress.view.frame = CGRectMake(0, [UIScreen mainScreen].bounds.size.height+self.progress.view.frame.size.height, self.progress.view.frame.size.width, self.progress.view.frame.size.height);
            }];
        });
    }];

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

- (void)dealloc
{
    [[NSNotificationCenter defaultCenter] removeObserver:self];
}

@end
