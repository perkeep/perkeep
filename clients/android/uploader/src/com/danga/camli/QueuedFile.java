package com.danga.camli;

import android.net.Uri;

/**
 * Immutable tuple of content name sha1 & URI to upload.
 */
public class QueuedFile {

    private final String mContentName;
    private final Uri mUri;
    private final long mSize;

    public QueuedFile(String sha1, Uri uri, long size) {
        if (sha1 == null) {
            throw new NullPointerException("sha1 == null");
        }
        if (uri == null) {
            throw new NullPointerException("uri == null");
        }
        if (sha1.length() != 40) {
            throw new IllegalArgumentException("unexpected sha1 length");
        }
        mContentName = "sha1-" + sha1;
        mUri = uri;
        mSize = size;
    }

    public String getContentName() {
        return mContentName;
    }

    public Uri getUri() {
        return mUri;
    }

    public long getSize() {
        return mSize;
    }

    @Override
    public String toString() {
        return "QueuedFile [mContentName=" + mContentName + ", mSize=" + mSize + ", mUri=" + mUri
                + "]";
    }

    @Override
    public int hashCode() {
        final int prime = 31;
        int result = 1;
        result = prime * result + ((mContentName == null) ? 0 : mContentName.hashCode());
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
        if (mContentName == null) {
            if (other.mContentName != null)
                return false;
        } else if (!mContentName.equals(other.mContentName))
            return false;
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
