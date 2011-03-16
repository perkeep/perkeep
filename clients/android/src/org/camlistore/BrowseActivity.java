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

public class BrowseActivity extends ListActivity {
    private static final String TAG = "BrowseActivity";
    private static final String BUNDLE_BLOBREF = "blobref";

    private static final String KEY_TITLE = "title";
    private static final String KEY_CONTENT = "content";
    private static final String KEY_TYPE = "type";

    private DownloadService mService = null;
    private SimpleAdapter mAdapter;

    private String mBlobRef = "";

    private ArrayList<HashMap<String, String>> mEntries =
        new ArrayList<HashMap<String, String>>();
    private HashMap<String, HashMap<String, String>> mEntriesByBlobRef =
        new HashMap<String, HashMap<String, String>>();

    private final ServiceConnection mConnection = new ServiceConnection() {
        public void onServiceConnected(ComponentName className, IBinder service) {
            Log.d(TAG, "connected to service");
            mService = ((DownloadService.LocalBinder) service).getService();
            if (mBlobRef.equals("")) {
                mService.getBlobAsByteArray("search", mSearchResultsListener);
            } else {
                mService.getBlobAsByteArray(mBlobRef, mDirectoryListener);
            }
        }

        public void onServiceDisconnected(ComponentName className) {
            Log.d(TAG, "disconnected from service");
            mService = null;
        }
    };

    private final DownloadService.ByteArrayListener mSearchResultsListener = new DownloadService.ByteArrayListener() {
        @Override
        public void onBlobDownloadSuccess(String blobRef, byte[] bytes) {
            try {
                JSONObject object = (JSONObject) new JSONTokener(new String(bytes)).nextValue();
                JSONArray array = object.getJSONArray("results");
                if (array == null) {
                    Log.e(TAG, "search results are missing results key");
                    return;
                }

                mEntries.clear();
                for (int i = 0; i < array.length(); ++i) {
                    JSONObject jsonEntry = array.getJSONObject(i);
                    Log.d(TAG, "adding entry " + jsonEntry.getString("blobref"));
                    HashMap<String, String> entry = new HashMap<String, String>();
                    entry.put(KEY_TITLE, jsonEntry.getString("blobref"));
                    entry.put(KEY_CONTENT, jsonEntry.getString("content"));
                    mEntries.add(entry);
                    mEntriesByBlobRef.put(jsonEntry.getString("blobref"), entry);
                }
                mAdapter.notifyDataSetChanged();
            } catch (org.json.JSONException e) {
                Log.e(TAG, "unable to parse JSON for search results", e);
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for search results");
        }
    };

    private final DownloadService.ByteArrayListener mDirectoryListener = new DownloadService.ByteArrayListener() {
        @Override
        public void onBlobDownloadSuccess(String blobRef, byte[] bytes) {
            try {
                JSONObject object = (JSONObject) new JSONTokener(new String(bytes)).nextValue();
                String type = object.getString("camliType");
                if (type == null || !type.equals("directory")) {
                    Log.e(TAG, "directory " + blobRef + " has missing or invalid type");
                    return;
                }

                String fileName = object.getString("fileName");
                if (fileName == null) {
                    Log.e(TAG, "directory " + blobRef + " doesn't have fileName");
                    return;
                }
                setTitle(fileName + "/");

                String entriesBlobRef = object.getString("entries");
                if (entriesBlobRef == null) {
                    Log.e(TAG, "directory " + blobRef + " doesn't have entries");
                    return;
                }

                Log.d(TAG, "requesting directory entries " + entriesBlobRef);
                mService.getBlobAsByteArray(entriesBlobRef, mDirectoryEntriesListener);
            } catch (org.json.JSONException e) {
                Log.e(TAG, "unable to parse JSON for search results", e);
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for directory " + blobRef);
        }
    };

    private final DownloadService.ByteArrayListener mDirectoryEntriesListener = new DownloadService.ByteArrayListener() {
        @Override
        public void onBlobDownloadSuccess(String blobRef, byte[] bytes) {
            try {
                JSONObject object = (JSONObject) new JSONTokener(new String(bytes)).nextValue();
                String type = object.getString("camliType");
                if (type == null || !type.equals("static-set")) {
                    Log.e(TAG, "directory list " + blobRef + " has missing or invalid camliType");
                    return;
                }

                JSONArray members = object.getJSONArray("members");
                if (members == null) {
                    Log.e(TAG, "directory list " + blobRef + " has no members key");
                    return;
                }

                mEntries.clear();
                for (int i = 0; i < members.length(); ++i) {
                    String entryBlobRef = members.getString(i);
                    mService.getBlobAsByteArray(entryBlobRef, mEntryListener);

                    Log.d(TAG, "adding directory entry " + entryBlobRef);
                    HashMap<String, String> entry = new HashMap<String, String>();
                    entry.put(KEY_TITLE, entryBlobRef);
                    entry.put(KEY_CONTENT, entryBlobRef);
                    mEntries.add(entry);
                    mEntriesByBlobRef.put(entryBlobRef, entry);
                }
                mAdapter.notifyDataSetChanged();
            } catch (org.json.JSONException e) {
                Log.e(TAG, "unable to parse JSON for directory list " + blobRef, e);
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for directory list " + blobRef);
        }
    };

    private final DownloadService.ByteArrayListener mEntryListener = new DownloadService.ByteArrayListener() {
        @Override
        public void onBlobDownloadSuccess(String blobRef, byte[] bytes) {
            try {
                HashMap<String, String> entry = mEntriesByBlobRef.get(blobRef);
                if (entry == null) {
                    Log.e(TAG, "got unknown entry " + blobRef);
                    return;
                }

                JSONObject object = (JSONObject) new JSONTokener(new String(bytes)).nextValue();
                String fileName = object.getString("fileName");
                String type = object.getString("camliType");
                if (fileName == null || type == null) {
                    Log.e(TAG, "entry " + blobRef + " is missing filename or type");
                    return;
                }

                Log.d(TAG, "updating directory entry " + blobRef + " to " + fileName);
                entry.put(KEY_TITLE, fileName);
                entry.put(KEY_TYPE, type);
                mAdapter.notifyDataSetChanged();
            } catch (org.json.JSONException e) {
                Log.e(TAG, "unable to parse JSON for entry " + blobRef, e);
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for entry " + blobRef);
        }
    };

    @Override
    public void onCreate(Bundle savedInstanceState) {
        Log.d(TAG, "onCreate");
        super.onCreate(savedInstanceState);

        String blobRef = getIntent().getStringExtra(BUNDLE_BLOBREF);
        if (blobRef != null && !blobRef.equals(""))
            mBlobRef = blobRef;
        setTitle(mBlobRef.equals("") ? getString(R.string.results) : mBlobRef);

        Intent serviceIntent = new Intent(this, DownloadService.class);
        startService(serviceIntent);
        bindService(new Intent(this, DownloadService.class), mConnection, 0);

        mAdapter = new SimpleAdapter(
            this,
            mEntries,
            R.layout.browse_row,
            new String[]{ KEY_TITLE },
            new int[]{ android.R.id.title });
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
}
