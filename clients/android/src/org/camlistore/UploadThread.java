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
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicReference;

import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";

    private final UploadService mService;
    private final HostPort mHostPort;
    private final String mUsername;
    private final String mPassword;

    AtomicReference<Process> goProcess = new AtomicReference<Process>();
    AtomicReference<OutputStream> toChildRef = new AtomicReference<OutputStream>();
    ConcurrentHashMap<String, QueuedFile> mQueuedFile = new ConcurrentHashMap<String, QueuedFile>();

    public UploadThread(UploadService uploadService, HostPort hp, String username, String password) {
        mService = uploadService;
        mHostPort = hp;
        mUsername = username;
        mPassword = password;
    }

    public void stopUploads() {
        Process p = goProcess.get();
        if (p != null) {
            p.destroy(); // force kill
        }
    }

    private String binaryPath(String suffix) {
        return mService.getBaseContext().getFilesDir().getAbsolutePath() + "/" + suffix;
    }

    private void status(String st) {
        Log.d(TAG, st);
        mService.setUploadStatusText(st);
    }

    @Override
    public void run() {
        Log.d(TAG, "Running");
        if (!mHostPort.isValid()) {
            Log.d(TAG, "host/port is invalid");
            return;
        }
        status("Running UploadThread for " + mHostPort);

        LinkedList<QueuedFile> queue;

        while (!(queue = mService.uploadQueue()).isEmpty()) {
            status("Uploading...");
            ListIterator<QueuedFile> iter = queue.listIterator();
            while (iter.hasNext()) {
                QueuedFile qf = iter.next();
                String diskPath = qf.getDiskPath();
                if (diskPath == null) {
                    Log.d(TAG, "URI " + qf.getUri() + " had no disk path; skipping");
                    iter.remove();
                    continue;
                }
                mQueuedFile.put(diskPath, qf);
                Log.d(TAG, "need to upload: " + qf);

                Process process = null;
                try {
                    ProcessBuilder pb = new ProcessBuilder()
                    .command(binaryPath("camput.bin"), "--server=" + mHostPort.urlPrefix(), "file", "-vivify", diskPath)
                    .redirectErrorStream(false);
                    pb.environment().put("CAMLI_AUTH", "userpass:" + mUsername + ":" + mPassword);
                    pb.environment().put("CAMLI_CACHE_DIR", mService.getCacheDir().getAbsolutePath());
                    pb.environment().put("CAMPUT_ANDROID_OUTPUT", "1");
                    process = pb.start();
                    goProcess.set(process);
                    new CopyToAndroidLogThread("stderr", process.getErrorStream()).start();
                    new ParseCamputOutputThread(process, mService).start();
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
                } finally {
                    goProcess.set(null);
                }
            }
        }

        status("Queue empty; done.");
    }

    public class CamputChunkUploadedMessage {
        private final String mFilename;
        private final long mSize;

        // "CHUNK_UPLOADED %d %s %s\n", sb.Size, blob, asr.path
        public CamputChunkUploadedMessage(String line) {
            String[] fields = line.split("\\s+");
            if (fields.length < 4 || fields[0] != "CHUNK_UPLOADED") {
                throw new RuntimeException("bogus CamputChunkMessage: " + line);
            }
            mSize = Long.parseLong(fields[1]);
            mFilename = fields[3];
        }

        public QueuedFile queuedFile() {
            return mQueuedFile.get(mFilename);
        }

        public long size() {
            return mSize;
        }
    }

    private class ParseCamputOutputThread extends Thread {
        private final BufferedReader mBufIn;
        private final UploadService mService;
        private final static String TAG = UploadThread.TAG + "/camput-out";

        public ParseCamputOutputThread(Process process, UploadService service) {
            mService = service;
            mBufIn = new BufferedReader(new InputStreamReader(process.getInputStream()));
        }

        @Override 
        public void run() {
            while (true) {
                String line = null;
                try {
                    line = mBufIn.readLine();
                } catch (IOException e) {
                    Log.d(TAG, "Exception reading camput's stdout: " + e.toString());
                    return;
                }
                if (line == null) {
                    // EOF
                    return;
                }
                // "CHUNK_UPLOADED %d %s %s\n", sb.Size, blob, asr.path
                if (line.startsWith("CHUNK_UPLOADED ")) {
                    CamputChunkUploadedMessage msg = new CamputChunkUploadedMessage(line);
                    mService.onChunkUploaded(msg);
                    continue;     
                }
                Log.d(TAG, "Unknown line: " + line);
            }

        }
    }

    // CopyToAndroidLogThread copies the camput child process's stderr
    // to Android's log.
    private static class CopyToAndroidLogThread extends Thread {
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
