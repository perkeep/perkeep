/*
Copyright 2013 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

#import "TimeTravelWindowController.h"

@implementation TimeTravelWindowController

- (void)setMountPath:(NSString*)to
{
    mountPath = to;
}

- (id)initWithWindow:(NSWindow *)window
{
    self = [super initWithWindow:window];
    if (self) {
        // Initialization code here.
    }
    return self;
}

- (void)windowDidLoad
{
    [super windowDidLoad];
}

- (void)loadWindow {
    when = [NSDate date];
    [super loadWindow];
}

- (IBAction)openFinder:(id)sender
{
    NSDateFormatter *formatter = [[NSDateFormatter alloc] init];
    [formatter setTimeZone:[NSTimeZone timeZoneForSecondsFromGMT:0]];
    [formatter setDateFormat:@"yyyy-MM-dd'T'HH:mm:ss'Z'"];
    [[self window] orderOut:self];

    if (![[NSWorkspace sharedWorkspace] openFile:[NSString stringWithFormat:@"%@/at/%@",
                                                  mountPath,[formatter stringFromDate:when]]]) {
        NSRunAlertPanel(@"Cannot Open Finder Window",
                        [NSString stringWithFormat:@"Can't open path for %@.", [formatter stringFromDate:when]],
                        nil, nil, nil);
        return;
    }
}

@end
