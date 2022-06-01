/*
Copyright 2011 The Perkeep Authors

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

import java.io.File;
import java.io.FileNotFoundException;
import java.io.IOException;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedList;
import java.util.List;
import java.util.Map;
import java.util.Map.Entry;
import java.util.TreeMap;

import org.camlistore.UploadThread.CamputChunkUploadedMessage;
import org.camlistore.UploadThread.CamputStatsMessage;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.app.TaskStackBuilder;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.FileObserver;
import android.os.IBinder;
import android.os.ParcelFileDescriptor;
import android.os.Parcelable;
import android.os.PowerManager;
import android.os.RemoteException;
import android.provider.MediaStore;
import android.util.Log;

public class UploadService extends Service {
    private static final String TAG = "UploadService";

    private static final int NOTIFY_ID_UPLOADING = 0x001;
    private static final int NOTIFY_ID_FOREGROUND = 0x002;

    public static final String INTENT_POWER_CONNECTED = "POWER_CONNECTED";
    public static final String INTENT_POWER_DISCONNECTED = "POWER_DISCONNECTED";
    public static final String INTENT_UPLOAD_ALL = "UPLOAD_ALL";
    public static final String INTENT_NETWORK_WIFI = "WIFI_NOW";
    public static final String INTENT_NETWORK_NOT_WIFI = "NOT_WIFI_NOW";

    // Everything in this block guarded by 'this':
    private boolean mUploading = false; // user's desired state (notified quickly)
    private UploadThread mUploadThread = null; // last thread created; null when thread exits
    private Notification.Builder mNotificationBuilder; // null until upload is started/resumed
    private int mLastNotificationProgress = 0; // last computed value of the uploaded bytes, to avoid excessive notification updates
    private final Map<QueuedFile, Long> mFileBytesRemain = new HashMap<>();
    private final LinkedList<QueuedFile> mQueueList = new LinkedList<>();
    private final Map<String, Long> mStatValue = new TreeMap<>();
    private IStatusCallback mCallback = DummyNullCallback.instance();
    private String mLastUploadStatusText = null; // single line
    private String mLastUploadStatsText = null; // multi-line stats
    private int mBytesInFlight = 0;
    private int mFilesInFlight = 0;
    private Notification.Builder autoUploadNotif;
    Preferences mPrefs;

    // Stats, all guarded by 'this', and all reset to 0 on queue size transition
    // from 0 -> 1.
    private long mBytesTotal = 0;
    private long mBytesUploaded = 0;
    private int mFilesTotal = 0;
    private int mFilesUploaded = 0;

    // Effectively final, initialized in onCreate():
    PowerManager mPowerManager;
    WifiManager mWifiManager;
    NotificationManager mNotificationManager;

    // File Observers. Need to keep a reference to them, as there's no JNI
    // reference and their finalizers would run otherwise, stopping their
    // inotify.
    // Make them static so that they're never GCed.
    private final static ArrayList<FileObserver> mObservers = new ArrayList<FileObserver>();

    @Override
    public void onCreate() {
        super.onCreate();
        Log.d(TAG, "onCreate");

        mPowerManager = (PowerManager) getSystemService(Context.POWER_SERVICE);
        mWifiManager = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
        mNotificationManager = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
        mPrefs = new Preferences(getSharedPreferences(Preferences.filename(this.getBaseContext()), 0));

        updateBackgroundWatchers();

        startForeground(NOTIFY_ID_FOREGROUND, newNotification());
    }

    private Notification newNotification() {
        Intent notificationIntent = new Intent(this, SettingsActivity.class);
        // The stack builder object will contain an artificial back stack for the
        // started Activity.
        // This ensures that navigating backward from the Activity leads out of
        // your app to the Home screen.
        TaskStackBuilder stackBuilder = TaskStackBuilder.create(this);
        // Adds the back stack for the Intent (but not the Intent itself)
        stackBuilder.addParentStack(SettingsActivity.class);
        // Adds the Intent that starts the Activity to the top of the stack
        stackBuilder.addNextIntent(notificationIntent);
        PendingIntent pendingIntent = stackBuilder.getPendingIntent(0, PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT);

        NotificationChannel mNotificationChannel = new NotificationChannel(
                getString(R.string.channel_id),
                getText(R.string.channel_name),
                NotificationManager.IMPORTANCE_DEFAULT);
        mNotificationChannel.setDescription(getString(R.string.channel_description));
        // Register the channel with the system; you can't change the importance
        // or other notification behaviors after this
        mNotificationManager.createNotificationChannel(mNotificationChannel);
        autoUploadNotif = new Notification.Builder(this, getString(R.string.channel_id));
        autoUploadNotif.setContentTitle(getText(R.string.notification_title))
            .setContentText(notificationMessage())
            .setSmallIcon(R.drawable.ic_stat_notify)
            .setContentIntent(pendingIntent);

        return autoUploadNotif.build();
    }

    private String notificationMessage() {
        if (mPrefs.autoUpload()) {
            return "Auto uploading is ON";
        }
        return "Auto uploading is OFF";
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

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        handleCommand(intent);
        // We want this service to continue running until it is explicitly
        // stopped, so return sticky.
        return Service.START_STICKY;
    }

    private String getPkBin() {
        return getApplicationInfo().nativeLibraryDir + "/libpkput.so";
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
            return;
        }

        if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            handleSendMultiple(intent);
            return;
        }

        if (INTENT_UPLOAD_ALL.equals(action)) {
            handleUploadAll();
            return;
        }

        try {
            if (mPrefs.autoUpload()) {
                boolean startAuto = false;
                boolean stopAuto = false;

                if (INTENT_POWER_CONNECTED.equals(action)) {
                    if (!mPrefs.autoRequiresWifi() || WifiPowerReceiver.onWifi(this)) {
                        startAuto = true;
                    }
                } else if (INTENT_NETWORK_WIFI.equals(action)) {
                    if (!mPrefs.autoRequiresPower() || WifiPowerReceiver.onPower(this)) {
                        String ssid = "";
                        String requiredSSID = mPrefs.autoRequiredWifiSSID();
                        if (intent.hasExtra("SSID")) {
                            ssid = intent.getStringExtra("SSID");
                        }
                        Log.d(TAG, "SSID: '" + ssid +"' / Required SSID: '" + requiredSSID + "'");
                        if (requiredSSID.equals("") || requiredSSID.equals(ssid)) {
                            startAuto = true;
                        }
                    }
                } else if (INTENT_POWER_DISCONNECTED.equals(action)) {
                    stopAuto = mPrefs.autoRequiresPower();
                } else if (INTENT_NETWORK_NOT_WIFI.equals(action)) {
                    stopAuto = mPrefs.autoRequiresWifi();
                }

                if (startAuto) {
                    Log.d(TAG, "Starting automatic uploads");
                    service.resume();
                    handleUploadAll();
                    return;
                }
                if (stopAuto) {
                    Log.d(TAG, "Stopping automatic uploads");
                    service.pause();
                    stopBackgroundWatchers();
                    return;
                }
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
        Util.runAsync(() -> {
            try {
                service.enqueueUpload(uri);
            } catch (RemoteException ignored) {
            }
        });
    }

    private void handleUploadAll() {
        startService(new Intent(UploadService.this, UploadService.class));
        final PowerManager.WakeLock wakeLock = mPowerManager.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "PerkeepUploadService:UploadAll");
        wakeLock.acquire();
        Util.runAsync(() -> {
            try {
                List<String> dirs = getBackupDirs();
                List<Uri> filesToQueue = new ArrayList<>();
                for (String dirName : dirs) {
                    File dir = new File(dirName);
                    if (!dir.exists()) {
                        continue;
                    }
                    Log.d(TAG, "Uploading all in directory: " + dirName);
                    File[] files = dir.listFiles();
                    if (files != null) {
                        for (File f : files) {
                            if (f.isDirectory()) {
                                // Skip thumbnails directory.
                                // TODO: are any interesting enough to recurse into?
                                // Definitely don't need to upload thumbnails, but
                                // but maybe some other app in the the future creates
                                // sharded directories. Eye-Fi doesn't, though.
                                continue;
                            }
                            filesToQueue.add(Uri.fromFile(f));
                        }
                    }
                }
                try {
                    service.enqueueUploadList(filesToQueue);
                } catch (RemoteException ignored) {
                }
            } finally {
                wakeLock.release();
            }
        });
    }

    private List<String> getBackupDirs() {
        ArrayList<String> dirs = new ArrayList<>();
        String stripped = "/Android/data/org.camlistore/files";
        // We use getExternalFilesDirs instead of getExternalStorageDirectory, so we can
        // try both the emulated SD card (the filesystem on the internal memory really),
        // and any existing SD card as well.
        for (File dirName : getExternalFilesDirs(null)) {
            String dirPath =  dirName.getAbsolutePath();
            String root = dirPath.substring(0, dirPath.indexOf(stripped));
            if (mPrefs.autoDirPhotos()) {
                dirs.add(root + "/Pictures");
                dirs.add(root + "/DCIM/Camera");
                dirs.add(root + "/DCIM/100MEDIA");
                dirs.add(root + "/DCIM/100ANDRO");
                dirs.add(root + "/DCIM/CardboardCamera");
                dirs.add(root + "/Eye-Fi");
            }
            if (mPrefs.autoDirMyTracks()) {
                dirs.add(root + "/gpx");
                dirs.add(root + "/kml");
            }
        }
        return dirs;
    }

    private void handleSendMultiple(Intent intent) {
        ArrayList<Parcelable> items = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
        ArrayList<Uri> uris = new ArrayList<>(items.size());
        for (Parcelable p : items) {
            if (!(p instanceof Uri)) {
                Log.d(TAG, "uh, unknown thing " + p);
                continue;
            }
            uris.add((Uri) p);
        }
        final ArrayList<Uri> finalUris = uris;
        Util.runAsync(() -> {
            try {
                service.enqueueUploadList(finalUris);
            } catch (RemoteException ignored) {
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
        if (!mPrefs.autoUpload()) {
            return;
        }
        startBackgroundWatchers();
    }

    private void startBackgroundWatchers() {
        Log.d(TAG, "Starting background watchers...");
        synchronized (UploadService.this) {
            for (String dir: getBackupDirs()) {
                mObservers.add(new PerkeepFileObserver(service, new File(dir)));
            }
        }
    }

    @Override
    public void onDestroy() {
        synchronized (this) {
            Log.d(TAG, "onDestroy of perkeep UploadService; thread=" + mUploadThread + "; uploading=" + mUploading + "; queue size=" + mFileBytesRemain.size());
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
            return new LinkedList<>(mQueueList);
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
        } catch (RemoteException ignored) {
        }
    }

    void broadcastByteStatus() {
        synchronized (this) {
            if (mNotificationBuilder == null) {
                return;
            }
            int progress = (int)(100 * (double)mBytesUploaded/(double)mBytesTotal);

            // Only build new notification when progress value actually changes. Some
            // devices slow down and finally freeze completely when updating too often.
            if (mLastNotificationProgress != progress) {
                mLastNotificationProgress = progress;

                mNotificationBuilder.setProgress(100, progress, false);
                mNotificationManager.notify(NOTIFY_ID_UPLOADING, mNotificationBuilder.build());
            }
            try {
                mCallback.setByteStatus(mBytesUploaded, mBytesInFlight, mBytesTotal);
            } catch (RemoteException ignored) {
            }
        }
    }

    void broadcastFileStatus() {
        // TODO read mfiles/mcallback under lock and setfilestatus after lock
        synchronized (this) {
            try {
                mCallback.setFileStatus(mFilesUploaded, mFilesInFlight, mFilesTotal);
            } catch (RemoteException ignored) {
            }
        }
    }

    void broadcastAllState() {
        synchronized (this) {
            try {
                mCallback.setUploading(mUploading);
                mCallback.setUploadStatusText(mLastUploadStatusText);
                mCallback.setUploadStatsText(mLastUploadStatsText);
            } catch (RemoteException ignored) {
            }
        }
        broadcastFileStatus();
        broadcastByteStatus();
    }

    private void onUploadThreadEnded() {
        synchronized (this) {
            Log.d(TAG, "UploadThread ended");
            mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
            mUploadThread = null;
            mUploading = false;
            try {
                mCallback.setUploading(false);
            } catch (RemoteException ignored) {
            }
        }
    }

    /**
     * Callback from the UploadThread to the service.
     *
     * @param qf
     *            the queued file that was successfully uploaded.
     */
    void onUploadComplete(QueuedFile qf) {
        Log.d(TAG, "onUploadComplete of " + qf);
        synchronized (this) {
            if (!mFileBytesRemain.containsKey(qf)) {
                Log.w(TAG, "onUploadComplete of un-queued file: " + qf);
                return;
            }
            incrBytes(qf, qf.getSize());
            mFileBytesRemain.remove(qf);
            if (mFileBytesRemain.isEmpty()) {
                // Fill up the percentage bars, since we could get
                // this event before the periodic stats event.
                // And at the end, we could kill pk-put between
                // getting the final "file uploaded" event and the final
                // stats event.
                mFilesUploaded = mFilesTotal;
                mBytesUploaded = mBytesTotal;
                mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
                stopUploadThread();
            }
            mQueueList.remove(qf); // TODO: ghetto, linear scan
        }
        broadcastAllState();
    }

    // incrBytes notes that size bytes of qf have been uploaded
    // and updates mBytesUploaded.
    private void incrBytes(QueuedFile qf, long size) {
        synchronized (this) {
            Long remain = mFileBytesRemain.get(qf);
            if (remain != null) {
                long actual = Math.min(size, remain);
                mBytesUploaded += actual;
                mFileBytesRemain.put(qf, remain - actual);
            }
        }
    }

    private void stopServiceIfEmpty() {
        // Convenient place to drop this cache.
        synchronized (this) {
            if (mFileBytesRemain.isEmpty() && !mUploading && mUploadThread == null && !mPrefs.autoUpload()) {
                Log.d(TAG, "stopServiceIfEmpty; stopping");
                stopSelf();
            } else {
                Log.d(TAG, "stopServiceIfEmpty; NOT stopping; " + mFileBytesRemain.isEmpty() + "; " + mUploading + "; " + (mUploadThread != null));
            }
        }
    }

    ParcelFileDescriptor getFileDescriptor(Uri uri) {
        // short race between inotify and the content resolver; retry a few times with a short sleep
        ContentResolver cr = getContentResolver();
        try {
            for (int i = 0; i < 2; i++) {
                try {
                    return cr.openFileDescriptor(uri, "r");
                } catch (FileNotFoundException  e) {
                    Log.w(TAG, "FileNotFound in getFileDescriptor() for " + uri);
                }
                Thread.sleep(500);
            }
        } catch (InterruptedException ignored){}

        return null;
    }

    private void incrementFilesToUpload(int size) {
        synchronized (UploadService.this) {
            mFilesTotal += size;
        }
        broadcastFileStatus();
    }

    // pathOfURI tries to return the on-disk absolute path of uri.
    // It may return null if it fails.
    public String pathOfURI(Uri uri) {
        if (uri == null) {
            return null;
        }
        if ("file".equals(uri.getScheme())) {
            return uri.getPath();
        }
        String[] proj = { MediaStore.Images.Media.DATA };
        try (Cursor cursor = getContentResolver().query(uri, proj, null, null, null)) {
            if (cursor == null) {
                return null;
            }
            cursor.moveToFirst();
            int columnIndex = cursor.getColumnIndex(proj[0]);
            return cursor.getString(columnIndex); // might still be null
        }
    }

    private final IUploadService.Stub service = new IUploadService.Stub() {

        @Override
        public int enqueueUploadList(List<Uri> uriList) throws RemoteException {
            startService(new Intent(UploadService.this, UploadService.class));
            Log.d(TAG, "enqueuing list of " + uriList.size() + " URIs");
            incrementFilesToUpload(uriList.size());
            int goodCount = 0;
            for (Uri uri : uriList) {
                goodCount += enqueueSingleUri(uri) ? 1 : 0;
            }
            Log.d(TAG, "...goodCount = " + goodCount);
            return goodCount;
        }

        @Override
        public boolean enqueueUpload(Uri uri) throws RemoteException {
            startUploadService();
            incrementFilesToUpload(1);
            return enqueueSingleUri(uri);
        }

        private boolean enqueueSingleUri(Uri uri) throws RemoteException {
            long statSize;
            {
                ParcelFileDescriptor pfd = getFileDescriptor(uri);
                if (pfd == null) {
                    incrementFilesToUpload(-1);
                    return false;
                }

                try {
                    statSize = pfd.getStatSize();
                } finally {
                    try {
                        pfd.close();
                    } catch (IOException ignored) {
                    }
                }
            }

            String diskPath = pathOfURI(uri);
            if (diskPath == null) {
                Log.e(TAG, "failed to find pathOfURI(" + uri + ")");
                return false;
            }
            Log.d(TAG, "diskPath of " + uri + " = " + diskPath);

            QueuedFile qf = new QueuedFile(uri, statSize, diskPath);

            boolean needResume;
            synchronized (UploadService.this) {
                if (mFileBytesRemain.containsKey(qf)) {
                    Log.d(TAG, "Dup blob enqueue, ignoring " + qf);
                    return false;
                }
                Log.d(TAG, "Enqueueing blob: " + qf);
                mFileBytesRemain.put(qf, qf.getSize());
                mQueueList.add(qf);

                if (mFileBytesRemain.size() == 1) {
                    mBytesTotal = 0;
                    mFilesTotal = 0;
                    mBytesUploaded = 0;
                    mFilesUploaded = 0;
                }
                mBytesTotal += qf.getSize();
                mFilesTotal += 1;
                needResume = !mUploading;

                if (mUploadThread != null) {
                    mUploadThread.enqueueFile(qf);
                }
            }
            broadcastFileStatus();
            broadcastByteStatus();
            if (needResume) {
                resume();
            }
            return true;
        }

        @Override
        public boolean isUploading() {
            synchronized (UploadService.this) {
                return mUploading;
            }
        }

        @Override
        public void registerCallback(IStatusCallback cb) {
            // TODO: permit multiple listeners? when need comes.
            synchronized (UploadService.this) {
                if (cb == null) {
                    cb = DummyNullCallback.instance();
                }
                mCallback = cb;
            }
            broadcastAllState();
        }

        @Override
        public void unregisterCallback(IStatusCallback cb) {
            synchronized (UploadService.this) {
                mCallback = DummyNullCallback.instance();
            }
        }

        @Override
        public boolean resume() throws RemoteException {
            Log.d(TAG, "Resuming upload...");
            HostPort hp = mPrefs.hostPort();
            if (!hp.isValid()) {
                setUploadStatusText("Upload server not configured.");
                return false;
            }

            final PowerManager.WakeLock wakeLock = mPowerManager.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "PerkeepUploadService:resume");
            final WifiManager.WifiLock wifiLock = mWifiManager.createWifiLock(WifiManager.WIFI_MODE_FULL, "PerkeepUploadService:resume");

            synchronized (UploadService.this) {
                if (mUploadThread != null) {
                    Log.d(TAG, "Already uploading; aborting resume.");
                    return false;
                }

                wakeLock.acquire();
                wifiLock.acquire();

                mNotificationBuilder = new Notification.Builder(UploadService.this);
                mNotificationBuilder.setOngoing(true)
                    .setContentTitle("Uploading")
                    .setContentText("perkeep uploader running")
                    .setSmallIcon(android.R.drawable.stat_sys_upload);
                mNotificationManager.notify(NOTIFY_ID_UPLOADING, mNotificationBuilder.build());
                mLastNotificationProgress = -1;

                mUploading = true;
                mUploadThread = new UploadThread(UploadService.this, hp, mPrefs.username(), mPrefs.password(), getPkBin());
                mUploadThread.start();

                // Start a thread to release the wakelock...
                final Thread threadToWatch = mUploadThread;
                new Thread("UploadThread-waiter") {
                    @Override
                    public void run() {
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

        @Override
        public boolean pause() {
            synchronized (UploadService.this) {
                if (mUploadThread != null) {
                    stopUploadThread();
                    return true;
                }
                return false;
            }
        }

        @Override
        public int queueSize() {
            synchronized (UploadService.this) {
                return mQueueList.size();
            }
        }

        @Override
        public void stopEverything() {
            synchronized (UploadService.this) {
                mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
                mFileBytesRemain.clear();
                mQueueList.clear();
                mLastUploadStatusText = "Stopped";
                mBytesInFlight = 0;
                mFilesInFlight = 0;
                mBytesTotal = 0;
                mBytesUploaded = 0;
                mFilesTotal = 0;
                mFilesUploaded = 0;
                stopUploadThread(); // recursive lock: okay
            }
            broadcastAllState();
        }

        @Override
        public void setBackgroundWatchersEnabled(boolean enabled) {
            if (enabled) {
                startUploadService();
                UploadService.this.stopBackgroundWatchers();
                UploadService.this.startBackgroundWatchers();
            } else {
                UploadService.this.stopBackgroundWatchers();
            }
            Notification notif = autoUploadNotif.setContentText(notificationMessage()).build();
            mNotificationManager.notify(NOTIFY_ID_FOREGROUND, notif);
        }

        public void reloadSettings() {
            String profileName = Preferences.filename(UploadService.this.getBaseContext());
            Log.d(TAG, "reloading settings from: " + profileName);
            synchronized (UploadService.this) {
                boolean oldAutoUpload = mPrefs.autoUpload();
                mPrefs = new Preferences(getSharedPreferences(profileName, 0));
                boolean newAutoUpload = mPrefs.autoUpload();
                if (newAutoUpload != oldAutoUpload) {
                    this.setBackgroundWatchersEnabled(newAutoUpload);
                }
            }
        }
    };

    public void onChunkUploaded(CamputChunkUploadedMessage msg) {
        Log.d(TAG, "chunked uploaded for " + msg.queuedFile() + " with size " + msg.size());
        synchronized (UploadService.this) {
            incrBytes(msg.queuedFile(), msg.size());
        }
        broadcastAllState();
    }

    public void onStatReceived(String stat, long value) {
        String v;
        synchronized (UploadService.this) {
            if (stat == null) {
                mStatValue.clear();
            } else {
                mStatValue.put(stat, value);
            }
            StringBuilder sb = new StringBuilder();
            for (Entry<String, Long> ent : mStatValue.entrySet()) {
                sb.append(ent.getKey());
                sb.append(": ");
                sb.append(ent.getValue());
                sb.append("\n");
            }
            v = sb.toString();
            mLastUploadStatsText = v;
        }
        try {
            mCallback.setUploadStatsText(v);
        } catch (RemoteException ignored) {
        }
    }

    protected void stopUploadThread() {
        synchronized (UploadService.this) {
            mNotificationManager.cancel(NOTIFY_ID_UPLOADING);
            if (mUploadThread != null) {
                mUploadThread.stopUploads();
                mUploadThread = null;
                try {
                    mCallback.setUploading(false);
                } catch (RemoteException ignored) {
                }
            }
            mUploading = false;
        }
    }

    public void onStatsReceived(CamputStatsMessage msg) {
        synchronized (UploadService.this) {
            mBytesTotal = msg.totalBytes();
            mFilesTotal = (int) msg.totalFiles();
            mBytesUploaded = msg.skippedBytes() + msg.uploadedBytes();
            mFilesUploaded = (int) (msg.skippedFiles() + msg.uploadedFiles());
        }
        broadcastAllState();
    }

    public void onUploadErrors(String errors) {
        try {
            mCallback.setUploadErrorsText(errors);
        } catch (RemoteException ignored) {
        }
    }
}
