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

import android.util.Log;
import android.util.Pair;

import java.io.File;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.concurrent.locks.Condition;
import java.util.concurrent.locks.ReentrantLock;
import java.util.HashSet;

class DownloadCache {
    private static final String TAG = "DownloadCache";
    private static final String PARTIAL_DOWNLOAD_SUFFIX = ".partial";

    // FIXME: Move this to a pref.
    private static final long MAX_CACHE_BYTES = 256 * 1024 * 1024;

    // Directory where we store blobs.
    private final File mBlobDir;

    private final ReentrantLock mLock = new ReentrantLock();

    // Is the cache ready?  Transitions from false to true exactly once.  Protected by |mLock|.
    private boolean mIsReady = false;

    // Used to wait for |mIsReady| to become true.
    private final Condition mIsReadyCondition = mLock.newCondition();

    // Current size used by the cache, in bytes.  Protected by |mLock|.
    private long mUsedBytes = 0;

    // Pathnames of cache files that shouldn't be deleted (typically because they're being used).
    // Protected by |mLock|.
    private HashSet<String> mPinnedPaths = new HashSet<String>();

    DownloadCache(String path) {
        mBlobDir = new File(path);
        mBlobDir.mkdirs();

        // Compute the starting size of the cache.
        Util.runAsync(new Runnable() {
            @Override
            public void run() {
                mLock.lock();
                try {
                    mUsedBytes = 0;
                    for (File file : mBlobDir.listFiles()) {
                        mUsedBytes += file.length();
                    }
                    Log.d(TAG, "cache is ready; currently has " + mUsedBytes +
                          " byte(s) of " + MAX_CACHE_BYTES + " max");
                    mIsReady = true;
                    mIsReadyCondition.signal();
                } finally {
                    mLock.unlock();
                }
            }
        });
    }

    // Get a file for accessing |blobRef| or null if it isn't cached.
    // Note that the file may disappear at any time if it gets evicted from the cache.
    public File getFileForBlob(String blobRef) {
        Util.assertNotMainThread();

        // We don't depend on anything protected by |mLock| or guarded by |mIsReady|.
        File file = new File(mBlobDir, blobRef);
        if (!file.exists())
            return null;

        // Update the file's mtime in response to the access.
        file.setLastModified(System.currentTimeMillis());
        return file;
    }

    // Get a temporary file to which |blobRef| can be downloaded.
    // The underlying file will not be created; the caller must call createNewFile().
    // In any case, the caller must call handleDoneWritingTempFile() when done using the file.
    public File getTempFileForDownload(String blobRef) {
        Util.assertNotMainThread();
        File file = new File(mBlobDir, blobRef + PARTIAL_DOWNLOAD_SUFFIX);

        mLock.lock();
        try {
            while (!mIsReady)
                try { mIsReadyCondition.await(); } catch (InterruptedException e) {}
            if (!mPinnedPaths.add(file.getAbsolutePath()))
                throw new RuntimeException("temp file " + file.getPath() + " for " + blobRef + " already in use");
        } finally {
            mLock.unlock();
        }

        return file;
    }

    // Handle the completion (either successful or not) of a download to a file returned by getTempFileForDownload().
    // If |successfullyWritten| is true, |tempFile| will be renamed to indicate that it's fully downloaded and the
    // file's final location will be returned.
    public File handleDoneWritingTempFile(File tempFile, boolean successfullyWritten) {
        Util.assertNotMainThread();
        mLock.lock();
        try {
            while (!mIsReady)
                try { mIsReadyCondition.await(); } catch (InterruptedException e) {}
            mUsedBytes += tempFile.length();
            if (!mPinnedPaths.remove(tempFile.getAbsolutePath()))
                throw new RuntimeException("unknown temp file " + tempFile.getPath());
        } finally {
            mLock.unlock();
        }

        if (!successfullyWritten)
            return null;

        final String name = tempFile.getName();
        if (!name.endsWith(PARTIAL_DOWNLOAD_SUFFIX))
            throw new RuntimeException("invalid cache filename \"" + name + "\"");
        File newFile = new File(mBlobDir, name.substring(0, name.length() - PARTIAL_DOWNLOAD_SUFFIX.length()));
        Log.d(TAG, "renaming " + tempFile.getPath() + " to " + newFile.getPath());
        return tempFile.renameTo(newFile) ? newFile : null;
    }

    // Try to make space for |neededBytes| bytes.
    // Returns true if successful and false otherwise.
    public boolean makeSpace(long neededBytes) {
        Util.assertNotMainThread();
        mLock.lock();
        try {
            while (!mIsReady)
                try { mIsReadyCondition.await(); } catch (InterruptedException e) {}
            Log.d(TAG, "making space for " + neededBytes + " byte(s) " +
                  "(using " + mUsedBytes + ", max is " + MAX_CACHE_BYTES + ")");

            if (neededBytes > MAX_CACHE_BYTES)
                return false;

            long freeBytes = MAX_CACHE_BYTES - mUsedBytes;
            if (freeBytes >= neededBytes)
                return true;

            // Pairs of (mtime, File), sorted by ascending mtime.
            ArrayList<Pair<Long, File>> filesByMtime = new ArrayList<Pair<Long, File>>();
            for (File file : mBlobDir.listFiles())
                filesByMtime.add(new Pair<Long, File>(file.lastModified(), file));
            Collections.sort(filesByMtime, new Comparator() {
                @Override
                public int compare(Object a, Object b) {
                    return compare(((Pair<Long, File>) a).first, ((Pair<Long, File>) b).first);
                }
            });

            while (freeBytes < neededBytes && !filesByMtime.isEmpty()) {
                File file = filesByMtime.get(0).second;
                filesByMtime.remove(0);

                if (mPinnedPaths.contains(file.getAbsolutePath()))
                    continue;

                final long fileBytes = file.length();
                Log.d(TAG, "deleting " + file.getPath() + " of length " + fileBytes);
                if (file.delete()) {
                    mUsedBytes -= fileBytes;
                    freeBytes += fileBytes;
                } else {
                    Log.e(TAG, "failed to delete " + file.getPath());
                }
            }
            return (freeBytes >= neededBytes);
        } finally {
            mLock.unlock();
        }
    }

}
