package com.danga.camli;

import java.util.ArrayList;

import android.app.Activity;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.net.Uri;
import android.os.AsyncTask;
import android.os.Bundle;
import android.os.Handler;
import android.os.IBinder;
import android.os.Parcelable;
import android.os.RemoteException;
import android.util.Log;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.view.View.OnClickListener;
import android.widget.Button;
import android.widget.ProgressBar;
import android.widget.TextView;

public class CamliActivity extends Activity {
    private static final String TAG = "CamliActivity";
    private static final int MENU_SETTINGS = 1;

    private IUploadService mServiceStub = null;
    private IStatusCallback mCallback = null;

    private final Handler mHandler = new Handler();
    private final ArrayList<Uri> mPendingUrisToUpload = new ArrayList<Uri>();

    private final ServiceConnection mServiceConnection = new ServiceConnection() {

        public void onServiceConnected(ComponentName name, IBinder service) {
            mServiceStub = IUploadService.Stub.asInterface(service);
            Log.d(TAG, "Service connected, registering callback " + mCallback);

            try {
                mServiceStub.registerCallback(mCallback);
                // Drain the queue from before the service was connected.
                for (Uri uri : mPendingUrisToUpload) {
                    startDownloadOfUri(uri);
                }
                mPendingUrisToUpload.clear();
            } catch (RemoteException e) {
                e.printStackTrace();
            }
        }

        public void onServiceDisconnected(ComponentName name) {
            Log.d(TAG, "Service disconnected");
            mServiceStub = null;
        };
    };

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.main);

        final Button buttonToggle = (Button) findViewById(R.id.buttonToggle);
        final TextView textStatus = (TextView) findViewById(R.id.textStatus);
        final TextView textBlobsRemain = (TextView) findViewById(R.id.textBlobsRemain);
        final TextView textUploadStatus = (TextView) findViewById(R.id.textUploadStatus);
        final ProgressBar progressBytes = (ProgressBar) findViewById(R.id.progressByteStatus);
        final ProgressBar progressBlob = (ProgressBar) findViewById(R.id.progressBlobStatus);

        buttonToggle.setOnClickListener(new OnClickListener() {
            public void onClick(View btn) {
                Log.d(TAG, "button click!  text=" + buttonToggle.getText());
                if ("Pause".equals(buttonToggle.getText())) {
                    try {
                        Log.d(TAG, "Pausing..");
                        mServiceStub.pause();
                    } catch (RemoteException e) {
                    }
                } else if ("Resume".equals(buttonToggle.getText())) {
                    try {
                        Log.d(TAG, "Resuming..");
                        mServiceStub.resume();
                    } catch (RemoteException e) {
                    }
                }
            }
        });

        mCallback = new IStatusCallback.Stub() {
            private volatile int mLastBlobsRemain = 0;

            public void logToClient(String stuff) throws RemoteException {
                // TODO Auto-generated method stub

            }

            public void setUploading(final boolean uploading) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        if (uploading) {
                            buttonToggle.setText("Pause");
                            textStatus.setText("Uploading...");
                        } else {
                            buttonToggle.setText("Resume");
                            textStatus.setText(mLastBlobsRemain > 0 ? "Paused." : "Idle.");
                        }
                    }
                });
            }

            public void setBlobStatus(final int done, final int total) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        buttonToggle.setEnabled(done != total);
                        progressBlob.setMax(total);
                        progressBlob.setProgress(done);
                    }
                });
            }

            public void setByteStatus(final long done, final long total) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        // setMax takes an (signed) int, but 2GB is a totally
                        // reasonable upload size, so use units of 1KB instead.
                        progressBytes.setMax((int) (total / 1024L));
                        progressBytes.setProgress((int) (done / 1024L));
                    }
                });
            }

            public void setBlobsRemain(final int num) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        mLastBlobsRemain = num;
                        buttonToggle.setEnabled(num != 0);
                        textBlobsRemain.setText("Blobs remain: " + num);
                    }
                });
            }

            public void setUploadStatusText(final String text) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        textUploadStatus.setText(text);
                    }
                });
            }
        };

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

        bindService(new Intent(this, UploadService.class), mServiceConnection,
                Context.BIND_AUTO_CREATE);

        Intent intent = getIntent();
        String action = intent.getAction();
        Log.d(TAG, "onResume; action=" + action);

        if (Intent.ACTION_SEND.equals(action)) {
            handleSend(intent);
            setIntent(new Intent(this, CamliActivity.class));
        } else if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            handleSendMultiple(intent);
            setIntent(new Intent(this, CamliActivity.class));
            // startActivity(new Intent(this, CamliActivity.class)
            // .setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP));
        } else {
            Log.d(TAG, "Normal CamliActivity viewing.");
        }
    }

    private void handleSendMultiple(Intent intent) {
        ArrayList<Parcelable> items = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
        for (Parcelable p : items) {
            if (!(p instanceof Uri)) {
                Log.d(TAG, "uh, unknown thing " + p);
                continue;
            }
            startDownloadOfUri((Uri) p);
        }
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

    private void startDownloadOfUri(final Uri uri) {
        Log.d(TAG, "startDownload of " + uri);
        if (mServiceStub == null) {
            Log.d(TAG, "serviceStub is null in startDownloadOfUri, enqueing");
            mPendingUrisToUpload.add(uri);
            return;
        }
        new AsyncTask<Void, Void, Void>() {
            @Override
            protected Void doInBackground(Void... unused) {
                try {
                    mServiceStub.enqueueUpload(uri);
                } catch (RemoteException e) {
                    Log.d(TAG, "failure to enqueue upload", e);
                }
                return null; // void
            }
        }.execute();
    }
}
