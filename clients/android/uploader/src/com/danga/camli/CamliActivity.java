package com.danga.camli;

import java.util.ArrayList;

import android.app.Activity;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.content.SharedPreferences;
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
    private static final int MENU_STOP = 2;
    private static final int MENU_UPLOAD_ALL = 3;

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
                if (!mPendingUrisToUpload.isEmpty()) {
                    // Drain the queue from before the service was connected.
                    startDownloadOfUriList(mPendingUrisToUpload);
                    mPendingUrisToUpload.clear();
                }
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

            public void logToClient(String stuff) throws RemoteException {
                // TODO Auto-generated method stub

            }

            public void setUploading(final boolean uploading) throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        if (uploading) {
                            buttonToggle.setText(R.string.pause);
                            textStatus.setText(R.string.uploading);
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

            public void setBlobStatus(final int blobsDone, final int inFlight, final int total)
                    throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        boolean finished = (blobsDone == total && mLastBlobsDigestRemain == 0);
                        buttonToggle.setEnabled(!finished);
                        progressBlob.setMax(total);
                        progressBlob.setProgress(blobsDone);
                        progressBlob.setSecondaryProgress(blobsDone + inFlight);
                        if (finished) {
                            buttonToggle.setText(getString(R.string.pause_resume));
                        }
                    }
                });
            }

            public void setByteStatus(final long done, final int inFlight, final long total)
                    throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        // setMax takes an (signed) int, but 2GB is a totally
                        // reasonable upload size, so use units of 1KB instead.
                        progressBytes.setMax((int) (total / 1024L));
                        progressBytes.setProgress((int) (done / 1024L));
                        progressBytes.setSecondaryProgress(progressBytes.getProgress() + inFlight
                                / 1024);
                    }
                });
            }

            public void setBlobsRemain(final int toUpload, final int toDigest)
                    throws RemoteException {
                mHandler.post(new Runnable() {
                    public void run() {
                        mLastBlobsUploadRemain = toUpload;
                        mLastBlobsDigestRemain = toDigest;

                        buttonToggle.setEnabled((toUpload + toDigest) != 0);
                        StringBuilder sb = new StringBuilder(40);
                        sb.append("Blobs to upload: ").append(toUpload);
                        if (toDigest > 0) {
                            sb.append("; to digest: ").append(toDigest);
                        }
                        textBlobsRemain.setText(sb.toString());
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
        menu.add(Menu.NONE, MENU_UPLOAD_ALL, 0, "Upload All");
        menu.add(Menu.NONE, MENU_STOP, 0, "Stop");
        menu.add(Menu.NONE, MENU_SETTINGS, 0, "Settings");
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
        case MENU_SETTINGS:
            SettingsActivity.show(this);
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

        SharedPreferences sp = getSharedPreferences(Preferences.NAME, 0);
        HostPort hp = new HostPort(sp.getString(Preferences.HOST, ""));
        if (!hp.isValid()) {
            // Crashes oddly in some Android Instrumentation thing if
            // uncommented:
            // SettingsActivity.show(this);
            // return;
        }

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
        } else {
            Log.d(TAG, "Normal CamliActivity viewing.");
        }
    }

    private void handleSendMultiple(Intent intent) {
        ArrayList<Parcelable> items = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
        ArrayList<Uri> uris = new ArrayList<Uri>(items.size());
        for (Parcelable p : items) {
            if (!(p instanceof Uri)) {
                Log.d(TAG, "uh, unknown thing " + p);
                continue;
            }
            uris.add((Uri) p);
        }
        startDownloadOfUriList(uris);
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

    private void startDownloadOfUriList(ArrayList<Uri> uriList) {
        // We need to make a copy of it for our AsyncTask, as our caller may
        // clear their owned copy of it before our AsyncTask runs.
        final ArrayList<Uri> uriListCopy = new ArrayList<Uri>(uriList);

        Log.d(TAG, "startDownload of list: " + uriListCopy);
        if (mServiceStub == null) {
            Log.d(TAG, "serviceStub is null in startDownloadOfUri, enqueing");
            mPendingUrisToUpload.addAll(uriListCopy);
            return;
        }
        new AsyncTask<Void, Void, Void>() {
            @Override
            protected Void doInBackground(Void... unused) {
                try {
                    Log.d(TAG, "From AsyncTask thread, enqueing uriList of size "
                            + uriListCopy.size());
                    mServiceStub.enqueueUploadList(uriListCopy);
                } catch (RemoteException e) {
                    Log.d(TAG, "failure to enqueue upload", e);
                }
                return null; // void
            }
        }.execute();
    }

}
