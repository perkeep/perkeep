package com.danga.camli;

import java.io.FileNotFoundException;
import java.util.HashSet;
import java.util.LinkedList;
import java.util.Set;

import android.app.Service;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.IBinder;
import android.os.ParcelFileDescriptor;
import android.os.PowerManager;
import android.os.RemoteException;
import android.util.Log;

public class UploadService extends Service {
    private static final String TAG = "UploadService";

    // Everything in this block guarded by 'this':
    private boolean mUploading = false; // user's desired state (notified
                                        // quickly)
    private UploadThread mUploadThread = null; // last thread created; null when
                                               // thread exits
    final Set<QueuedFile> mQueueSet = new HashSet<QueuedFile>();
    final LinkedList<QueuedFile> mQueueList = new LinkedList<QueuedFile>();
    private IStatusCallback mCallback = DummyNullCallback.instance();
    private String mLastUploadStatusText = null;

    // Effectively final, initialized in onCreate():
    PowerManager mPowerManager;
    WifiManager mWifiManager;

    @Override
    public void onCreate() {
        super.onCreate();
        mPowerManager = (PowerManager) getSystemService(Context.POWER_SERVICE);
        mWifiManager = (WifiManager) getSystemService(Context.WIFI_SERVICE);
    }

	@Override
    public IBinder onBind(Intent intent) {
        return service;
	}

    // Called by UploadThread to get stuff to do. Caller owns the returned new
    // LinkedList. Doesn't return null.
    LinkedList<QueuedFile> uploadQueue() {
        synchronized (this) {
            LinkedList<QueuedFile> copy = new LinkedList<QueuedFile>();
            copy.addAll(mQueueList);
            return copy;
        }
    }

    void setUploadStatusText(String status) {
        IStatusCallback cb;
        synchronized (this) {
            mLastUploadStatusText = status;
            cb = mCallback;
        }
        try {
            cb.setUploadStatusText(status);
        } catch (RemoteException e) {
        }
    }

    private void onUploadThreadEnded() {
        synchronized (this) {
            Log.d(TAG, "UploadThread ended.");
            mUploadThread = null;
            mUploading = false;
            try {
                mCallback.setUploading(false);
            } catch (RemoteException e) {
            }
        }
    }

    void onUploadComplete(QueuedFile qf, boolean wasAlreadyExisting) {
        synchronized (this) {
            mQueueSet.remove(qf);
            mQueueList.remove(qf); // TODO: ghetto, linear scan
            try {
                mCallback.setBlobsRemain(mQueueSet.size());
            } catch (RemoteException e) {
            }
        }
    }

    ParcelFileDescriptor getFileDescriptor(Uri uri) {
        ContentResolver cr = getContentResolver();
        try {
            return cr.openFileDescriptor(uri, "r");
        } catch (FileNotFoundException e) {
            Log.w(TAG, "FileNotFound for " + uri, e);
            return null;
        }
    }

    private final IUploadService.Stub service = new IUploadService.Stub() {

        public boolean enqueueUpload(Uri uri) throws RemoteException {
            SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
            HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                return false;
            }

            ParcelFileDescriptor pfd = getFileDescriptor(uri);

            String sha1 = Util.getSha1(pfd.getFileDescriptor());
            QueuedFile qf = new QueuedFile(sha1, uri, pfd.getStatSize());

            int remain = 0;
            boolean needResume = false;
            synchronized (UploadService.this) {
                if (mQueueSet.contains(qf)) {
                    Log.d(TAG, "Dup blob enqueue, ignoring " + qf);
                    return false;
                }
                Log.d(TAG, "Enqueueing blob: " + qf);
                mQueueSet.add(qf);
                mQueueList.add(qf);
                remain = mQueueSet.size();
                needResume = !mUploading;
            }
            mCallback.setBlobsRemain(remain);
            if (needResume) {
                resume();
            }
            return true;
        }

        public boolean isUploading() throws RemoteException {
            synchronized (UploadService.this) {
                return mUploading;
            }
        }

        public void registerCallback(IStatusCallback cb) throws RemoteException {
            // TODO: permit multiple listeners? when need comes.
            synchronized (UploadService.this) {
                mCallback = cb != null ? cb : DummyNullCallback.instance();

                // Init the new connection.
                mCallback.setBlobsRemain(mQueueSet.size());
                mCallback.setUploading(mUploading);
                mCallback.setUploadStatusText(mLastUploadStatusText);
            }
        }

        public void unregisterCallback(IStatusCallback cb) throws RemoteException {
            synchronized (UploadService.this) {
                mCallback = DummyNullCallback.instance();
            }
        }

        public boolean resume() throws RemoteException {
            SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
            HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                return false;
            }
            String password = sp.getString(Preferences.PASSWORD, "");

            final PowerManager.WakeLock wakeLock = mPowerManager.newWakeLock(
                    PowerManager.PARTIAL_WAKE_LOCK, "Camli Upload");
            final WifiManager.WifiLock wifiLock = mWifiManager.createWifiLock(
                    WifiManager.WIFI_MODE_FULL, "Camli Upload");

            synchronized (UploadService.this) {
                if (mUploadThread != null) {
                    return false;
                }

                wakeLock.acquire();
                wifiLock.acquire();

                mUploading = true;
                mUploadThread = new UploadThread(UploadService.this, hp,
                        password);

                // Start a thread to release the wakelock...
                final Thread threadToWatch = mUploadThread;
                new Thread() {
                    @Override public void run() {
                        try {
                            threadToWatch.join();
                        } catch (InterruptedException e) {
                        }
                        Log.d(TAG, "UploadThread done; releasing the wakelock");
                        wakeLock.release();
                        wifiLock.release();
                        onUploadThreadEnded();
                    }
                }.start();
                mUploadThread.start();
            }
            mCallback.setUploading(true);
            return true;
        }

        public boolean pause() throws RemoteException {
            synchronized (UploadService.this) {
                if (mUploadThread != null) {
                    mUploadThread.stopPlease();
                    mUploading = false;
                    mCallback.setUploading(false);
                    return true;
                }
                return false;
            }
        }

        public int queueSize() throws RemoteException {
            synchronized (UploadService.this) {
                return mQueueList.size();
            }
        }
    };
}
