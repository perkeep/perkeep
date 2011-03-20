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

import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

// Used to verify that the digest of a blob's contents match the digest from its blobref.
class BlobVerifier {
    private static final String TAG = "BlobVerifier";
    private static final String SHA1_PREFIX = "sha1-";
    private static final String MD5_PREFIX = "md5-";

    private final String mBlobRef;

    // Effectively-final members assigned either in the c'tor or in a method that it calls.
    private String mExpectedDigest;
    private MessageDigest mDigester;

    // Initializes a verifier for |blobRef|.
    BlobVerifier(String blobRef) {
        mBlobRef = blobRef;

        if (mBlobRef.startsWith(SHA1_PREFIX)) {
            initForAlgorithm(SHA1_PREFIX, "SHA-1");
        } else if (mBlobRef.startsWith(MD5_PREFIX)) {
            initForAlgorithm(MD5_PREFIX, "MD5");
        }
    }

    // Update the digest using the blob's bytes.
    // Can be invoked repeatedly with successive chunks as the blob is being downloaded.
    public void processBytes(byte[] bytes, int offset, int length) {
        if (mDigester != null)
            mDigester.update(bytes, offset, length);
    }

    // Do the blob's contents match its blobref?
    // Returns true if the contents are valid or if the blob is using an unknown algorithm.
    public boolean isBlobValid() {
        if (mDigester == null)
            return true;

        final String actualDigest = Util.getHex(mDigester.digest());
        return actualDigest.equals(mExpectedDigest);
    }

    // Helper method called by the constructor.
    // Initializes |mExpectedDigest| and |mDigester| for a blobref starting with |blobRefPrefix|
    // and that's using |algorithmName| (an algorithm known by MessageDigest).
    private void initForAlgorithm(String blobRefPrefix, String algorithmName) {
        if (!mBlobRef.startsWith(blobRefPrefix))
            throw new RuntimeException("blobref " + mBlobRef + " doesn't start with " + blobRefPrefix);
        mExpectedDigest = mBlobRef.substring(blobRefPrefix.length(), mBlobRef.length()).toLowerCase();

        try {
            mDigester = MessageDigest.getInstance(algorithmName);
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException(e);
        }
    }
}
