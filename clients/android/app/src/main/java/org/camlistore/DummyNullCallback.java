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

import android.os.RemoteException;

/**
 * No-op callback for service to use when it doesn't have a real callback.
 * Avoids a lot of null checks.
 */
public class DummyNullCallback extends IStatusCallback.Stub {

    private static final IStatusCallback.Stub mInstance = new DummyNullCallback();

    public static IStatusCallback.Stub instance() {
        return mInstance;
    }

    @Override
    public void logToClient(String stuff) throws RemoteException {
    }

    @Override
    public void setByteStatus(long done, int inFlight, long total) throws RemoteException {
    }

    @Override
    public void setUploading(boolean uploading) throws RemoteException {
    }

    @Override
    public void setUploadStatusText(String text) throws RemoteException {
    }

    @Override
    public void setFileStatus(int done, int inFlight, int total) throws RemoteException {
    }

    @Override
    public void setUploadStatsText(String text) throws RemoteException {
    }
}
