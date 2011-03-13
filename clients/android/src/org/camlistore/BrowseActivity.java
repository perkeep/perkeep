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

import android.app.ListActivity;
import android.content.ComponentName;
import android.content.Intent;
import android.content.ServiceConnection;
import android.os.Bundle;
import android.os.IBinder;
import android.util.Log;
import android.view.View;
import android.widget.ListView;
import android.widget.SimpleAdapter;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

import java.io.InputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.HashMap;

public class BrowseActivity extends ListActivity
                            implements DownloadService.Listener {
    private static final String TAG = "BrowseActivity";
    private static final String BUNDLE_BLOBREF = "blobref";

    private static final String KEY_TITLE = "title";
    private static final String KEY_CONTENT = "content";
    private static final String KEY_TYPE = "type";

    private DownloadService mService = null;
    private SimpleAdapter mAdapter;

    private String mDirectoryBlobRef = "";
    private String mDirectoryEntriesBlobRef = "";

    // If true, we're showing the results of a search.
    private boolean mIsSearch;

    private ArrayList<HashMap<String, String>> mEntries =
        new ArrayList<HashMap<String, String>>();
    private HashMap<String, HashMap<String, String>> mEntriesByBlobRef =
        new HashMap<String, HashMap<String, String>>();

    private final ServiceConnection mConnection = new ServiceConnection() {
        public void onServiceConnected(ComponentName className, IBinder service) {
            Log.d(TAG, "connected to service");
            mService = ((DownloadService.LocalBinder) service).getService();
            mService.getBlob(mDirectoryBlobRef.equals("") ? "search" : mDirectoryBlobRef,
                             !mDirectoryBlobRef.equals(""),  // persistent
                             BrowseActivity.this);
        }

        public void onServiceDisconnected(ComponentName className) {
            Log.d(TAG, "disconnected from service");
            mService = null;
        }
    };

    @Override
    public void onCreate(Bundle savedInstanceState) {
        Log.d(TAG, "onCreate");
        super.onCreate(savedInstanceState);

        String blobRef = getIntent().getStringExtra(BUNDLE_BLOBREF);
        if (blobRef != null && !blobRef.equals(""))
            mDirectoryBlobRef = blobRef;
        setTitle(mDirectoryBlobRef.equals("") ? getString(R.string.results) : mDirectoryBlobRef);

        Intent serviceIntent = new Intent(this, DownloadService.class);
        startService(serviceIntent);
        bindService(new Intent(this, DownloadService.class), mConnection, 0);

        mAdapter = new SimpleAdapter(
            this,
            mEntries,
            android.R.layout.simple_list_item_1,
            new String[]{ KEY_TITLE },
            new int[]{ android.R.id.text1 });
        setListAdapter(mAdapter);
    }

    @Override
    protected void onDestroy() {
        Log.d(TAG, "onDestroy");
        super.onDestroy();
        unbindService(mConnection);
    }

    @Override
    protected void onListItemClick(ListView listView, View view, int position, long id) {
        Intent intent = new Intent(this, BrowseActivity.class);
        HashMap<String, String> blob = mEntries.get(position);
        intent.putExtra(BUNDLE_BLOBREF, blob.get(KEY_CONTENT));
        startActivity(intent);
    }

    // Implements DownloadService.Listener.
    @Override
    public void onBlobDownloadComplete(final String blobRef, final InputStream stream) {
        runOnUiThread(new Runnable() {
            @Override
            public void run() {
                try {
                    String json = Util.slurp(stream);
                    Log.d(TAG, "got reply: " + json);

                    if (blobRef.equals("search")) {
                        parseSearchResults(json);
                    } else if (blobRef.equals(mDirectoryBlobRef)) {
                        parseDirectory(json);
                    } else if (blobRef.equals(mDirectoryEntriesBlobRef)) {
                        parseDirectoryEntries(json);
                    } else if (mEntriesByBlobRef.get(blobRef) != null) {
                        parseEntry(blobRef, json);
                    }
                } catch (IOException e) {
                }
            }
        });
    }

    // Implements DownloadService.Listener.
    @Override
    public void onBlobDownloadFail(final String blobRef) {
        runOnUiThread(new Runnable() {
            @Override
            public void run() {
                Toast.makeText(BrowseActivity.this, "Download failed.", Toast.LENGTH_SHORT).show();
            }
        });
    }

    private boolean parseSearchResults(String json) {
        try {
            JSONObject object = (JSONObject) new JSONTokener(json).nextValue();
            JSONArray array = object.getJSONArray("results");
            if (array == null)
                return false;

            mEntries.clear();
            for (int i = 0; i < array.length(); ++i) {
                JSONObject jsonBlob = array.getJSONObject(i);

                String title = "";
                JSONObject jsonAttributes = jsonBlob.getJSONObject("attr");
                if (jsonAttributes != null) {
                    JSONArray jsonTitle = jsonAttributes.getJSONArray("title");
                    if (jsonTitle != null && jsonTitle.length() > 0)
                        title = jsonTitle.getString(0);
                }
                if (title.equals(""))
                    title = jsonBlob.getString("blobref");

                Log.d(TAG, "adding entry " + title);
                HashMap<String, String> entry = new HashMap<String, String>();
                entry.put(KEY_TITLE, title);
                entry.put(KEY_CONTENT, jsonBlob.getString("content"));
                mEntries.add(entry);
                mEntriesByBlobRef.put(jsonBlob.getString("blobref"), entry);
            }
        } catch (org.json.JSONException e) {
            return false;
        }

        mAdapter.notifyDataSetChanged();
        return true;
    }

    private boolean parseDirectory(String json) {
        try {
            JSONObject object = (JSONObject) new JSONTokener(json).nextValue();
            String type = object.getString("camliType");
            if (type == null || !type.equals("directory"))
                return false;

            String fileName = object.getString("fileName");
            if (fileName == null)
                return false;
            setTitle(fileName + "/");

            String entriesBlobRef = object.getString("entries");
            if (entriesBlobRef == null)
                return false;

            Log.d(TAG, "requesting directory entries " + entriesBlobRef);
            mDirectoryEntriesBlobRef = entriesBlobRef;
            mService.getBlob(entriesBlobRef, true, BrowseActivity.this);

        } catch (org.json.JSONException e) {
            return false;
        }

        return true;
    }

    private boolean parseDirectoryEntries(String json) {
        try {
            JSONObject object = (JSONObject) new JSONTokener(json).nextValue();
            String type = object.getString("camliType");
            if (type == null || !type.equals("static-set"))
                return false;

            JSONArray members = object.getJSONArray("members");
            if (members == null)
                return false;

            mEntries.clear();
            for (int i = 0; i < members.length(); ++i) {
                String blobRef = members.getString(i);
                mService.getBlob(blobRef, true, BrowseActivity.this);

                Log.d(TAG, "adding directory entry " + blobRef);
                HashMap<String, String> entry = new HashMap<String, String>();
                entry.put(KEY_TITLE, blobRef);
                entry.put(KEY_CONTENT, blobRef);
                mEntries.add(entry);
                mEntriesByBlobRef.put(blobRef, entry);
            }

        } catch (org.json.JSONException e) {
            return false;
        }

        mAdapter.notifyDataSetChanged();
        return true;
    }

    private boolean parseEntry(String blobRef, String json) {
        try {
            HashMap<String, String> entry = mEntriesByBlobRef.get(blobRef);
            if (entry == null)
                return false;

            JSONObject object = (JSONObject) new JSONTokener(json).nextValue();
            String fileName = object.getString("fileName");
            String type = object.getString("camliType");
            if (fileName == null || type == null)
                return false;

            Log.d(TAG, "updating directory entry " + blobRef + " to " + fileName);
            entry.put(KEY_TITLE, fileName);
            entry.put(KEY_TYPE, type);

        } catch (org.json.JSONException e) {
            return false;
        }

        mAdapter.notifyDataSetChanged();
        return true;
    }
}
