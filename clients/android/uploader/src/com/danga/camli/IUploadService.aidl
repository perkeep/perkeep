package com.danga.camli;

import com.danga.camli.IStatusCallback;
import android.os.ParcelFileDescriptor;

interface IUploadService {
    void registerCallback(IStatusCallback cb);
    void unregisterCallback(IStatusCallback cb);

    boolean isUploading();

    void stop();
    void resume();

    // Returns false if server not configured.
    boolean addFile(in ParcelFileDescriptor pfd);
}
