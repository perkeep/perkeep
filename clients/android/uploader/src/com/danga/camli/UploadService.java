package com.danga.camli;

import java.io.IOException;

import android.app.Service;
import android.content.Intent;
import android.content.SharedPreferences;
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

        // Guarded by 'this':
        private boolean mUploading = false;
        private UploadThread mUploadThread = null;


        public boolean addFile(ParcelFileDescriptor pfd) throws RemoteException {
            SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
            HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                return false;
            }

            String password = sp.getString(Preferences.PASSWORD, "");

            synchronized (this) {
                if (!mUploading) {
                    mUploading = true;
                    mUploadThread = new UploadThread(hp, password);
                    mUploadThread.start();
                }
            }
            Log.d(TAG, "addFile for " + pfd + "; size=" + pfd.getStatSize());
            try {
                pfd.close();
            } catch (IOException e) {
                // TODO Auto-generated catch block
                e.printStackTrace();
            }
            return true;
        }

        public boolean isUploading() throws RemoteException {
            synchronized (this) {
                return mUploading;
            }
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
