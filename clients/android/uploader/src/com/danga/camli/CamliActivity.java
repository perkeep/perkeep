package com.danga.camli;

import java.io.FileDescriptor;
import java.io.FileInputStream;
import java.io.FileNotFoundException;
import java.io.IOException;

import android.app.Activity;
import android.content.ContentResolver;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.util.Log;
import android.view.Menu;
import android.view.MenuItem;

public class CamliActivity extends Activity {
    private static final String TAG = "CamliActivity";
    private static final int MENU_SETTINGS = 1;

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.main);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        // TODO Auto-generated method stub
        super.onActivityResult(requestCode, resultCode, data);
    }

    @Override
    protected void onDestroy() {
        // TODO Auto-generated method stub
        super.onDestroy();
    }

    @Override
    public boolean onCreateOptionsMenu(Menu menu) {
        super.onCreateOptionsMenu(menu);
        menu.add(Menu.NONE, MENU_SETTINGS, 0, "Settings");
        return true;
    }

    @Override
    public boolean onOptionsItemSelected(MenuItem item) {
        switch (item.getItemId()) {
        case MENU_SETTINGS:
            SettingsActivity.show(this);
            break;
        }
        return true;
    }

    @Override
    protected void onPause() {
        super.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();

        Intent intent = getIntent();
        String action = intent.getAction();
        Log.d(TAG, "onResume; action=" + action);
        if (Intent.ACTION_SEND.equals(action)) {
            handleSend(intent);
        } else if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            handleSendMultiple(intent);
        }
    }

    private void handleSendMultiple(Intent intent) {
        // TODO Auto-generated method stub

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

        Uri uri = (Uri) streamValue;
        startDownloadOfUri(uri);
    }

    private void startDownloadOfUri(Uri uri) {
        Log.d(TAG, "startDownloadOf: " + uri);
        ContentResolver cr = getContentResolver();
        ParcelFileDescriptor pfd = null;
        try {
            pfd = cr.openFileDescriptor(uri, "r");
        } catch (FileNotFoundException e) {
            Log.w(TAG, "FileNotFound for " + uri, e);
            return;
        }
        Log.d(TAG, "opened parcel fd = " + pfd);
        FileDescriptor fd = pfd.getFileDescriptor();
        FileInputStream fis = new FileInputStream(fd);

        try {
            pfd.close();
        } catch (IOException e) {
            Log.w(TAG, "error closing fd", e);
        }
    }
}
