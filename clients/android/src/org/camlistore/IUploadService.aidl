/*
Copyright 2011 Google Inc.

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

package org.camlistore;

import org.camlistore.IStatusCallback;
import android.os.ParcelFileDescriptor;
import android.net.Uri;
import java.util.List;

interface IUploadService {
    void registerCallback(IStatusCallback cb);
    void unregisterCallback(IStatusCallback cb);

    int queueSize();
    boolean isUploading();

    // Returns true if thread was running and we requested it be stopped.
    boolean pause();

    // Returns true if upload wasn't already in progress and new upload
    // thread was started.
    boolean resume();

    // Enqueues a new file to be uploaded (a file:// or content:// URI).  Does disk I/O,
    // so should be called from an AsyncTask.
    // Returns false if server not configured.
    boolean enqueueUpload(in Uri uri);
    int enqueueUploadList(in List<Uri> uri);

    // Stop stop uploads, clear queues.
    void stopEverything();
    
    // For the SettingsActivity
    void setBackgroundWatchersEnabled(boolean enabled);
}
