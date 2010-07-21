package com.danga.camli;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.MalformedURLException;
import java.net.ProtocolException;
import java.net.URL;
import java.util.concurrent.atomic.AtomicBoolean;

import org.apache.http.client.HttpClient;
import org.apache.http.impl.client.DefaultHttpClient;

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
        
        URL url;
        try {
            url = new URL("http://" + mHostPort + "/camli/preupload");
        } catch (MalformedURLException e) {
            Log.d(TAG, "Bogus URL:" + e);
            return;
        }

        HttpClient ua = new DefaultHttpClient();

        HttpURLConnection conn;
        try {
            conn = (HttpURLConnection) url.openConnection();
        } catch (IOException e) {
            // TODO Auto-generated catch block
            e.printStackTrace();
            return;
        }
        try {
            conn.setRequestMethod("POST");
        } catch (ProtocolException e) {
            Log.w(TAG, "Bogus method:" + e);
            return;
        }
        conn.setDoInput(true);
        conn.setDoOutput(true);
        try {
            conn.connect();
        } catch (IOException e) {
            Log.w(TAG, "Connect error:" + e);
            return;
        }
        
        Log.d(TAG, "Connected!");
        try {
            // read the result from the server
            BufferedReader rd = new BufferedReader(new InputStreamReader(conn
                    .getInputStream()));
            StringBuilder sb = new StringBuilder();
            String line;

            while ((line = rd.readLine()) != null) {
                sb.append(line + '\n');
                Log.d(TAG, "Got line: " + line);
            }

            Log.d(TAG, "Got response: " + sb);

            Log.d(TAG, "response status: " + conn.getResponseCode());
            Log.d(TAG, "response message: " + conn.getResponseMessage());

            Object o = conn.getContent();
            Log.d(TAG, "Got object: " + o);
        } catch (IOException e) {
            Log.w(TAG, "IO error:" + e);
            return;
        }
        conn.disconnect();
    }
}
