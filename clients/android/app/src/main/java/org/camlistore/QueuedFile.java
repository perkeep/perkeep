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

import android.net.Uri;

/**
 * Immutable struct for tuple (sha1 blobRef, URI to upload, size of blob).
 */
public class QueuedFile {

    private final Uri mUri;
    private final long mSize;
    private final String mDiskPath;  // or null if it can't be resolved.

    public QueuedFile(Uri uri, long size, String diskPath) {
        if (uri == null) {
            throw new NullPointerException("uri == null");
        }
        mUri = uri;
        mSize = size;
        mDiskPath = diskPath;
    }

    public Uri getUri() {
        return mUri;
    }

    public long getSize() {
        return mSize;
    }

    // getDiskPath may return null, if the URI couldn't be resolved to a path on disk.
    public String getDiskPath() {
        return mDiskPath;
    }

    @Override
    public String toString() {
        return "QueuedFile [mSize=" + mSize + ", mUri=" + mUri + "]";
    }

    @Override
    public int hashCode() {
        final int prime = 31;
        int result = 1;
        result = prime * result + (int) (mSize ^ (mSize >>> 32));
        result = prime * result + ((mUri == null) ? 0 : mUri.hashCode());
        return result;
    }

    @Override
    public boolean equals(Object obj) {
        if (this == obj)
            return true;
        if (obj == null)
            return false;
        if (getClass() != obj.getClass())
            return false;
        QueuedFile other = (QueuedFile) obj;
        if (mSize != other.mSize)
            return false;
        if (mUri == null) {
            if (other.mUri != null)
                return false;
        } else if (!mUri.equals(other.mUri))
            return false;
        return true;
    }
}
