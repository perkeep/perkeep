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

import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Objects;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";

    private final UploadService mService;
    private final HostPort mHostPort;
    private final String mUsername;
    private final String mPassword;
    private final String mPkPut;
    private final LinkedBlockingQueue<UploadThreadMessage> msgCh = new LinkedBlockingQueue<>();

    AtomicReference<Process> goProcess = new AtomicReference<>();
    final HashMap<String, QueuedFile> mQueuedFile = new HashMap<>(); // guarded by itself

    private final Object stdinLock = new Object(); // guards setting and writing to stdinWriter
    private BufferedWriter stdinWriter;

    public UploadThread(UploadService uploadService, HostPort hp, String username, String password, String pkput) {
        mService = uploadService;
        mHostPort = hp;
        mUsername = username;
        mPassword = password;
        mPkPut = pkput;
    }

    public void stopUploads() {
        Process p = goProcess.get();
        if (p == null) {
            return;
        }
        synchronized (stdinLock) {
            if (stdinWriter == null) {
                // force kill. confused.
                p.destroy();
                goProcess.set(null);
                return;
            }
            try {
                stdinWriter.close();
                Log.d(TAG, "Closed pk-put's stdin");
                stdinWriter = null;
            } catch (IOException e) {
                p.destroy(); // force kill
                goProcess.set(null);
                return;
            }

            // Unnecessary paranoia, never seen in practice:
            new Thread(() -> {
                try {
                    Thread.sleep(750, 0);
                    stopUploads(); // force kill if still alive.
                } catch (InterruptedException ignored) {
                }

            }).start();
        }
    }

    private void status(String st) {
        Log.d(TAG, st);
        mService.setUploadStatusText(st);
    }

    // An UploadThreadMessage can be sent on msgCh and read by the run() method
    // in
    // until it's time to quit the thread.
    private static class UploadThreadMessage {
    }

    private static class ProcessExitedMessage extends UploadThreadMessage {
        public int code;

        public ProcessExitedMessage(int code) {
            this.code = code;
        }
    }

    public void enqueueFile(QueuedFile qf) {
        String diskPath = qf.getDiskPath();
        if (diskPath == null) {
            Log.d(TAG, "file has no disk path: " + qf);
            return;
        }
        synchronized (stdinLock) {
            if (stdinWriter == null) {
                return;
            }
            synchronized (mQueuedFile) {
                mQueuedFile.put(diskPath, qf);
            }
            try {
                stdinWriter.write(diskPath + "\n");
                stdinWriter.flush();
            } catch (IOException e) {
                Log.d(TAG, "Failed to write " + diskPath + " to pk-put stdin: " + e);
            }
        }
    }

    @Override
    public void run() {
        Log.d(TAG, "Running");
        if (!mHostPort.isValid()) {
            Log.d(TAG, "host/port is invalid");
            return;
        }
        status("Running UploadThread for " + mHostPort);

        mService.onStatReceived(null, 0);

        Process process;
        try {
            ProcessBuilder pb = new ProcessBuilder();
            pb.command(mPkPut, "--server=" + mHostPort.urlPrefix(), "file", "-stdinargs", "-vivify");
            pb.redirectErrorStream(false);
            pb.environment().put("CAMLI_AUTH", "userpass:" + mUsername + ":" + mPassword);
            pb.environment().put("CAMLI_CACHE_DIR", mService.getCacheDir().getAbsolutePath());
            pb.environment().put("CAMPUT_ANDROID_OUTPUT", "1");
            process = pb.start();
            goProcess.set(process);
            synchronized (stdinLock) {
                stdinWriter = new BufferedWriter(new OutputStreamWriter(process.getOutputStream(), StandardCharsets.UTF_8));
            }
            new CopyToAndroidLogThread("stderr", process.getErrorStream(), mService).start();
            new ParseCamputOutputThread(process, mService).start();
            new WaitForProcessThread(process).start();
        } catch (IOException e) {
            throw new RuntimeException(e);
        }

        for (QueuedFile queuedFile : mService.uploadQueue()) {
            enqueueFile(queuedFile);
        }

        // Loop forever reading from msgCh
        while (true) {
            UploadThreadMessage msg;
            try {
                msg = msgCh.poll(10, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                continue;
            }
            if (msg instanceof ProcessExitedMessage) {
                status("Upload process ended.");
                ProcessExitedMessage pem = (ProcessExitedMessage) msg;
                Log.d(TAG, "Loop exiting; code was = " + pem.code);
                return;
            }
        }
    }

    // "CHUNK_UPLOADED %d %s %s\n", sb.Size, blob, asr.path
    private final static Pattern chunkUploadedPattern = Pattern.compile("^CHUNK_UPLOADED (\\d+) (\\S+) (.+)");

    public class CamputChunkUploadedMessage {
        private final String mFilename;
        private final long mSize;

        public CamputChunkUploadedMessage(String line) {
            Matcher m = chunkUploadedPattern.matcher(line);
            if (!m.matches()) {
                throw new RuntimeException("bogus CamputChunkMessage: " + line);
            }
            mSize = Long.parseLong(Objects.requireNonNull(m.group(1)));
            mFilename = m.group(3);
        }

        public QueuedFile queuedFile() {
            synchronized (mQueuedFile) {
                return mQueuedFile.get(mFilename);
            }
        }

        public long size() {
            return mSize;
        }
    }

    // STAT %s %d\n
    private final static Pattern statPattern = Pattern.compile("^STAT (\\S+) (\\d+)\\b");

    public static class CamputStatMessage {
        private final Matcher mm;

        public CamputStatMessage(String line) {
            mm = statPattern.matcher(line);
            if (!mm.matches()) {
                throw new RuntimeException("bogus CamputStatMessage: " + line);
            }
        }

        public String name() {
            return mm.group(1);
        }

        public long value() {
            return Long.parseLong(Objects.requireNonNull(mm.group(2)));
        }
    }

    // STATS nfile=%d nbyte=%d skfile=%d skbyte=%d upfile=%d upbyte=%d\n
    private final static Pattern statsPattern = Pattern.compile("^STATS nfile=(\\d+) nbyte=(\\d+) skfile=(\\d+) skbyte=(\\d+) upfile=(\\d+) upbyte=(\\d+)");

    public static class CamputStatsMessage {
        private final Matcher mm;

        public CamputStatsMessage(String line) {
            mm = statsPattern.matcher(line);
            if (!mm.matches()) {
                throw new RuntimeException("bogus CamputStatsMessage: " + line);
            }
        }

        private long field(int n) {
            return Long.parseLong(Objects.requireNonNull(mm.group(n)));
        }

        public long totalFiles() {
            return field(1);
        }

        public long totalBytes() {
            return field(2);
        }

        public long skippedFiles() {
            return field(3);
        }

        public long skippedBytes() {
            return field(4);
        }

        public long uploadedFiles() {
            return field(5);
        }

        public long uploadedBytes() {
            return field(6);
        }
    }

    private class ParseCamputOutputThread extends Thread {
        private final BufferedReader mBufIn;
        private final UploadService mService;
        private final static String TAG = UploadThread.TAG + "/pk-put-out";
        private final static boolean DEBUG_CAMPUT_ACTIVITY = false;

        public ParseCamputOutputThread(Process process, UploadService service) {
            mService = service;
            mBufIn = new BufferedReader(new InputStreamReader(process.getInputStream()));
        }

        @Override
        public void run() {
            while (true) {
                String line;
                try {
                    line = mBufIn.readLine();
                } catch (IOException e) {
                    Log.d(TAG, "Exception reading pk-put's stdout: " + e);
                    return;
                }
                if (line == null) {
                    // EOF
                    return;
                }
                if (DEBUG_CAMPUT_ACTIVITY) {
                    Log.d(TAG, "pk-put: " + line);
                }
                if (line.startsWith("CHUNK_UPLOADED ")) {
                    CamputChunkUploadedMessage msg = new CamputChunkUploadedMessage(line);
                    mService.onChunkUploaded(msg);
                    continue;
                }
                if (line.startsWith("STATS ")) {
                    CamputStatsMessage msg = new CamputStatsMessage(line);
                    mService.onStatsReceived(msg);
                    continue;
                }
                if (line.startsWith("STAT ")) {
                    CamputStatMessage msg = new CamputStatMessage(line);
                    mService.onStatReceived(msg.name(), msg.value());
                    continue;
                }
                if (line.startsWith("FILE_UPLOADED ")) {
                    String filename = line.substring(14).trim();
                    QueuedFile qf;
                    synchronized (mQueuedFile) {
                        qf = mQueuedFile.get(filename);
                        if (qf != null) {
                            mQueuedFile.remove(filename);
                        }
                    }
                    if (qf != null) {
                        mService.onUploadComplete(qf);
                    }
                    continue;
                }
                Log.d(TAG, "pk-put said unknown line: " + line);
            }

        }
    }

    private class WaitForProcessThread extends Thread {
        private final Process mProcess;

        public WaitForProcessThread(Process p) {
            mProcess = p;
        }

        @Override
        public void run() {
            Log.d(TAG, "Waiting for pk-put process.");
            try {
                mProcess.waitFor();
            } catch (InterruptedException e) {
                Log.d(TAG, "Interrupted waiting for pk-put");
                msgCh.offer(new ProcessExitedMessage(-1));
                return;
            }
            Log.d(TAG, "Exit status of pk-put = " + mProcess.exitValue());
            msgCh.offer(new ProcessExitedMessage(mProcess.exitValue()));
        }
    }

    // CopyToAndroidLogThread copies the pk-put child process's stderr
    // to Android's log and submits it to to the main activity in batches.
    private static class CopyToAndroidLogThread extends Thread {
        private static final int MAX_LINES = 6; // amount of lines to buffer before submission

        private final BufferedReader mBufIn;
        private final UploadService mService;
        private final String mTag;
        private final ArrayList<String> mLines = new ArrayList<>();

        public CopyToAndroidLogThread(String stream, InputStream in, UploadService service) {
            mBufIn = new BufferedReader(new InputStreamReader(in));
            mService = service;
            mTag = TAG + "/" + stream + "-child";
        }

        @Override
        public void run() {
            while (true) {
                String line;
                try {
                    line = mBufIn.readLine();
                } catch (IOException e) {
                    Log.d(mTag, "Exception: " + e);
                    return;
                }
                if (line == null) {
                    // EOF
                    submitLines();
                    return;
                }
                handle(line);
            }
        }

        private void handle(String line) {
            Log.d(mTag, line);

            mLines.add(line);
            // Prevent accumulation of a large number of lines when pk-put produces a lot of
            // logging output for some reason.
            if (mLines.size() >= MAX_LINES) {
                submitLines();
                mLines.clear();
            }
        }

        private void submitLines() {
            StringBuilder sb = new StringBuilder();
            for (String s : mLines) {
                sb.append(s);
                sb.append('\n');
            }
            mService.onUploadErrors(sb.toString());
        }
    }

}
