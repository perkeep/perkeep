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
import java.util.ListIterator;
import java.util.concurrent.atomic.AtomicBoolean;

import org.apache.http.Header;
import org.apache.http.HttpEntity;
import org.apache.http.HttpResponse;
import org.apache.http.StatusLine;
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
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import android.os.ParcelFileDescriptor;
import android.os.SystemClock;
import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";
    
    private final UploadService mService;
    private final HostPort mHostPort;
    private LinkedList<QueuedFile> mQueue;

    private final AtomicBoolean mStopRequested = new AtomicBoolean(false);

    private final DefaultHttpClient mUA = new DefaultHttpClient();

    public UploadThread(UploadService uploadService, HostPort hp, String password) {
        mService = uploadService;
        mHostPort = hp;

        CredentialsProvider creds = new BasicCredentialsProvider();
        creds.setCredentials(AuthScope.ANY, new UsernamePasswordCredentials("TODO-DUMMY-USER",
                password));
        mUA.setCredentialsProvider(creds);
    }
    
    public void stopPlease() {
        mStopRequested.set(true);
    }

    @Override
    public void run() {
        if (!mHostPort.isValid()) {
            return;
        }
        status("Running UploadThread for " + mHostPort);
        
        while (!(mQueue = mService.uploadQueue()).isEmpty()) {
            if (mStopRequested.get()) {
                status("Upload pause requested; ending upload.");
                return;
            }

            status("Starting pre-upload of " + mQueue.size() + " files.");
            JSONObject preUpload = doPreUpload();
            if (preUpload == null) {
                Log.w(TAG, "Preupload failed, ending UploadThread.");
                return;
            }

            if (mStopRequested.get()) {
                status("Upload pause requested; ending upload.");
                return;
            }

            status("Uploading...");
            if (!doUpload(preUpload)) {
                Log.w(TAG, "Upload failed, ending UploadThread.");
                return;
            }
        }

        status("Queue empty; done.");
    }

    private JSONObject doPreUpload() {
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
            return null;
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
            return null;
        } catch (IOException e) {
            Log.e(TAG, "preupload error", e);
            return null;
        } catch (JSONException e) {
            Log.e(TAG, "preupload JSON parse error from: " + jsonSlurp, e);
            return null;
        }
        return preUpload;
    }

    private boolean doUpload(JSONObject preUpload) {
        Log.d(TAG, "JSON: " + preUpload);
        String uploadUrl = preUpload
                .optString("uploadUrl", "http://" + mHostPort + "/camli/upload");
        Log.d(TAG, "uploadURL is: " + uploadUrl);

        // Which ones do we already have, so don't have to upload again?
        filterOutAlreadyUploadedBlobs(preUpload.optJSONArray("alreadyHave"));
        if (mQueue.isEmpty()) {
            return true;
        }

        HttpPost uploadReq = new HttpPost(uploadUrl);
        MultipartEntity entity = new MultipartEntity();
        uploadReq.setEntity(entity);
        HttpResponse uploadRes = null;
        try {
            uploadRes = mUA.execute(uploadReq);
        } catch (ClientProtocolException e) {
            Log.e(TAG, "upload1 error", e);
            return false;
        } catch (IOException e) {
            Log.e(TAG, "upload2 error", e);
            return false;
        }
        Log.d(TAG, "response: " + uploadRes);
        StatusLine statusLine = uploadRes.getStatusLine();
        Log.d(TAG, "response code: " + statusLine);
        // TODO: check response body, once response body is defined?
        if (statusLine == null || statusLine.getStatusCode() < 200
                || statusLine.getStatusCode() > 299) {
            Log.d(TAG, "upload error.");
            // TODO: back-off? or probably in the Service layer.
            return false;
        }
        for (QueuedFile qf : entity.getFilesWritten()) {
            // TODO: only do this if acknowledged in JSON response?
            Log.d(TAG, "Upload complete for: " + qf);
            mService.onUploadComplete(qf, true /* not a dupe, uploaded */);
        }
        Log.d(TAG, "doUpload returning true.");
        return true;
    }

    private void filterOutAlreadyUploadedBlobs(JSONArray alreadyHave) {
        if (alreadyHave == null) {
            return;
        }
        for (int i = 0; i < alreadyHave.length(); ++i) {
            JSONObject o = alreadyHave.optJSONObject(i);
            if (o == null) {
                // Malformed response; ignore.
                continue;
            }
            String blobRef = o.optString("blobRef");
            if (blobRef == null) {
                // Malformed response; ignore
                continue;
            }
            filterOutBlobRef(blobRef);
        }
    }

    private void filterOutBlobRef(String blobRef) {
        // TODO: kinda lame, iterating over whole list.
        ListIterator<QueuedFile> iter = mQueue.listIterator();
        while (iter.hasNext()) {
            QueuedFile qf = iter.next();
            if (qf.getContentName().equals(blobRef)) {
                iter.remove();
                mService.onUploadComplete(qf, false /* not uploaded */);
            }
        }
    }

    private void status(String st) {
        Log.d(TAG, st);
        mService.setUploadStatusText(st);
    }

    private class MultipartEntity implements HttpEntity {

        private boolean mDone = false;
        private final String mBoundary;
        private final List<QueuedFile> mFilesWritten = new ArrayList<QueuedFile>();

        public MultipartEntity() {
            // TODO: proper boundary
            mBoundary = "TODOLKSDJFLKSDJFLdslkjfjf23ojf0j30dm32LFDSJFLKSDJF";
        }

        public List<QueuedFile> getFilesWritten() {
            return mFilesWritten;
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
            return false;
        }

        public boolean isRepeatable() {
            // Well, not really, but needs to be for DefaultRequestDirector
            return true;
        }

        public boolean isStreaming() {
            return !mDone;
        }

        public void writeTo(OutputStream out) throws IOException {
            Log.d(TAG, "writeTo outputstream...");
            BufferedOutputStream bos = new BufferedOutputStream(out, 1024);
            PrintWriter pw = new PrintWriter(bos);
            byte[] buf = new byte[1024];

            int bytesWritten = 0;
            long timeStarted = SystemClock.uptimeMillis();
            long lastLogUpdate = 0;

            for (QueuedFile qf : mQueue) {
                Log.d(TAG, "begin writeTo of " + qf);
                ParcelFileDescriptor pfd = mService.getFileDescriptor(qf.getUri());
                long totalFileBytes = pfd.getStatSize();
                long uploadedFileBytes = 0;
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
                    bytesWritten += n;
                    uploadedFileBytes += n;
                    long now = SystemClock.uptimeMillis();
                    if (now - lastLogUpdate > 1000) {
                        Log.d(TAG, "wrote " + uploadedFileBytes + "/" + totalFileBytes + " of "
                                + qf);
                        lastLogUpdate = now;
                    }
                    bos.write(buf, 0, n);
                    if (mStopRequested.get()) {
                        status("Upload pause requested; ending write.");
                        pfd.close();
                        return;
                    }
                }
                bos.flush();
                pfd.close();
                // TODO: notification of update
                Log.d(TAG, "write of " + qf.getContentName() + " complete.");
                mFilesWritten.add(qf);

                if (bytesWritten > 1024 * 1024) {
                    Log.d(TAG, "enough bytes written, stopping writing after " + bytesWritten);
                    // Stop after 1MB to get response.
                    // TODO: make this smarter, configurable, time-based.
                    break;
                }

                long now = SystemClock.uptimeMillis();
                if (now - timeStarted > 15 * 1000) {
                    // TODO: configurable
                    status("We've been writing this request for 15 seconds, finish it.");
                    break;
                }
            }
            endBoundary(pw);
            pw.flush();
            Log.d(TAG, "finished writing upload MIME body.");
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
