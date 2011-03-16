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

import android.app.Service;
import android.content.Intent;
import android.content.SharedPreferences;
import android.os.Binder;
import android.os.Handler;
import android.os.IBinder;
import android.util.Log;

import org.apache.http.client.ClientProtocolException;
import org.apache.http.client.methods.HttpGet;
import org.apache.http.HttpResponse;
import org.apache.http.impl.client.DefaultHttpClient;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;

public class DownloadService extends Service {
    private static final String TAG = "DownloadService";
    private static final String BLOB_SUBDIR = "blobs";
    private static final int BUFFER_SIZE = 4096;
    private static final String USERNAME = "TODO-DUMMY-USER";
    private static final String SEARCH_BLOBREF = "search";

    private final IBinder mBinder = new LocalBinder();
    private final Handler mHandler = new Handler();

    // Blobs currently being downloaded.  Protected by |this|.
    private final HashSet<String> mInProgressBlobRefs = new HashSet<String>();

    // Maps from blobrefs to callbacks for their contents.  Protected by |this|.
    private final HashMap<String, ArrayList<ByteArrayListener>> mByteArrayListenersByBlobRef =
        new HashMap<String, ArrayList<ByteArrayListener>>();
    private final HashMap<String, ArrayList<FileListener>> mFileListenersByBlobRef =
        new HashMap<String, ArrayList<FileListener>>();

    // Effectively-final objects initialized in onCreate().
    private SharedPreferences mSharedPrefs;
    private File mBlobDir;

    // Callback for receiving a blob's contents as an in-memory array of bytes.
    interface ByteArrayListener {
        void onBlobDownloadSuccess(String blobRef, byte[] bytes);
        void onBlobDownloadFailure(String blobRef);
    }

    // Callback for receiving a blob's contents as a File.
    interface FileListener {
        void onBlobDownloadSuccess(String blobRef, File file);
        void onBlobDownloadFailure(String data);
    }

    public class LocalBinder extends Binder {
        DownloadService getService() {
            return DownloadService.this;
        }
    }

    @Override
    public void onCreate() {
        Log.d(TAG, "onCreate");
        super.onCreate();
        mSharedPrefs = getSharedPreferences(Preferences.NAME, 0);
        mBlobDir = new File(getExternalFilesDir(null), BLOB_SUBDIR);
        mBlobDir.mkdirs();
    }

    @Override
    public void onDestroy() {
        Log.d(TAG, "onDestroy");
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return mBinder;
    }

    private static final int START_STICKY = 1;  // in SDK 5
    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        return START_STICKY;
    }

    // Get |blobRef|'s contents, passing them as a byte[] to |listener| on the UI thread.
    public void getBlobAsByteArray(String blobRef, ByteArrayListener listener) {
        Util.runAsync(new GetBlobTask(blobRef, listener, null));
    }

    // Get |blobRef|'s contents, passing them as a File to |listener| on the UI thread.
    public void getBlobAsFile(String blobRef, FileListener listener) {
        Util.runAsync(new GetBlobTask(blobRef, null, listener));
    }

    private static boolean canBlobBeCached(String blobRef) {
        return !blobRef.equals(SEARCH_BLOBREF);
    }

    // Get a list of byte array listeners waiting for |blobRef|.
    // If |insert| is true, the returned list can be used for adding new listeners.
    private ArrayList<ByteArrayListener> getByteArrayListenersForBlobRef(String blobRef, boolean insert) {
        ArrayList<ByteArrayListener> listeners = mByteArrayListenersByBlobRef.get(blobRef);
        if (listeners == null) {
            listeners = new ArrayList<ByteArrayListener>();
            if (insert)
                mByteArrayListenersByBlobRef.put(blobRef, listeners);
        }
        return listeners;
    }

    // Get a list of file listeners waiting for |blobRef|.
    // If |insert| is true, the returned list can be used for adding new listeners.
    private ArrayList<FileListener> getFileListenersForBlobRef(String blobRef, boolean insert) {
        ArrayList<FileListener> listeners = mFileListenersByBlobRef.get(blobRef);
        if (listeners == null) {
            listeners = new ArrayList<FileListener>();
            if (insert)
                mFileListenersByBlobRef.put(blobRef, listeners);
        }
        return listeners;
    }

    private class GetBlobTask implements Runnable {
        private static final String TAG = "DownloadService.GetBlobTask";

        private final String mBlobRef;
        private final ByteArrayListener mByteArrayListener;
        private final FileListener mFileListener;

        GetBlobTask(String blobRef, ByteArrayListener byteArrayListener, FileListener fileListener) {
            if (!(byteArrayListener != null) ^ (fileListener != null))
                throw new RuntimeException("exactly one of byteArrayListener and fileListener must be non-null");
            mBlobRef = blobRef;
            mByteArrayListener = byteArrayListener;
            mFileListener = fileListener;
        }

        @Override
        public void run() {
            synchronized(DownloadService.this) {
                if (mByteArrayListener != null)
                    getByteArrayListenersForBlobRef(mBlobRef, true).add(mByteArrayListener);
                if (mFileListener != null)
                    getFileListenersForBlobRef(mBlobRef, true).add(mFileListener);

                // If another thread is already servicing a request for this blob, let it handle us too.
                if (mInProgressBlobRefs.contains(mBlobRef))
                    return;
                mInProgressBlobRefs.add(mBlobRef);
            }

            File file = loadBlobFromCache();
            if (file == null)
                file = downloadBlob();

            synchronized(DownloadService.this) {
                if (file != null) {
                    handleSuccess(file);
                } else {
                    handleFailure();
                }
            }
        }

        // Load |mBlobRef| from the cache, returning a File on success or null on failure.
        private File loadBlobFromCache() {
            if (canBlobBeCached(mBlobRef))
                return null;

            File file = new File(mBlobDir, mBlobRef);
            return file.exists() ? file : null;
        }

        // Download |mBlobRef|, returning a File on success or null on failure.
        private File downloadBlob() {
            DefaultHttpClient httpClient = new DefaultHttpClient();
            HostPort hp = new HostPort(mSharedPrefs.getString(Preferences.HOST, ""));
            String url = "http://" + hp.toString() + "/camli/" + mBlobRef;
            Log.d(TAG, "downloading " + url);
            HttpGet req = new HttpGet(url);
            req.setHeader("Authorization",
                          Util.getBasicAuthHeaderValue(
                              USERNAME, mSharedPrefs.getString(Preferences.PASSWORD, "")));

            boolean success = false;
            File file = null;
            FileOutputStream outputStream = null;

            try {
                HttpResponse response = httpClient.execute(req);
                final int statusCode = response.getStatusLine().getStatusCode();
                if (statusCode != 200) {
                    Log.e(TAG, "got status code " + statusCode + "while downloading " + mBlobRef);
                    return null;
                }

                if (canBlobBeCached(mBlobRef)) {
                    file = new File(mBlobDir, mBlobRef);
                    file.createNewFile();
                } else {
                    // FIXME: Don't write uncacheable blobs to disk at all.
                    file = File.createTempFile(mBlobRef, null, mBlobDir);
                    file.deleteOnExit();
                }
                outputStream = new FileOutputStream(file);

                int bytesRead = 0;
                byte[] buffer = new byte[BUFFER_SIZE];
                InputStream inputStream = response.getEntity().getContent();
                while ((bytesRead = inputStream.read(buffer)) != -1) {
                    outputStream.write(buffer, 0, bytesRead);
                }

                success = true;
                return file;

            } catch (ClientProtocolException e) {
                Log.e(TAG, "protocol error while downloading " + mBlobRef, e);
                return null;
            } catch (IOException e) {
                Log.e(TAG, "IO error while downloading " + mBlobRef, e);
                return null;
            } finally {
                if (outputStream != null) {
                    try { outputStream.close(); } catch (IOException e) {}
                }
                if (!success && file != null && file.exists()) {
                    file.delete();
                }
            }
        }

        // Report a successful download or cache access of |mBlobRef| to all waiting listeners.
        // Removes |mBlobRef| from |mInProgressBlobRefs|.
        private void handleSuccess(final File file) {
            Log.d(TAG, "returning " + file.getPath());

            ArrayList<ByteArrayListener> byteArrayListeners = getByteArrayListenersForBlobRef(mBlobRef, false);
            if (!byteArrayListeners.isEmpty()) {
                byte[] bytes = null;
                try {
                    bytes = Util.slurpToByteArray(new FileInputStream(file));
                } catch (IOException e) {
                    Log.e(TAG, "got IO error while reading " + file.getPath(), e);
                }

                final byte[] finalBytes = bytes;
                for (final ByteArrayListener listener : byteArrayListeners) {
                    mHandler.post(new Runnable() {
                        @Override
                        public void run() {
                            if (finalBytes != null)
                                listener.onBlobDownloadSuccess(mBlobRef, finalBytes);
                            else
                                listener.onBlobDownloadFailure(mBlobRef);
                        }
                    });
                }
            }
            mByteArrayListenersByBlobRef.remove(mBlobRef);

            for (final FileListener listener : getFileListenersForBlobRef(mBlobRef, false)) {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        listener.onBlobDownloadSuccess(mBlobRef, file);
                    }
                });
            }
            mFileListenersByBlobRef.remove(mBlobRef);

            mInProgressBlobRefs.remove(mBlobRef);
        }

        // Report a unsuccessful download of |mBlobRef| to all waiting listeners.
        // Removes |mBlobRef| from |mInProgressBlobRefs|.
        private void handleFailure() {
            for (final ByteArrayListener listener : getByteArrayListenersForBlobRef(mBlobRef, false)) {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        listener.onBlobDownloadFailure(mBlobRef);
                    }
                });
            }
            mByteArrayListenersByBlobRef.remove(mBlobRef);

            for (final FileListener listener : getFileListenersForBlobRef(mBlobRef, false)) {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        listener.onBlobDownloadFailure(mBlobRef);
                    }
                });
            }
            mFileListenersByBlobRef.remove(mBlobRef);

            mInProgressBlobRefs.remove(mBlobRef);
        }
    }
}
