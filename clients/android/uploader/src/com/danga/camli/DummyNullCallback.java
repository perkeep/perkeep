package com.danga.camli;

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

    public void logToClient(String stuff) throws RemoteException {
        // TODO Auto-generated method stub

    }

    public void setBlobStatus(int done, int inFlight, int total) throws RemoteException {
        // TODO Auto-generated method stub

    }

    public void setBlobsRemain(int toUpload, int toDigest) throws RemoteException {
        // TODO Auto-generated method stub

    }

    public void setByteStatus(long done, int inFlight, long total) throws RemoteException {
        // TODO Auto-generated method stub

    }

    public void setUploading(boolean uploading) throws RemoteException {
        // TODO Auto-generated method stub

    }

    public void setUploadStatusText(String text) throws RemoteException {
        // TODO Auto-generated method stub

    }

}
