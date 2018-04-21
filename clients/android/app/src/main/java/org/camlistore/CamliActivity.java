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

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.ServiceConnection;
import android.content.SharedPreferences;
import android.Manifest;
import android.os.Bundle;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.MessageQueue;
import android.os.RemoteException;
import android.support.v4.app.ActivityCompat;
import android.support.v4.content.ContextCompat;
import android.util.Log;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.view.View.OnClickListener;
import android.widget.Button;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;

public class CamliActivity extends Activity {
    private static final String TAG = "CamliActivity";

    private static final int MENU_SETTINGS = 1;
    private static final int MENU_STOP = 2;
    private static final int MENU_STOP_DIE = 3;
    private static final int MENU_UPLOAD_ALL = 4;
    private static final int MENU_VERSION = 5;
    private static final int MENU_PROFILES = 6;

    private static final int READ_EXTERNAL_STORAGE_PERMISSION_RESPONSE = 0;


    private IUploadService mServiceStub = null;
    private IStatusCallback mCallback = null;

    // Status text update state, since it updates too quickly to do it the naive way.
    private long mLastStatusUpdate = 0; // time in millis we lasted updated the screen
    private String mStatusTextCurrent = null; // what the screen says
    private String mStatusTextWant = null; // what the service wants it to say

    private final Handler mHandler = new Handler();

    private final MessageQueue.IdleHandler mIdleHandler = new MessageQueue.IdleHandler() {
        @Override
        public boolean queueIdle() {
            if (mStatusTextCurrent != mStatusTextWant) {
                TextView textStats = (TextView) findViewById(R.id.textStats);
                mLastStatusUpdate = System.currentTimeMillis();
                mStatusTextCurrent = mStatusTextWant;
                textStats.setText(mStatusTextWant);
            }
            return true;
        }
    };

    private final ServiceConnection mServiceConnection = new ServiceConnection() {

        @Override
        public void onServiceConnected(ComponentName name, IBinder service) {
            mServiceStub = IUploadService.Stub.asInterface(service);
            Log.d(TAG, "Service connected, registering callback " + mCallback);

            try {
                mServiceStub.registerCallback(mCallback);
            } catch (RemoteException e) {
                e.printStackTrace();
            }
        }

        @Override
        public void onServiceDisconnected(ComponentName name) {
            Log.d(TAG, "Service disconnected");
            mServiceStub = null;
        };
    };

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.main);

        Looper.myQueue().addIdleHandler(mIdleHandler);
        final Button buttonToggle = (Button) findViewById(R.id.buttonToggle);

        final TextView textStatus = (TextView) findViewById(R.id.textStatus);
        final TextView textStats = (TextView) findViewById(R.id.textStats);
        final TextView textErrors = (TextView) findViewById(R.id.textErrors);
        final TextView textBlobsRemain = (TextView) findViewById(R.id.textBlobsRemain);
        final TextView textUploadStatus = (TextView) findViewById(R.id.textUploadStatus);
        final TextView textByteStatus = (TextView) findViewById(R.id.textByteStatus);
        final ProgressBar progressBytes = (ProgressBar) findViewById(R.id.progressByteStatus);
        final TextView textFileStatus = (TextView) findViewById(R.id.textFileStatus);
        final ProgressBar progressFile = (ProgressBar) findViewById(R.id.progressFileStatus);

        buttonToggle.setOnClickListener(new OnClickListener() {
            @Override
            public void onClick(View btn) {
                Log.d(TAG, "button click!  text=" + buttonToggle.getText());
                if (getString(R.string.pause).equals(buttonToggle.getText())) {
                    try {
                        Log.d(TAG, "Pausing..");
                        mServiceStub.pause();
                    } catch (RemoteException e) {
                    }
                } else if (getString(R.string.resume).equals(buttonToggle.getText())) {
                    try {
                        Log.d(TAG, "Resuming..");
                        mServiceStub.resume();
                    } catch (RemoteException e) {
                    }
                }
            }
        });

        mCallback = new IStatusCallback.Stub() {
            private volatile int mLastBlobsUploadRemain = 0;
            private volatile int mLastBlobsDigestRemain = 0;

            @Override
            public void logToClient(String stuff) throws RemoteException {
                // TODO Auto-generated method stub
            }

            @Override
            public void setUploading(final boolean uploading) throws RemoteException {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        if (uploading) {
                            buttonToggle.setText(R.string.pause);
                            textStatus.setText(R.string.uploading);
                            textErrors.setText("");
                        } else if (mLastBlobsDigestRemain > 0) {
                            buttonToggle.setText(R.string.pause);
                            textStatus.setText(R.string.digesting);
                        } else {
                            buttonToggle.setText(R.string.resume);
                            int stepsRemain = mLastBlobsUploadRemain + mLastBlobsDigestRemain;
                            textStatus.setText(stepsRemain > 0 ? "Paused." : "Idle.");
                        }
                    }
                });
            }

            @Override
            public void setFileStatus(final int done, final int inFlight, final int total) throws RemoteException {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        boolean finished = (done == total && mLastBlobsDigestRemain == 0);
                        buttonToggle.setEnabled(!finished);
                        progressFile.setMax(total);
                        progressFile.setProgress(done);
                        progressFile.setSecondaryProgress(done + inFlight);
                        if (finished) {
                            buttonToggle.setText(getString(R.string.pause_resume));
                        }

                        StringBuilder filesUploaded = new StringBuilder(40);
                        if (done < 2) {
                            filesUploaded.append(done).append(" file uploaded");
                        } else {
                            filesUploaded.append(done).append(" files uploaded");
                        }
                        textFileStatus.setText(filesUploaded.toString());

                        StringBuilder sb = new StringBuilder(40);
                        sb.append("Files to upload: ").append(total - done);
                        textBlobsRemain.setText(sb.toString());
                    }
                });
            }

            @Override
            public void setByteStatus(final long done, final int inFlight, final long total) throws RemoteException {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        // setMax takes an (signed) int, but 2GB is a totally
                        // reasonable upload size, so use units of 1KB instead.
                        progressBytes.setMax((int) (total / 1024L));
                        progressBytes.setProgress((int) (done / 1024L));
                        // TODO: renable once pk-put properly sends inflight information
                        // progressBytes.setSecondaryProgress(progressBytes.getProgress() + inFlight / 1024);

                        StringBuilder bytesUploaded = new StringBuilder(40);
                        if (done < 2) {
                            bytesUploaded.append(done).append(" byte uploaded");
                        } else {
                            bytesUploaded.append(done).append(" bytes uploaded");
                        }
                        textByteStatus.setText(bytesUploaded.toString());
                    }
                });
            }

            @Override
            public void setUploadStatusText(final String text) throws RemoteException {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        textUploadStatus.setText(text);
                    }
                });
            }

            @Override
            public void setUploadStatsText(final String text) throws RemoteException {
                // We were getting these status updates so quickly that the calls to TextView.setText
                // were consuming all CPU on the main thread and it was stalling the main thread
                // for seconds, sometimes even triggering device freezes. Ridiculous. So instead,
                // only update this every 30 milliseconds, otherwise wait for the looper to be idle
                // to update it.
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        mStatusTextWant = text;
                        long now = System.currentTimeMillis();
                        if (mLastStatusUpdate < now - 30) {
                            mStatusTextCurrent = mStatusTextWant;
                            textStats.setText(mStatusTextWant);
                            mLastStatusUpdate = System.currentTimeMillis();
                        }
                    }
                });
            }

            public void setUploadErrorsText(final String text) throws RemoteException {
                mHandler.post(new Runnable() {
                    @Override
                    public void run() {
                        textErrors.setText(text);
                    }
                });
            }
        };

    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);

        // TODO: picking files/photos to upload?
    }

    @Override
    protected void onDestroy() {
        // TODO Auto-generated method stub
        super.onDestroy();
    }

    @Override
    public boolean onCreateOptionsMenu(Menu menu) {
        super.onCreateOptionsMenu(menu);

        MenuItem uploadAll = menu.add(Menu.NONE, MENU_UPLOAD_ALL, 0, R.string.upload_all);
        uploadAll.setIcon(android.R.drawable.ic_menu_upload);

        MenuItem stop = menu.add(Menu.NONE, MENU_STOP, 0, R.string.stop);
        stop.setIcon(android.R.drawable.ic_menu_close_clear_cancel);

        MenuItem stopDie = menu.add(Menu.NONE, MENU_STOP_DIE, 0, R.string.stop_die);
        stopDie.setIcon(android.R.drawable.ic_menu_close_clear_cancel);

        MenuItem profiles = menu.add(Menu.NONE, MENU_PROFILES, 0, R.string.profile);
        // TODO(mpl): do we care about this icon? I don't even know where it actually appears.
        profiles.setIcon(android.R.drawable.ic_menu_preferences);

        MenuItem settings = menu.add(Menu.NONE, MENU_SETTINGS, 0, R.string.settings);
        settings.setIcon(android.R.drawable.ic_menu_preferences);

        menu.add(Menu.NONE, MENU_VERSION, 0, R.string.version);
        return true;
    }

    @Override
    public boolean onOptionsItemSelected(MenuItem item) {
        switch (item.getItemId()) {
        case MENU_STOP:
            try {
                if (mServiceStub != null) {
                    mServiceStub.stopEverything();
                }
            } catch (RemoteException e) {
                // Ignore.
            }
            break;
        case MENU_STOP_DIE:
            System.exit(1);
        case MENU_SETTINGS:
            SettingsActivity.show(this);
            break;
        case MENU_PROFILES:
            ProfilesActivity.show(this);
            break;
        case MENU_VERSION:
            Toast.makeText(this, "pk-put version: " + ((UploadApplication) getApplication()).getCamputVersion(), Toast.LENGTH_LONG).show();
            break;
        case MENU_UPLOAD_ALL:
            Intent uploadAll = new Intent(UploadService.INTENT_UPLOAD_ALL);
            uploadAll.setClass(this, UploadService.class);
            Log.d(TAG, "Starting upload all...");
            startService(uploadAll);
            Log.d(TAG, "Back from upload all...");
            break;
        }
        return true;
    }

    @Override
    protected void onPause() {
        super.onPause();
        try {
            if (mServiceStub != null)
                mServiceStub.unregisterCallback(mCallback);
        } catch (RemoteException e) {
            // Ignore.
        }
        if (mServiceConnection != null) {
            unbindService(mServiceConnection);
        }
    }

    @Override
    protected void onResume() {
        super.onResume();

        // Check for the right to read the user's files.
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_EXTERNAL_STORAGE)
            != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.READ_EXTERNAL_STORAGE},
                READ_EXTERNAL_STORAGE_PERMISSION_RESPONSE);
        }

        SharedPreferences sp = getSharedPreferences(Preferences.filename(this.getBaseContext()), 0);
        try {
            HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
            if (!hp.isValid()) {
                // Crashes oddly in some Android Instrumentation thing if
                // uncommented:
                // SettingsActivity.show(this);
                // return;
            }
        } catch (NumberFormatException enf) {
            AlertDialog.Builder builder = new AlertDialog.Builder(this);
            builder.setMessage("Server should be of form [https://]<host[:port]>")
                    .setTitle("Invalid Setting");
            AlertDialog alert = builder.create();
            alert.show();
        }

        // Actually start the service before binding to it, so that unbinding from it does not destroy the service.
        Intent intent = getIntent();
        Intent serviceIntent = new Intent(intent);
        serviceIntent.setClass(this, UploadService.class);
        startService(serviceIntent);
        bindService(serviceIntent, mServiceConnection, Context.BIND_AUTO_CREATE);

        // TODO(mpl): maybe remove all of that below. Does the intent action still matter now?
        String action = intent.getAction();
        Log.d(TAG, "onResume; action=" + action);

        if (Intent.ACTION_SEND.equals(action) || Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            setIntent(new Intent(this, CamliActivity.class));
        } else {
            Log.d(TAG, "Normal CamliActivity viewing.");
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode,
        String permissions[], int[] grantResults) {
        switch (requestCode) {
        case READ_EXTERNAL_STORAGE_PERMISSION_RESPONSE: {
            // If request is cancelled, the result arrays are empty.
            if (grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                Log.d(TAG, "User authorized us to read his files.");
            } else {
                // The app is useless without this permission, so we just kill ourselves.
                Log.d(TAG, "Permission to read files denied by user.");
                System.exit(1);
            }
            return;
        }
        }
    }
}
