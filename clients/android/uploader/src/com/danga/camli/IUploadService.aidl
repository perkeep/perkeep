package com.danga.camli;

import com.danga.camli.IStatusCallback;
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

    // Stop digesting, stop uploads, clear queues.
    void stopEverything();
    
    // For the SettingsActivity
    void setBackgroundWatchersEnabled(boolean enabled);
}
