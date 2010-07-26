package com.danga.camli;

import java.io.File;
import java.io.FileNotFoundException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedList;
import java.util.List;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;

import android.app.Notification;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Environment;
import android.os.FileObserver;
import android.os.IBinder;
import android.os.ParcelFileDescriptor;
import android.os.PowerManager;
import android.os.RemoteException;
import android.util.Log;

public class UploadService extends Service {
    private static final String TAG = "UploadService";

    private static int NOTIFY_ID_UPLOADING = 0x001;

    public static final String INTENT_POWER_CONNECTED = "POWER_CONNECTED";
    public static final String INTENT_POWER_DISCONNECTED = "POWER_DISCONNECTED";

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
    private int mBlobsToDigest = 0;

    // Stats, all guarded by 'this', and all reset to 0 on queue size transition from 0 -> 1.
    private long mBytesTotal = 0;
    private long mBytesUploaded = 0;
    private int mBlobsTotal = 0;
    private int mBlobsUploaded = 0;

    // Effectively final, initialized in onCreate():
    PowerManager mPowerManager;
    WifiManager mWifiManager;
    NotificationManager mNotificationManager;
    SharedPreferences mPrefs;
    
    // File Observers. Need to keep a reference to them, as there's no JNI
    // reference and their finalizers would run otherwise, stopping their
    // inotify.
    private ArrayList<FileObserver> mObservers = new ArrayList<FileObserver>();

    @Override
    public void onCreate() {
        super.onCreate();
        mPowerManager = (PowerManager) getSystemService(Context.POWER_SERVICE);
        mWifiManager = (WifiManager) getSystemService(Context.WIFI_SERVICE);
        mNotificationManager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        mPrefs = getSharedPreferences(Preferences.NAME, 0);

        updateBackgroundWatchers();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return service;
    }

    @Override
    public void onStart(Intent intent, int startId) {
        handleCommand(intent);
    }

    // This is @Override as of SDK version 5, but we're targetting 4 (Android
    // 1.6)
    private static final int START_STICKY = 1; // in SDK 5
    public int onStartCommand(Intent intent, int flags, int startId) {
        handleCommand(intent);
        // We want this service to continue running until it is explicitly
        // stopped, so return sticky.
        return START_STICKY;
    }

    private void handleCommand(Intent intent) {
        Log.d(TAG, "handling startService() intent: " + intent);
        if (intent == null) {
            stopServiceIfEmpty();
            return;
        }
        try {
            if (intent.getAction().equals(INTENT_POWER_CONNECTED)) {
                service.resume();
                startBackgroundWatchers();
            }

            if (intent.getAction().equals(INTENT_POWER_DISCONNECTED)
                    && mPrefs.getBoolean(Preferences.AUTO_REQUIRE_POWER, false)) {
                service.pause();
                stopBackgroundWatchers();
            }
        } catch (RemoteException e) {
            // Ignore.
        }
        stopServiceIfEmpty();
    }

    private void stopBackgroundWatchers() {
        synchronized (UploadService.this) {
            if (mObservers.isEmpty()) {
                return;
            }
            Log.d(TAG, "Stopping background watchers...");
            for (FileObserver fo : mObservers) {
                fo.stopWatching();
            }
            mObservers.clear();
        }
    }

    private void updateBackgroundWatchers() {
        stopBackgroundWatchers();
        if (!mPrefs.getBoolean(Preferences.AUTO, false)) {
            return;
        }
        startBackgroundWatchers();
    }

    private void startBackgroundWatchers() {
        Log.d(TAG, "Starting background watchers...");
        synchronized (UploadService.this) {
            mObservers.add(new CamliFileObserver(service, new File(Environment
                    .getExternalStorageDirectory(), "DCIM/Camera")));
            mObservers.add(new CamliFileObserver(service, new File(Environment
                    .getExternalStorageDirectory(), "gpx")));
        }
    }

    @Override
    public void onDestroy() {
        super.onDestroy();
        if (mUploadThread != null) {
            Log.e(TAG, "Unexpected onDestroy with active upload thread.  Killing it.");
            mUploadThread.interrupt();
            mUploadThread = null;
        }
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

    void broadcastBlobsRemain() {
        synchronized (this) {
            try {
                mCallback.setBlobsRemain(mQueueSet.size(), mBlobsToDigest);
            } catch (RemoteException e) {
            }
        }
    }

    void broadcastAllState() {
        synchronized (this) {
            try {
                mCallback.setUploading(mUploading);
                mCallback.setUploadStatusText(mLastUploadStatusText);
            } catch (RemoteException e) {
            }
        }
        broadcastBlobStatus();
        broadcastByteStatus();
        broadcastBlobsRemain();
    }

    void setInFlightBlobs(int v) {
        synchronized (this) {
            mBlobsInFlight = v;
        }
    }

    private void onUploadThreadEnded() {
        synchronized (this) {
            Log.d(TAG, "UploadThread ended; blobsToDigest=" + mBlobsToDigest);
            if (mBlobsToDigest == 0) {
                mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
            }
            mUploadThread = null;
            mUploading = false;
            try {
                mCallback.setUploading(false);
            } catch (RemoteException e) {
            }
        }
    }

    /**
     * Callback from the UploadThread to the service.
     * 
     * @param qf
     *            the queued file
     * @param wasUploaded
     *            not a dupe that the server already had. the bytes were
     *            actually uploaded.
     */
    void onUploadComplete(QueuedFile qf, boolean wasUploaded) {
        Log.d(TAG, "onUploadComplete of " + qf + ", uploaded=" + wasUploaded);
        synchronized (this) {
            if (!mQueueSet.remove(qf)) {
                return;
            }
            mQueueList.remove(qf); // TODO: ghetto, linear scan

            if (wasUploaded) {
                mBytesUploaded += qf.getSize();
                mBlobsUploaded += 1;
            } else {
                mBytesTotal -= qf.getSize();
                mBlobsTotal -= 1;
            }
            broadcastBlobsRemain();
            broadcastByteStatus();
            broadcastBlobStatus();
        }
        stopServiceIfEmpty();
    }

    private void stopServiceIfEmpty() {
        synchronized (this) {
            if (mQueueSet.isEmpty() && mBlobsToDigest == 0
                    && !mPrefs.getBoolean(Preferences.AUTO, false)) {
                stopService(new Intent(UploadService.this, UploadService.class));
            }
        }
    }

    ParcelFileDescriptor getFileDescriptor(Uri uri) {
        ContentResolver cr = getContentResolver();
        try {
            return cr.openFileDescriptor(uri, "r");
        } catch (FileNotFoundException e) {
            Log.w(TAG, "FileNotFound in getFileDescriptor() for " + uri);
            return null;
        }
    }

    private void incrementBlobsToDigest(int size) throws RemoteException {
        synchronized (UploadService.this) {
            mBlobsToDigest += size;
        }
        broadcastBlobsRemain();
    }

    private final IUploadService.Stub service = new IUploadService.Stub() {

        // Incremented whenever "stop" is pressed:
        private final AtomicInteger mStopDigestingCounter = new AtomicInteger(0);

        public int enqueueUploadList(List<Uri> uriList) throws RemoteException {
            startService(new Intent(UploadService.this, UploadService.class));
            Log.d(TAG, "enqueuing list of " + uriList.size() + " URIs");
            incrementBlobsToDigest(uriList.size());
            int goodCount = 0;
            int startGen = mStopDigestingCounter.get();
            for (Uri uri : uriList) {
                goodCount += enqueueSingleUri(uri) ? 1 : 0;
                if (startGen != mStopDigestingCounter.get()) {
                    return goodCount;
                }
            }
            Log.d(TAG, "...goodCount = " + goodCount);
            return goodCount;
        }

        /*
         * Note: blocks while sha1'ing the file. Should be called from an
         * AsyncTask from the Activity. TODO: make the activity pass this info
         * via a startService(Intent) to us.
         */
        public boolean enqueueUpload(Uri uri) throws RemoteException {
            startService(new Intent(UploadService.this, UploadService.class));
            incrementBlobsToDigest(1);
            return enqueueSingleUri(uri);
        }

        private boolean enqueueSingleUri(Uri uri) throws RemoteException {
            ParcelFileDescriptor pfd = getFileDescriptor(uri);
            if (pfd == null) {
                incrementBlobsToDigest(-1);
                stopServiceIfEmpty();
                return false;
            }

            String sha1 = Util.getSha1(pfd.getFileDescriptor());
            QueuedFile qf = new QueuedFile(sha1, uri, pfd.getStatSize());

            boolean needResume = false;
            synchronized (UploadService.this) {
                mBlobsToDigest--;
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
            }
            broadcastBlobStatus();
            broadcastByteStatus();
            broadcastBlobsRemain();
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
            }
            broadcastAllState();
        }

        public void unregisterCallback(IStatusCallback cb) throws RemoteException {
            synchronized (UploadService.this) {
                mCallback = DummyNullCallback.instance();
            }
        }

        public boolean resume() throws RemoteException {
            HostPort hp = new HostPort(mPrefs.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                setUploadStatusText("Upload server not configured.");
                return false;
            }
            String password = mPrefs.getString(Preferences.PASSWORD, "");

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

                Notification n = new Notification(android.R.drawable.stat_sys_upload,
                        "Uploading", System.currentTimeMillis());
                n.flags = Notification.FLAG_NO_CLEAR | Notification.FLAG_ONGOING_EVENT;
                PendingIntent pIntent = PendingIntent.getActivity(UploadService.this, 0,
                        new Intent(UploadService.this, CamliActivity.class), 0);
                n.setLatestEventInfo(UploadService.this, "Uploading",
                        "Camlistore uploader running",
                        pIntent);
                mNotificationManager.notify(NOTIFY_ID_UPLOADING, n);

                mUploading = true;
                mUploadThread = new UploadThread(UploadService.this, hp,
                        password);

                // Start a thread to release the wakelock...
                final Thread threadToWatch = mUploadThread;
                new Thread("UploadThread-waiter") {
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
                    mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
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

        public void stopEverything() throws RemoteException {
            synchronized (UploadService.this) {
                mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
                mQueueSet.clear();
                mQueueList.clear();
                mLastUploadStatusText = "Stopped";
                mUploading = false;
                mBytesInFlight = 0;
                mBlobsInFlight = 0;
                mBlobsToDigest = 0;
                mBytesTotal = 0;
                mBytesUploaded = 0;
                mBlobsTotal = 0;
                mBlobsUploaded = 0;
                mStopDigestingCounter.incrementAndGet();
                if (mUploadThread != null) {
                    mUploadThread.stopPlease();
                    mUploadThread = null;
                }
            }
            broadcastAllState();
        }

        public void setBackgroundWatchersEnabled(boolean enabled) throws RemoteException {
            if (enabled) {
                UploadService.this.stopBackgroundWatchers();
                UploadService.this.startBackgroundWatchers();
            } else {
                UploadService.this.stopBackgroundWatchers();
            }
        }
    };
}
