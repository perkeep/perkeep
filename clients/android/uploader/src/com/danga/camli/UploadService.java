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

package com.danga.camli;

import java.io.File;
import java.io.FileNotFoundException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedList;
import java.util.List;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.HashMap;

import android.app.Notification;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.database.Cursor;
import android.database.sqlite.SQLiteDatabase;
import android.database.sqlite.SQLiteOpenHelper;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.Environment;
import android.os.FileObserver;
import android.os.IBinder;
import android.os.ParcelFileDescriptor;
import android.os.Parcelable;
import android.os.PowerManager;
import android.os.RemoteException;
import android.util.Log;

public class UploadService extends Service {
    private static final String TAG = "UploadService";

    private static int NOTIFY_ID_UPLOADING = 0x001;

    private static final int DB_VERSION = 1;

    public static final String INTENT_POWER_CONNECTED = "POWER_CONNECTED";
    public static final String INTENT_POWER_DISCONNECTED = "POWER_DISCONNECTED";
    public static final String INTENT_UPLOAD_ALL = "UPLOAD_ALL";

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
    SharedPreferences mSharedPrefs;
    Preferences mPrefs;
    SQLiteOpenHelper mOpenHelper;

    // Wake locks for when we have work in-flight
    private PowerManager.WakeLock mWakeLock;
    private WifiManager.WifiLock mWifiLock;
    
    // File Observers. Need to keep a reference to them, as there's no JNI
    // reference and their finalizers would run otherwise, stopping their
    // inotify.
    private ArrayList<FileObserver> mObservers = new ArrayList<FileObserver>();

    // Created lazily by getDb(), guarded by this. Closed when service stops.
    private SQLiteDatabase mDb;

    @Override
    public void onCreate() {
        super.onCreate();
        Log.d(TAG, "onCreate");

        mPowerManager = (PowerManager) getSystemService(Context.POWER_SERVICE);
        mWifiManager = (WifiManager) getSystemService(Context.WIFI_SERVICE);
        mNotificationManager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        mSharedPrefs = getSharedPreferences(Preferences.NAME, 0);
        mPrefs = new Preferences(mSharedPrefs);
        mOpenHelper = new SQLiteOpenHelper(this, "camli.db", null, DB_VERSION) {

            @Override
            public void onUpgrade(SQLiteDatabase db, int oldVersion, int newVersion) {
            }

            @Override
            public void onCreate(SQLiteDatabase db) {
                db.execSQL("CREATE TABLE digestcache (file VARCHAR(200) NOT NULL PRIMARY KEY,"
                        + "size INT, sha1 TEXT)");
            }
        };

        updateBackgroundWatchers();
    }

    @Override
    public IBinder onBind(Intent intent) {
        Log.d(TAG, "onBind intent=" + intent);
        return service;
    }

    @Override
    public void onStart(Intent intent, int startId) {
        handleCommand(intent);
    }

    private void startUploadService() {
        startService(new Intent(UploadService.this, UploadService.class));
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
        Log.d(TAG, "in handleCommand() for onStart() intent: " + intent);
        if (intent == null) {
            stopServiceIfEmpty();
            return;
        }

        String action = intent.getAction();
        if (Intent.ACTION_SEND.equals(action)) {
            handleSend(intent);
            stopServiceIfEmpty();
            return;
        }

        if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            handleSendMultiple(intent);
            stopServiceIfEmpty();
            return;
        }

        if (INTENT_UPLOAD_ALL.equals(action)) {
            handleUploadAll();
            return;
        }

        try {
            if (INTENT_POWER_CONNECTED.equals(action) && mPrefs.autoUpload()) {
                service.resume();
                handleUploadAll();
            }

            if (INTENT_POWER_DISCONNECTED.equals(action) && mPrefs.autoRequiresPower()) {
                service.pause();
                stopBackgroundWatchers();
                stopServiceIfEmpty();
            }
        } catch (RemoteException e) {
            // Ignore.
        }
    }

    private void handleSend(Intent intent) {
        Bundle extras = intent.getExtras();
        if (extras == null) {
            Log.w(TAG, "expected extras in handleSend");
            return;
        }

        extras.keySet(); // unparcel
        Log.d(TAG, "handleSend; extras=" + extras);

        Object streamValue = extras.get("android.intent.extra.STREAM");
        if (!(streamValue instanceof Uri)) {
            Log.w(TAG, "Expected URI for STREAM; got: " + streamValue);
            return;
        }

        final Uri uri = (Uri) streamValue;
        Util.runAsync(new Runnable() {
            public void run() {
                try {
                    service.enqueueUpload(uri);
                } catch (RemoteException e) {
                } finally {
                    stopServiceIfEmpty();
                }
            }
        });
    }

    private void handleUploadAll() {
        startService(new Intent(UploadService.this, UploadService.class));
        final PowerManager.WakeLock wakeLock = mPowerManager.newWakeLock(
                PowerManager.PARTIAL_WAKE_LOCK, "Camli Upload All");
        wakeLock.acquire();
        Util.runAsync(new Runnable() {
            public void run() {
                try {
                    List<String> dirs = getBackupDirs();
                    List<Uri> filesToQueue = new ArrayList<Uri>();
                    for (String dirName : dirs) {
                        File dir = new File(dirName);
                        File[] files = dir.listFiles();
                        Log.d(TAG, "Contents of " + dirName + ": " + files);
                        if (files != null) {
                            for (int i = 0; i < files.length; ++i) {
                                Log.d(TAG, "  " + files[i]);
                                filesToQueue.add(Uri.fromFile(files[i]));
                            }
                        }
                    }
                    try {
                        service.enqueueUploadList(filesToQueue);
                    } catch (RemoteException e) {
                    } finally {
                        stopServiceIfEmpty();
                    }
                } finally {
                    wakeLock.release();
                }
            }
        });
    }

    private List<String> getBackupDirs() {
        ArrayList<String> dirs = new ArrayList<String>();
        if (mSharedPrefs.getBoolean(Preferences.AUTO_DIR_PHOTOS, true)) {
            dirs.add(Environment.getExternalStorageDirectory() + "/DCIM/Camera");
        }
        if (mSharedPrefs.getBoolean(Preferences.AUTO_DIR_MYTRACKS, true)) {
            dirs.add(Environment.getExternalStorageDirectory() + "/gpx");
            dirs.add(Environment.getExternalStorageDirectory() + "/kml");
        }
        return dirs;
    }

    private void handleSendMultiple(Intent intent) {
        ArrayList<Parcelable> items = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
        ArrayList<Uri> uris = new ArrayList<Uri>(items.size());
        for (Parcelable p : items) {
            if (!(p instanceof Uri)) {
                Log.d(TAG, "uh, unknown thing " + p);
                continue;
            }
            uris.add((Uri) p);
        }
        final ArrayList<Uri> finalUris = uris;
        Util.runAsync(new Runnable() {
            public void run() {
                try {
                    service.enqueueUploadList(finalUris);
                } catch (RemoteException e) {
                } finally {
                    stopServiceIfEmpty();
                }
            }
        });
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
        if (!mSharedPrefs.getBoolean(Preferences.AUTO, false)) {
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
        synchronized (this) {
            Log.d(TAG, "onDestroy of camli UploadService; thread=" + mUploadThread + "; uploading="
                    + mUploading + "; mBlobsToDigest=" + mBlobsToDigest + "; queue size="
                    + mQueueSet.size());
        }
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
        stopServiceIfEmpty();
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
        // Convenient place to drop this cache.
        synchronized (mDigestRows) {
            mDigestRows.clear();
        }

        synchronized (this) {
            if (mQueueSet.isEmpty() && mBlobsToDigest == 0 && !mUploading && mUploadThread == null &&
                !mPrefs.autoUpload()) {
                Log.d(TAG, "stopServiceIfEmpty; stopping");
                stopSelf();
            } else {
                Log.d(TAG, "stopServiceIfEmpty; NOT stopping; "
                      + mQueueSet.isEmpty() + "; "
                      + mBlobsToDigest + "; "
                      + mUploading + "; "
                      + (mUploadThread != null));
                return;
            }

            if (mDb != null) {
                mDb.close();
                mDb = null;
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

    private SQLiteDatabase getDb() {
        synchronized (UploadService.this) {
            mDb = mOpenHelper.getWritableDatabase();
            return mDb;
        }
    }

    private static class DigestCacheRow {
        String sha1;
        long   size;
    }

    private final HashMap<String, DigestCacheRow> mDigestRows = new HashMap<String, DigestCacheRow>();
    private void batchDigestLookup(List<Uri> uriList) {
        synchronized (mDigestRows) {
            for (Uri uri : uriList) {
                mDigestRows.put(uri.toString(), new DigestCacheRow());
            }
            SQLiteDatabase db = getDb();
            Cursor c = db.query("digestcache", new String[] { "sha1", "file", "size" },
                                null, null, null, null, null);
            try {
                while (c.moveToNext()) {
                    String file = c.getString(1);
                    Log.d(TAG, "batch stat = " + file);
                    DigestCacheRow row = mDigestRows.get(file);
                    if (row == null) {
                        continue;
                    }
                    Log.d(TAG, "populating");
                    row.sha1 = c.getString(0);
                    row.size = c.getLong(2);
                }
            } finally {
                c.close();
            }
        }
    }

    private synchronized String getSha1OfUri(Uri uri, ParcelFileDescriptor pfd) {
        long statSize = pfd.getStatSize();
        synchronized (mDigestRows) {
            String uriString = uri.toString();
            DigestCacheRow row = mDigestRows.get(uriString);
            mDigestRows.remove(uriString);
            if (row != null && row.size == statSize) {
                return row.sha1;
            }
        }
        SQLiteDatabase db = getDb();
        Cursor c = db.query("digestcache", new String[] { "sha1" }, "file=? AND size=?",
                new String[] { uri.toString(), "" + statSize }, null /* groupBy */,
                null /* having */, null /* orderBy */);
        if (c != null) {
            try {
                if (c.moveToNext()) {
                    String cachedSha1 = c.getString(0);
                    Log.d(TAG, "Cached sha1 of " + uri + ": " + cachedSha1);
                    return cachedSha1;
                }
            } finally {
                c.close();
            }
        }
        String sha1 = Util.getSha1(pfd.getFileDescriptor());
        Log.d(TAG, "Uncached sha1 for " + uri + ": " + sha1);
        if (sha1 != null) {
            ContentValues row = new ContentValues();
            row.put("file", uri.toString());
            row.put("size", statSize);
            row.put("sha1", sha1);
            try {
                db.replace("digestcache", null, row);
            } catch (IllegalStateException e) {
                Log.d(TAG, "error replacing sha1", e);
            }
        }
        return sha1;
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
            batchDigestLookup(uriList);
            int goodCount = 0;
            int startGen = mStopDigestingCounter.get();
            for (Uri uri : uriList) {
                goodCount += enqueueSingleUri(uri) ? 1 : 0;
                if (startGen != mStopDigestingCounter.get()) {
                    synchronized (UploadService.this) {
                        mBlobsToDigest = 0;
                    }
                    return goodCount;
                }
            }
            synchronized (mDigestRows) {
                mDigestRows.clear();
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
            startUploadService();
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

            Log.d(TAG, "Getting SHA-1 of " + uri + "...");
            String sha1 = getSha1OfUri(uri, pfd);
            if (sha1 == null) {
                Log.w(TAG, "File is corrupt?" + uri);
                // null is returned on IO errors (e.g. flaky SD cards?)
                // TODO: propagate error up. record in service & tell activity?
                // maybe log to disk too?
                incrementBlobsToDigest(-1);
                stopServiceIfEmpty();
                return false;
            }

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
            Log.d(TAG, "Resuming upload...");
            HostPort hp = new HostPort(mSharedPrefs.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                setUploadStatusText("Upload server not configured.");
                return false;
            }
            String password = mSharedPrefs.getString(Preferences.PASSWORD, "");

            final PowerManager.WakeLock wakeLock = mPowerManager.newWakeLock(
                    PowerManager.PARTIAL_WAKE_LOCK, "Camli Upload");
            final WifiManager.WifiLock wifiLock = mWifiManager.createWifiLock(
                    WifiManager.WIFI_MODE_FULL, "Camli Upload");

            synchronized (UploadService.this) {
                if (mUploadThread != null) {
                    Log.d(TAG, "Already uploading; aborting resume.");
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
                mUploadThread.start();

                // Start a thread to release the wakelock...
                final Thread threadToWatch = mUploadThread;
                new Thread("UploadThread-waiter") {
                    @Override public void run() {
                        while (true) {
                            try {
                                threadToWatch.join(10000); // 10 seconds
                            } catch (InterruptedException e) {
                                Log.d(TAG, "Interrupt waiting for uploader thread.", e);
                            }
                            synchronized (UploadService.this) {
                                if (threadToWatch.getState() == Thread.State.TERMINATED) {
                                    break;
                                }
                                if (threadToWatch == mUploadThread) {
                                    Log.d(TAG, "UploadThread-waiter still waiting.");                                          
                                    continue;
                                }
                            }
                            break;
                        }
                        Log.d(TAG, "UploadThread done; releasing the wakelock");
                        wakeLock.release();
                        wifiLock.release();
                        onUploadThreadEnded();
                    }
                }.start();
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
                startUploadService();
                UploadService.this.stopBackgroundWatchers();
                UploadService.this.startBackgroundWatchers();
            } else {
                UploadService.this.stopBackgroundWatchers();
                stopServiceIfEmpty();
            }
        }
    };
}
