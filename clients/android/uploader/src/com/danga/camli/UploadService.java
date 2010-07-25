package com.danga.camli;

import java.io.FileNotFoundException;
import java.util.HashSet;
import java.util.LinkedList;
import java.util.List;
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
    private final Set<QueuedFile> mQueueSet = new HashSet<QueuedFile>();
    private final LinkedList<QueuedFile> mQueueList = new LinkedList<QueuedFile>();
    private IStatusCallback mCallback = DummyNullCallback.instance();
    private String mLastUploadStatusText = null;
    private int mBytesInFlight = 0;
    private int mBlobsInFlight = 0;

    // Stats, all guarded by 'this', and all reset to 0 on queue size transition from 0 -> 1.
    private long mBytesTotal = 0;
    private long mBytesUploaded = 0;
    private int mBlobsTotal = 0;
    private int mBlobsUploaded = 0;

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
    public void onDestroy() {
        super.onDestroy();
        Log.d(TAG, "UPLOAD SERVICE onDestroy !!!");
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

    void setInFlightBytes(int v) {
        synchronized (this) {
            mBytesInFlight = v;
        }
        broadcastByteStatus();
    }

    void broadcastByteStatus() {
        synchronized (this) {
            try {
                mCallback.setByteStatus(mBytesUploaded, mBytesInFlight, mBytesTotal);
            } catch (RemoteException e) {
            }
        }
    }

    void broadcastBlobStatus() {
        synchronized (this) {
            try {
                mCallback.setBlobStatus(mBlobsUploaded, mBlobsInFlight, mBlobsTotal);
            } catch (RemoteException e) {
            }
        }
    }

    void setInFlightBlobs(int v) {
        synchronized (this) {
            mBlobsInFlight = v;
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
            if (!mQueueSet.remove(qf)) {
                return;
            }
            mQueueList.remove(qf); // TODO: ghetto, linear scan

            mBytesUploaded += qf.getSize();
            mBlobsUploaded += 1;
            try {
                mCallback.setBlobsRemain(mQueueSet.size());
            } catch (RemoteException e) {
            }
            broadcastByteStatus();
            broadcastBlobStatus();
        }
        stopServiceIfEmpty();
    }

    private void stopServiceIfEmpty() {
        synchronized (this) {
            if (mQueueSet.isEmpty()) {
                stopService(new Intent(UploadService.this, UploadService.class));
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

        public int enqueueUploadList(List<Uri> uriList) throws RemoteException {
            Log.d(TAG, "enqueuing list of " + uriList.size() + " URIs");
            int goodCount = 0;
            for (Uri uri : uriList) {
                goodCount += enqueueUpload(uri) ? 1 : 0;
            }
            Log.d(TAG, "...goodCount = " + goodCount);
            return goodCount;
        }

        public boolean enqueueUpload(Uri uri) throws RemoteException {
            startService(new Intent(UploadService.this, UploadService.class));

            ParcelFileDescriptor pfd = getFileDescriptor(uri);
            String sha1 = Util.getSha1(pfd.getFileDescriptor());
            QueuedFile qf = new QueuedFile(sha1, uri, pfd.getStatSize());

            boolean needResume = false;
            synchronized (UploadService.this) {
                if (mQueueSet.contains(qf)) {
                    Log.d(TAG, "Dup blob enqueue, ignoring " + qf);
                    stopServiceIfEmpty();
                    return false;
                }
                Log.d(TAG, "Enqueueing blob: " + qf);
                mQueueSet.add(qf);
                mQueueList.add(qf);

                if (mQueueSet.size() == 1) {
                    mBytesTotal = 0;
                    mBlobsTotal = 0;
                    mBytesUploaded = 0;
                    mBlobsUploaded = 0;
                }
                mBytesTotal += qf.getSize();
                mBlobsTotal += 1;
                needResume = !mUploading;

                mCallback.setBlobsRemain(mQueueSet.size());
            }
            broadcastBlobStatus();
            broadcastByteStatus();
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
                if (cb == null) {
                    cb = DummyNullCallback.instance();
                }
                mCallback = cb;
                // Init the new connection.
                cb.setUploading(mUploading);
                cb.setUploadStatusText(mLastUploadStatusText);
                cb.setBlobsRemain(mQueueSet.size());
            }
            broadcastBlobStatus();
            broadcastByteStatus();
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
                setUploadStatusText("Upload server not configured.");
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
