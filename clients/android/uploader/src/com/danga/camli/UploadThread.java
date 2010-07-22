package com.danga.camli;

import java.io.IOException;
import java.io.UnsupportedEncodingException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

import org.apache.http.HttpRequestFactory;
import org.apache.http.HttpResponse;
import org.apache.http.auth.AuthScope;
import org.apache.http.auth.UsernamePasswordCredentials;
import org.apache.http.client.ClientProtocolException;
import org.apache.http.client.CredentialsProvider;
import org.apache.http.client.entity.UrlEncodedFormEntity;
import org.apache.http.client.methods.HttpPost;
import org.apache.http.impl.DefaultHttpRequestFactory;
import org.apache.http.impl.client.BasicCredentialsProvider;
import org.apache.http.impl.client.DefaultHttpClient;
import org.apache.http.message.BasicNameValuePair;

import android.util.Log;

public class UploadThread extends Thread {
    private static final String TAG = "UploadThread";
    
    private final HostPort mHostPort;
    private final String mPassword;

    private final AtomicBoolean mStopRequested = new AtomicBoolean(false);

    public UploadThread(HostPort hp, String password) {
        mHostPort = hp;
        mPassword = password;
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
        
        DefaultHttpClient ua = new DefaultHttpClient();
        HttpRequestFactory reqFactory = new DefaultHttpRequestFactory();

        CredentialsProvider creds = new BasicCredentialsProvider();
        creds.setCredentials(AuthScope.ANY,
                new UsernamePasswordCredentials("TODO-DUMMY-USER", mPassword));
        ua.setCredentialsProvider(creds);

        // Do the pre-upload.
        HttpPost preReq = new HttpPost("http://" + mHostPort
                + "/camli/preupload");
        List<BasicNameValuePair> uploadKeys = new ArrayList<BasicNameValuePair>();
        uploadKeys.add(new BasicNameValuePair("camliversion", "1"));
        try {
            preReq.setEntity(new UrlEncodedFormEntity(uploadKeys));
        } catch (UnsupportedEncodingException e) {
            Log.e(TAG, "error", e);
            return;
        }

        try {
            HttpResponse res = ua.execute(preReq);
            Log.d(TAG, "response: " + res);
            Log.d(TAG, "response code: " + res.getStatusLine());
            Log.d(TAG, "entity: " + res.getEntity());
        } catch (ClientProtocolException e) {
            Log.e(TAG, "preupload error", e);
            return;
        } catch (IOException e) {
            Log.e(TAG, "preupload error", e);
            return;
        }
    }
}
