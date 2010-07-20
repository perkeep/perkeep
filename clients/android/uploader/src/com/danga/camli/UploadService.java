package com.danga.camli;

import java.io.IOException;

import android.app.Service;
import android.content.Intent;
import android.os.IBinder;
import android.os.ParcelFileDescriptor;
import android.os.RemoteException;
import android.util.Log;

public class UploadService extends Service {
    private static final String TAG = "UploadService";

	@Override
    public IBinder onBind(Intent intent) {
        return service;
	}

    private final IUploadService.Stub service = new IUploadService.Stub() {

        public void addFile(ParcelFileDescriptor pfd) throws RemoteException {
            Log.d(TAG, "addFile for " + pfd + "; size=" + pfd.getStatSize());
            try {
                pfd.close();
            } catch (IOException e) {
                // TODO Auto-generated catch block
                e.printStackTrace();
            }
        }

        public boolean isUploading() throws RemoteException {
            // TODO Auto-generated method stub
            return false;
        }

        public void registerCallback(IStatusCallback cb) throws RemoteException {
            // TODO Auto-generated method stub

        }

        public void resume() throws RemoteException {
            // TODO Auto-generated method stub

        }

        public void stop() throws RemoteException {
            // TODO Auto-generated method stub

        }

        public void unregisterCallback(IStatusCallback cb)
                throws RemoteException {
            // TODO Auto-generated method stub

        }
    };
}
