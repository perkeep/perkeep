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

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.IOException;
import java.io.OutputStream;

public class DownloadService extends Service {
    private static final String TAG = "DownloadService";
    private static final String BLOB_SUBDIR = "blobs";
    private static final int BUFFER_SIZE = 4096;
    private static final String USERNAME = "TODO-DUMMY-USER";

    private final IBinder mBinder = new LocalBinder();
    private final Handler mHandler = new Handler();

    // Effectively-final objects initialized in onCreate().
    private SharedPreferences mSharedPrefs;
    private File mBlobDir;

    interface Listener {
        void onBlobDownloadComplete(String blobRef, InputStream stream);
        void onBlobDownloadFail(String blobRef);
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
    public int onStartCommand(Intent intent, int flags, int startId) {
        return START_STICKY;
    }

    public void getBlob(String blobRef, boolean persistent, Listener listener) {
        Util.runAsync(new DownloadTask(blobRef, persistent, listener));
    }

    private class DownloadTask implements Runnable {
        private static final String TAG = "DownloadService.DownloadTask";

        private final String mBlobRef;
        private final boolean mPersistent;
        private final Listener mListener;

        DownloadTask(String blobRef, boolean persistent, Listener listener) {
            mBlobRef = blobRef;
            mPersistent = persistent;
            mListener = listener;
        }

        @Override
        public void run() {
            DefaultHttpClient httpClient = new DefaultHttpClient();
            HostPort hp = new HostPort(mSharedPrefs.getString(Preferences.HOST, ""));
            HttpGet req = new HttpGet("http://" + hp.toString() + "/camli/" + mBlobRef);
            req.setHeader("Authorization",
                          Util.getBasicAuthHeaderValue(
                              USERNAME, mSharedPrefs.getString(Preferences.PASSWORD, "")));

            OutputStream outputStream = null;
            InputStream resultStream = null;

            try {
                HttpResponse response = httpClient.execute(req);
                if (response.getStatusLine().getStatusCode() != 200) {
                    Log.e(TAG,
                          "got status code " + response.getStatusLine().getStatusCode() +
                          " while downloading " + mBlobRef);
                    handleFailure();
                    return;
                }

                File file = null;
                if (mPersistent) {
                    file = new File(mBlobDir, mBlobRef);
                    file.createNewFile();
                    outputStream = new FileOutputStream(file);
                } else {
                    outputStream = new ByteArrayOutputStream();
                }

                int bytesRead = 0;
                byte[] buffer = new byte[BUFFER_SIZE];
                InputStream inputStream = response.getEntity().getContent();
                while ((bytesRead = inputStream.read(buffer)) != -1) {
                    outputStream.write(buffer, 0, bytesRead);
                }

                resultStream =
                    mPersistent ?
                    new FileInputStream(file) :
                    new ByteArrayInputStream(((ByteArrayOutputStream) outputStream).toByteArray());

            } catch (ClientProtocolException e) {
                Log.e(TAG, "protocol error while downloading " + mBlobRef, e);
                handleFailure();
                return;
            } catch (IOException e) {
                Log.e(TAG, "IO error while downloading " + mBlobRef, e);
                handleFailure();
                return;
            } finally {
                if (outputStream != null) {
                    try { outputStream.close(); } catch (IOException e) {}
                }
            }

            // FIXME: In the case of BrowseActivity asking for a schema blob, we should probably
            // just return an in-memory copy and write to disk in the background.
            final InputStream finalResultStream = resultStream;
            mHandler.post(new Runnable() {
                @Override
                public void run() {
                    mListener.onBlobDownloadComplete(mBlobRef, finalResultStream);
                }
            });
        }

        private void handleFailure() {
            mHandler.post(new Runnable() {
                @Override
                public void run() {
                    mListener.onBlobDownloadFail(mBlobRef);
                }
            });
        }
    }
}
