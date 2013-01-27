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

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.util.LinkedList;
import java.util.ListIterator;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";

    private final UploadService mService;
    private final HostPort mHostPort;
    private final String mUsername;
    private final String mPassword;
    private LinkedList<QueuedFile> mQueue;

    AtomicReference<Process> goProcess = new AtomicReference<Process>();
    AtomicReference<OutputStream> toChildRef = new AtomicReference<OutputStream>();

    private final AtomicBoolean mStopRequested = new AtomicBoolean(false);

    public UploadThread(UploadService uploadService, HostPort hp, String username, String password) {
        mService = uploadService;
        mHostPort = hp;
        mUsername = username;
        mPassword = password;
    }

    public void stopPlease() {
        mStopRequested.set(true);
    }

    private String binaryPath(String suffix) {
        return mService.getBaseContext().getFilesDir().getAbsolutePath() + "/" + suffix;
    }

    @Override
    public void run() {
        Log.d(TAG, "Running");
        if (!mHostPort.isValid()) {
            Log.d(TAG, "host/port is invalid");
            return;
        }
        status("Running UploadThread for " + mHostPort);

        mService.setInFlightBytes(0);
        mService.setInFlightBlobs(0);

        while (!(mQueue = mService.uploadQueue()).isEmpty()) {
            if (mStopRequested.get()) {
                status("Upload pause requested; ending upload.");
                return;
            }

            status("Uploading...");
            ListIterator<QueuedFile> iter = mQueue.listIterator();
            while (iter.hasNext()) {
                QueuedFile qf = iter.next();
                String diskPath = qf.getDiskPath();
                if (diskPath == null) {
                    Log.d(TAG, "URI " + qf.getUri() + " had no disk path; skipping");
                    iter.remove();
                    continue;
                }
                Log.d(TAG, "need to upload: " + qf);

                Process process = null;
                try {
                    ProcessBuilder pb = new ProcessBuilder()
                    .command(binaryPath("camput.bin"), "--server=" + mHostPort.urlPrefix(), "file", "-vivify", diskPath)
                    .redirectErrorStream(false);
                    pb.environment().put("CAMLI_AUTH", "userpass:" + mUsername + ":" + mPassword);
                    pb.environment().put("CAMLI_CACHE_DIR", mService.getCacheDir().getAbsolutePath());
                    process = pb.start();
                    goProcess.set(process);
                    new CopyToAndroidLogThread("stderr", process.getErrorStream()).start();
                    new CopyToAndroidLogThread("stdout", process.getInputStream()).start();
                    //BufferedReader br = new BufferedReader(new InputStreamReader(in));
                    Log.d(TAG, "Waiting for camput process.");
                    process.waitFor();
                    Log.d(TAG, "Exit status of camput = " + process.exitValue());
                    if (process.exitValue() == 0) {
                        status("Uploaded " + diskPath);
                        mService.onUploadComplete(qf);
                    } else {
                        Log.d(TAG, "Problem uploading.");
                        return;
                    }
                } catch (IOException e) {
                    throw new RuntimeException(e);
                } catch (InterruptedException e) {
                    throw new RuntimeException(e);
                }

            }

            mService.setInFlightBytes(0);
            mService.setInFlightBlobs(0);
        }

        status("Queue empty; done.");
    }



    private void status(String st) {
        Log.d(TAG, st);
        mService.setUploadStatusText(st);
    }

    private class CopyToAndroidLogThread extends Thread {
        private final BufferedReader mBufIn;
        private final String mStream;

        public CopyToAndroidLogThread(String stream, InputStream in) {
            mBufIn = new BufferedReader(new InputStreamReader(in));
            mStream = stream;
        }

        @Override 
        public void run() {
            String tag = TAG + "/" + mStream + "-child";
            while (true) {
                String line = null;
                try {
                    line = mBufIn.readLine();
                } catch (IOException e) {
                    Log.d(tag, "Exception: " + e.toString());
                    return;
                }
                if (line == null) {
                    // EOF
                    return;
                }
                Log.d(tag, line);
            }
        }
    }

}
