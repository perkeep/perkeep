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
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.net.Uri;
import android.os.Bundle;
import android.os.IBinder;
import android.util.Log;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.ImageView;
import android.widget.ListView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.net.URLConnection;
import java.util.ArrayList;
import java.util.HashMap;

public class BrowseActivity extends ListActivity {
    private static final String TAG = "BrowseActivity";
    private static final String BUNDLE_BLOBREF = "blobref";
    private static final String DEFAULT_MIME_TYPE = "application/octet-stream";

    private DownloadService mService = null;
    private final EntryAdapter mAdapter = new EntryAdapter();

    private String mBlobRef = "";

    private ArrayList<Entry> mEntries = new ArrayList<Entry>();
    private HashMap<String, Entry> mEntriesByBlobRef = new HashMap<String, Entry>();
    // TODO: Remove this; it's pretty ugly.
    private HashMap<String, Entry> mEntriesByContentBlobRef = new HashMap<String, Entry>();

    // Map from the request code of an activity that we started to the file that we passed to it.
    // We keep track of this so that we can delete the file when the activity is done.
    private HashMap<Integer, File> mOutstandingActivities = new HashMap<Integer, File>();

    // Next request code to allocate for |mOutstandingActivities|.
    private int mNextActivityRequestCode = 1;

    private enum EntryType {
        UNKNOWN("unknown"),
        FILE("file"),
        DIRECTORY("directory");

        private final String mName;

        EntryType(String name) {
            mName = name;
        }

        public static EntryType fromString(String str) {
            if (str != null) {
                for (EntryType type : EntryType.values()) {
                    if (type.mName.equals(str))
                        return type;
                }
            }
            return UNKNOWN;
        }
    }

    // Represents a listed entry that the user can click (generally, a file or directory).
    // Not thread-safe.
    private class Entry {
        private final String mBlobRef;

        // Effectively-final objects initialized in updateFromJSON().
        private String mFilename = null;
        private EntryType mType = EntryType.UNKNOWN;
        private String mContentBlobRef = null;
        private long mContentLength = 0;

        Entry(String blobRef) {
            mBlobRef = blobRef;
        }

        public String getBlobRef() { return mBlobRef; }
        public String getFilename() { return mFilename; }
        public EntryType getType() { return mType; }
        public String getContentBlobRef() { return mContentBlobRef; }
        public long getContentLength() { return mContentLength; }

        public String toString() { return mFilename != null ? mFilename : mBlobRef; }

        public boolean updateFromJSON(String json) {
            try {
                JSONObject object = (JSONObject) new JSONTokener(json).nextValue();
                mFilename = object.getString("fileName");
                mType = EntryType.fromString(object.getString("camliType"));
                if (mType == EntryType.DIRECTORY) {
                    mContentBlobRef = mBlobRef;
                } else if (mType == EntryType.FILE) {
                    // TODO: Handle multi-part files, partial portions of blobs, etc.
                    JSONArray parts = object.getJSONArray("contentParts");
                    if (parts != null && parts.length() == 1) {
                        JSONObject partsObject = parts.getJSONObject(0);
                        mContentBlobRef = partsObject.getString("blobRef");
                        mContentLength = partsObject.getLong("size");
                    }
                }
                return true;
            } catch (org.json.JSONException e) {
                Log.e(TAG, "unable to parse JSON for entry " + mBlobRef, e);
                return false;
            }
        }
    }

    private class EntryAdapter extends BaseAdapter {
        @Override
        public int getCount() { return mEntries.size(); }
        @Override
        public Object getItem(int position) { return position; }
        @Override
        public long getItemId(int position) { return position; }

        @Override
        public View getView(int position, View convertView, ViewGroup parent) {
            View view;
            if (convertView != null) {
                view = convertView;
            } else {
                LayoutInflater inflater = (LayoutInflater) getSystemService(Context.LAYOUT_INFLATER_SERVICE);
                view = inflater.inflate(R.layout.browse_row, null);
            }

            Entry entry = mEntries.get(position);
            ((TextView) view.findViewById(R.id.title)).setText(entry.toString());

            ImageView icon = ((ImageView) view.findViewById(R.id.icon));
            switch (entry.getType()) {
                case DIRECTORY:
                    icon.setImageResource(R.drawable.icon_folder);
                    break;
                case FILE:
                    icon.setImageResource(R.drawable.icon_file);
                    break;
                default:
            }

            return view;
        }
    }

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

        setListAdapter(mAdapter);
    }

    @Override
    protected void onDestroy() {
        Log.d(TAG, "onDestroy");
        super.onDestroy();
        unbindService(mConnection);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        final File file = mOutstandingActivities.get(requestCode);
        if (file == null) {
            Log.e(TAG, "got unknown activity result with request code " + requestCode);
            return;
        }
        mOutstandingActivities.remove(requestCode);

        Util.runAsync(new Runnable() {
            @Override
            public void run() {
                Log.d(TAG, "deleting " + file.getPath());
                file.delete();
            }
        });
    }

    @Override
    protected void onListItemClick(ListView listView, View view, int position, long id) {
        Entry entry = mEntries.get(position);
        if (entry.getType() == EntryType.DIRECTORY) {
            if (entry.getContentBlobRef() == null) {
                Log.e(TAG, "no content for directory " + entry.getBlobRef());
                return;
            }
            Intent intent = new Intent(this, BrowseActivity.class);
            intent.putExtra(BUNDLE_BLOBREF, entry.getContentBlobRef());
            startActivity(intent);
        } else if (entry.getType() == EntryType.FILE) {
            if (entry.getContentBlobRef() == null) {
                Log.e(TAG, "no content for file " + entry.getBlobRef());
                return;
            }
            mService.getBlobAsFile(entry.getContentBlobRef(), entry.getContentLength(), mFileListener);
        }
    }

    private final ServiceConnection mConnection = new ServiceConnection() {
        public void onServiceConnected(ComponentName className, IBinder service) {
            Log.d(TAG, "connected to service");
            mService = ((DownloadService.LocalBinder) service).getService();
            if (mBlobRef.equals("")) {
                mService.getBlobAsByteArray("search", 0, mSearchResultsListener);
            } else {
                mService.getBlobAsByteArray(mBlobRef, 0, mDirectoryListener);
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
            Util.assertMainThread();
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
                    String entryBlobRef = jsonEntry.getString("content");
                    Log.d(TAG, "adding search entry " + entryBlobRef);
                    Entry entry = new Entry(entryBlobRef);
                    mEntries.add(entry);
                    mEntriesByBlobRef.put(entryBlobRef, entry);
                    mService.getBlobAsByteArray(entryBlobRef, 0, mEntryListener);
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
            Util.assertMainThread();
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
                mService.getBlobAsByteArray(entriesBlobRef, 0, mDirectoryEntriesListener);
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
            Util.assertMainThread();
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
                    Log.d(TAG, "adding directory entry " + entryBlobRef);
                    Entry entry = new Entry(entryBlobRef);
                    mEntries.add(entry);
                    mEntriesByBlobRef.put(entryBlobRef, entry);
                    mService.getBlobAsByteArray(entryBlobRef, 0, mEntryListener);
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
            Util.assertMainThread();
            Entry entry = mEntriesByBlobRef.get(blobRef);
            if (entry == null) {
                Log.e(TAG, "got unknown entry " + blobRef);
                return;
            }

            Log.d(TAG, "updating directory entry " + blobRef);
            if (entry.updateFromJSON(new String(bytes))) {
                mAdapter.notifyDataSetChanged();
                if (entry.getContentBlobRef() != null)
                    mEntriesByContentBlobRef.put(entry.getContentBlobRef(), entry);
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for entry " + blobRef);
        }
    };

    private final DownloadService.FileListener mFileListener = new DownloadService.FileListener() {
        @Override
        public void onBlobDownloadSuccess(String blobRef, File file) {
            Util.assertMainThread();
            Entry entry = mEntriesByContentBlobRef.get(blobRef);
            if (entry == null) {
                Log.e(TAG, "got unknown file content " + blobRef);
                return;
            }

            // Try to guess the mime type from the filename's extension.
            String mimeType = null;
            if (entry.getFilename() != null)
                mimeType = URLConnection.guessContentTypeFromName(entry.getFilename());
            if (mimeType == null)
                mimeType = DEFAULT_MIME_TYPE;

            Intent intent = new Intent();
            intent.setAction(intent.ACTION_VIEW);
            intent.setDataAndType(Uri.fromFile(file), mimeType);
            final int requestCode = mNextActivityRequestCode++;
            if (mOutstandingActivities.put(requestCode, file) != null)
                throw new RuntimeException("request code " + requestCode + " is already in use");
            try {
                startActivityForResult(intent, requestCode);
            } catch (android.content.ActivityNotFoundException e) {
                Toast.makeText(BrowseActivity.this, "No activity found to display " + mimeType + ".", Toast.LENGTH_SHORT).show();
            }
        }

        @Override
        public void onBlobDownloadFailure(String blobRef) {
            Log.e(TAG, "download failed for file " + blobRef);
        }
    };
}
