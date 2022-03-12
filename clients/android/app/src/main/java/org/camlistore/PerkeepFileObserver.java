/*
Copyright 2011 The Perkeep Authors

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

import java.io.File;
import java.nio.file.Paths;

import android.net.Uri;
import android.os.FileObserver;
import android.os.RemoteException;
import android.util.Log;

import org.camlistore.IUploadService.Stub;

public class PerkeepFileObserver extends FileObserver {
    private static final String TAG = "PerkeepFileObserver";

    private final File mDirectory;
    private final Stub mServiceStub;

    public PerkeepFileObserver(IUploadService.Stub service, File directory) {
        super(directory.getAbsolutePath(), FileObserver.CLOSE_WRITE | FileObserver.MOVED_TO);
        // TODO: Docs say: "The monitored file or directory must exist at this
        // time, or else no events will be reported (even if it appears
        // later).".  This means that a user without, say, a "gpx/" directory
        // that then goes to "Export all Tracks.." won't start them uploading.
        mDirectory = directory;
        mServiceStub = service;
        Log.d(TAG, "Starting to watch: " + mDirectory.getAbsolutePath());
        startWatching();
    }

    @Override
    public void onEvent(int event, String path) {
        if (!shouldActOnEvent(path)){
            return;
        }
        File fullFile = new File(mDirectory, path);
        Log.d(TAG, "event " + event + " for " + fullFile.getAbsolutePath());
        try {
                mServiceStub.enqueueUpload(Uri.fromFile(fullFile));
        } catch (RemoteException ignored) {
        }
    }

    private boolean shouldActOnEvent(String path) {
        // It's null for certain directory-level events.
        if (path == null) {
            return false;
        }
        // Taking a photo will generate a ".pending-*" file before moving it into the proper
        // path leading to double uploads sometimes ( race between enqueue and upload). We
        // get around that by the heuristic of ignoring ".pending" filenames here.
        if (Paths.get(path).getFileName().toString().startsWith(".pending")) {
            return false;
        }
        // act on all other events
        return true;
    }
}
