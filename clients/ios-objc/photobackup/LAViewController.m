//
//  LAViewController.m
//  photobackup
//
//  Created by Nick O'Neill on 10/20/13.
//  Copyright (c) 2013 The Camlistore Authors. All rights reserved.
//

#import "LAViewController.h"
#import "LACamliFile.h"
#import <AssetsLibrary/AssetsLibrary.h>

@interface LAViewController ()

@end

@implementation LAViewController

- (void)viewDidLoad
{
    [super viewDidLoad];
    
    NSString *credentialsPath = [[NSBundle mainBundle] pathForResource:@"credentials" ofType:@"plist"];
    NSDictionary *credentials = [NSDictionary dictionaryWithContentsOfFile:credentialsPath];
    
    NSAssert(credentials[@"camlistore_url"], @"no camlistore url specified");
    NSAssert(credentials[@"camlistore_username"], @"no camlistore username specified");
    NSAssert(credentials[@"camlistore_password"], @"no camlistore password specified");
    
    self.client = [[LACamliClient alloc] initWithServer:[NSURL URLWithString:credentials[@"camlistore_url"]] username:credentials[@"camlistore_username"] andPassword:credentials[@"camlistore_password"]];
    
    NSUInteger __block filesToUpload = 0;
    
    self.library = [[ALAssetsLibrary alloc] init];
    [self.library enumerateGroupsWithTypes:ALAssetsGroupSavedPhotos usingBlock:^(ALAssetsGroup *group, BOOL *stop) {
        LALog(@"group: %@",group);
        [group enumerateAssetsUsingBlock:^(ALAsset *result, NSUInteger index, BOOL *stop) {
            LALog(@"asset: %@",result);
            
            if (result && [result valueForProperty:ALAssetPropertyType] != ALAssetTypeVideo) { // enumerate returns null after the last item
                LACamliFile *file = [[LACamliFile alloc] initWithAsset:result];
                
                if (![self.client fileAlreadyUploaded:file]) {
                    filesToUpload++;
                    [self.client addFile:file];
                } else {
                    LALog(@"file already uploaded: %@",file.blobRef);
                }
            }
        }];
    } failureBlock:^(NSError *error) {
        LALog(@"failed enumerate: %@",error);
    }];
    
    // TODO: set badge number to filesToUpload
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
