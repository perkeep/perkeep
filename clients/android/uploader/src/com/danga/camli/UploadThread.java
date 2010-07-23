package com.danga.camli;

import java.io.BufferedOutputStream;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.PrintWriter;
import java.io.UnsupportedEncodingException;
import java.util.ArrayList;
import java.util.LinkedList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

import org.apache.http.Header;
import org.apache.http.HttpEntity;
import org.apache.http.HttpResponse;
import org.apache.http.auth.AuthScope;
import org.apache.http.auth.UsernamePasswordCredentials;
import org.apache.http.client.ClientProtocolException;
import org.apache.http.client.CredentialsProvider;
import org.apache.http.client.entity.UrlEncodedFormEntity;
import org.apache.http.client.methods.HttpPost;
import org.apache.http.impl.client.BasicCredentialsProvider;
import org.apache.http.impl.client.DefaultHttpClient;
import org.apache.http.message.BasicHeader;
import org.apache.http.message.BasicNameValuePair;
import org.json.JSONException;
import org.json.JSONObject;

import android.os.ParcelFileDescriptor;
import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";
    
    private final UploadService mService;
    private final HostPort mHostPort;
    private final LinkedList<QueuedFile> mQueue;

    private final AtomicBoolean mStopRequested = new AtomicBoolean(false);

    private final DefaultHttpClient mUA = new DefaultHttpClient();

    public UploadThread(UploadService uploadService, HostPort hp, String password) {
        mService = uploadService;
        mHostPort = hp;
        mQueue = mService.uploadQueue();

        CredentialsProvider creds = new BasicCredentialsProvider();
        creds.setCredentials(AuthScope.ANY, new UsernamePasswordCredentials("TODO-DUMMY-USER",
                password));
        mUA.setCredentialsProvider(creds);
    }
    
    public void stopPlease() {
        mStopRequested.set(false);
    }

    @Override
    public void run() {
        if (!mHostPort.isValid()) {
            return;
        }
        Log.d(TAG, "Running UploadThread for " + mHostPort);
        
        if (mQueue.isEmpty()) {
            Log.d(TAG, "Queue empty; done.");
            return;
        }

        // Do the pre-upload.
        HttpPost preReq = new HttpPost("http://" + mHostPort
                + "/camli/preupload");
        List<BasicNameValuePair> uploadKeys = new ArrayList<BasicNameValuePair>();
        uploadKeys.add(new BasicNameValuePair("camliversion", "1"));

        int n = 0;
        for (QueuedFile qf : mQueue) {
            uploadKeys.add(new BasicNameValuePair("blob" + (++n), qf.getContentName()));
        }

        try {
            preReq.setEntity(new UrlEncodedFormEntity(uploadKeys));
        } catch (UnsupportedEncodingException e) {
            Log.e(TAG, "error", e);
            return;
        }

        JSONObject preUpload = null;
        String jsonSlurp = null;
        try {
            HttpResponse res = mUA.execute(preReq);
            Log.d(TAG, "response: " + res);
            Log.d(TAG, "response code: " + res.getStatusLine());
            // TODO: check response code

            jsonSlurp = Util.slurp(res.getEntity().getContent());
            preUpload = new JSONObject(jsonSlurp);
        } catch (ClientProtocolException e) {
            Log.e(TAG, "preupload error", e);
            return;
        } catch (IOException e) {
            Log.e(TAG, "preupload error", e);
            return;
        } catch (JSONException e) {
            Log.e(TAG, "preupload JSON parse error from: " + jsonSlurp, e);
            return;
        }

        Log.d(TAG, "JSON: " + preUpload);
        String uploadUrl = preUpload
                .optString("uploadUrl", "http://" + mHostPort + "/camli/upload");
        Log.d(TAG, "uploadURL is: " + uploadUrl);

        HttpPost uploadReq = new HttpPost(uploadUrl);
        uploadReq.setEntity(new MultipartEntity());
        HttpResponse uploadRes = null;
        try {
            uploadRes = mUA.execute(uploadReq);
            Log.d(TAG, "response: " + uploadRes);
            Log.d(TAG, "response code: " + uploadRes.getStatusLine());
        } catch (ClientProtocolException e) {
            Log.e(TAG, "upload1 error", e);
            return;
        } catch (IOException e) {
            Log.e(TAG, "upload2 error", e);
            return;
        }
    }

    private class MultipartEntity implements HttpEntity {

        private boolean mDone = false;
        private final String mBoundary;

        public MultipartEntity() {
            // TODO: proper boundary
            mBoundary = "TODOLKSDJFLKSDJFLdslkjfjf23ojf0j30dm32LFDSJFLKSDJF";
        }

        public void consumeContent() throws IOException {
            // From the docs: "The name of this method is misnomer ...
            // This method is called to indicate that the content of this entity
            // is no longer required. All entity implementations are expected to
            // release all allocated resources as a result of this method
            // invocation."
            Log.d(TAG, "consumeContent()");
            mDone = true;
        }

        public InputStream getContent() throws IOException, IllegalStateException {
            Log.d(TAG, "getContent()");
            throw new RuntimeException("unexpected getContent() call");
        }

        public Header getContentEncoding() {
            return null; // "unknown"
        }

        public long getContentLength() {
            return -1; // "unknown"
        }

        public Header getContentType() {
            return new BasicHeader("Content-Type", "multipart/form-data; boundary=" + mBoundary);
        }

        public boolean isChunked() {
            Log.d(TAG, "isChunked?");
            return false;
        }

        public boolean isRepeatable() {
            Log.d(TAG, "isRepeatable?");
            // Well, not really, but needs to be for DefaultRequestDirector
            return true;
        }

        public boolean isStreaming() {
            Log.d(TAG, "isStreaming?");
            return !mDone;
        }

        public void writeTo(OutputStream out) throws IOException {
            BufferedOutputStream bos = new BufferedOutputStream(out, 1024);
            PrintWriter pw = new PrintWriter(bos);
            byte[] buf = new byte[1024];

            while (!mStopRequested.get() && !mQueue.isEmpty()) {
                QueuedFile qf = mQueue.getFirst();
                ParcelFileDescriptor pfd = mService.getFileDescriptor(qf.getUri());
                if (pfd == null) {
                    // TODO: report some error up to user?
                    mQueue.removeFirst();
                    continue;
                }
                startNewBoundary(pw);
                pw.flush();
                pw.print("Content-Disposition: form-data; name=");
                pw.print(qf.getContentName());
                pw.print("\r\n\r\n");
                pw.flush();

                FileInputStream fis = new FileInputStream(pfd.getFileDescriptor());
                int n;
                while ((n = fis.read(buf)) != -1) {
                    bos.write(buf, 0, n);
                    if (mStopRequested.get()) {
                        Log.d(TAG, "Stopping upload pre-maturely.");
                        pfd.close();
                        return;
                    }
                }
                bos.flush();
                pfd.close();
                mQueue.removeFirst();
                // TODO: notification of update
            }
            endBoundary(pw);
            pw.flush();
            Log.d(TAG, "upload complete.");
        }

        private void startNewBoundary(PrintWriter pw) {
            pw.print("\r\n--");
            pw.print(mBoundary);
            pw.print("\r\n");
        }

        private void endBoundary(PrintWriter pw) {
            pw.print("\r\n--");
            pw.print(mBoundary);
            pw.print("--\r\n");
        }
    }
}
