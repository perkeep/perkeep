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

import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileDescriptor;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.concurrent.locks.ReentrantLock;

import android.os.AsyncTask;
import android.os.Looper;
import android.util.Base64;
import android.util.Log;

public class Util {
    private static final String TAG = "Camli_Util";

    public static String slurp(InputStream in) throws IOException {
        StringBuilder sb = new StringBuilder();
        byte[] b = new byte[4096];
        for (int n; (n = in.read(b)) != -1;) {
            sb.append(new String(b, 0, n));
        }
        return sb.toString();
    }

    public static byte[] slurpToByteArray(InputStream inputStream) throws IOException {
        ByteArrayOutputStream outputStream = new ByteArrayOutputStream();
        byte[] buffer = new byte[4096];
        for (int numRead; (numRead = inputStream.read(buffer)) != -1;) {
            outputStream.write(buffer, 0, numRead);
        }
        return outputStream.toByteArray();
    }

    public static void copyFile(File fromFile, File toFile) throws IOException {
        FileInputStream inputStream = new FileInputStream(fromFile);
        FileOutputStream outputStream = new FileOutputStream(toFile);
        byte[] buffer = new byte[4096];
        for (int numRead; (numRead = inputStream.read(buffer)) != -1;)
            outputStream.write(buffer, 0, numRead);
        inputStream.close();
        outputStream.close();
    }

    public static void runAsync(final Runnable r) {
        new AsyncTask<Void, Void, Void>() {
            @Override
            protected Void doInBackground(Void... unused) {
                r.run();
                return null;
            }
        }.execute();
    }

    public static boolean onMainThread() {
        return Looper.myLooper() == Looper.getMainLooper();
    }

    public static void assertMainThread() {
        if (!onMainThread()) {
            throw new RuntimeException("Assert: unexpected call off the main thread");
        }
    }

    public static void assertNotMainThread() {
        if (onMainThread()) {
            throw new RuntimeException("Assert: unexpected call on main thread");
        }
    }

    // Asserts that |lock| is held by the current thread.
    public static void assertLockIsHeld(ReentrantLock lock) {
        if (!lock.isHeldByCurrentThread()) {
            throw new RuntimeException("Assert: mandatory lock isn't held by current thread");
        }
    }

    // Asserts that |lock| is not held by the current thread.
    public static void assertLockIsNotHeld(ReentrantLock lock) {
        if (lock.isHeldByCurrentThread()) {
            throw new RuntimeException("Assert: lock is held by current thread but shouldn't be");
        }
    }

    private static final String HEX = "0123456789abcdef";

    public static String getHex(byte[] raw) {
        if (raw == null) {
            return null;
        }
        final StringBuilder hex = new StringBuilder(2 * raw.length);
        for (final byte b : raw) {
            hex.append(HEX.charAt((b & 0xF0) >> 4)).append(
                    HEX.charAt((b & 0x0F)));
        }
        return hex.toString();
    }

    // Requires that the fd be seeked to the beginning.
    public static String getSha1(FileDescriptor fd) {
        MessageDigest md;
        try {
            md = MessageDigest.getInstance("SHA-1");
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException(e);
        }
        byte[] b = new byte[4096];
        FileInputStream fis = new FileInputStream(fd);
        InputStream is = new BufferedInputStream(fis, 4096);
        try {
            for (int n; (n = is.read(b)) != -1;) {
                md.update(b, 0, n);
            }
        } catch (IOException e) {
            Log.w(TAG, "IOException while computing SHA-1");
            return null;
        }
        byte[] sha1hash = new byte[40];
        sha1hash = md.digest();
        return getHex(sha1hash);
    }

    public static String getBasicAuthHeaderValue(String username, String password) {
        return "Basic " + Base64.encodeToString((username + ":" + password).getBytes(),
                                                Base64.NO_WRAP);
    }
}
