package com.danga.camli;

import java.io.FileNotFoundException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import android.app.Service;
import android.content.ContentResolver;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.Uri;
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
        private final Set<Uri> mEnqueuedUri = new HashSet<Uri>();
        private final List<Uri> mUriList = new ArrayList<Uri>();

        public boolean enqueueUpload(Uri uri) throws RemoteException {
            SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
            HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                return false;
            }

            ContentResolver cr = getContentResolver();
            ParcelFileDescriptor pfd = null;
            try {
                pfd = cr.openFileDescriptor(uri, "r");
            } catch (FileNotFoundException e) {
                Log.w(TAG, "FileNotFound for " + uri, e);
                return false;
            }

            String sha1 = Util.getSha1(pfd.getFileDescriptor());
            Log.d(TAG, "sha1 of file is: " + sha1);
            Log.d(TAG, "size of file is: " + pfd.getStatSize());

            synchronized (this) {
                if (mEnqueuedUri.contains(uri)) {
                    return false;
                }
                mEnqueuedUri.add(uri);
                mUriList.add(uri);
                if (!mUploading) {
                    resume();
                }
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

        public boolean resume() throws RemoteException {
            synchronized (this) {
                if (mUploadThread != null) {
                    return false;
                }
                mUploading = true;

                SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
                HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
                if (!hp.isValid()) {
                    return false;
                }
                String password = sp.getString(Preferences.PASSWORD, "");

                mUploadThread = new UploadThread(hp, password);
                mUploadThread.start();
                return true;
            }
        }

        public boolean pause() throws RemoteException {
            synchronized (this) {
                if (mUploadThread != null) {
                    mUploadThread.stopPlease();
                    return true;
                }
                return false;
            }
        }

        public void unregisterCallback(IStatusCallback cb)
                throws RemoteException {
            // TODO Auto-generated method stub

        }

        public int queueSize() throws RemoteException {
            synchronized (this) {
                return mUriList.size();
            }
        }
    };
}
