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

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.lang.reflect.Method;

import android.app.Application;
import android.content.pm.PackageManager.NameNotFoundException;
import android.util.Log;

public class UploadApplication extends Application {
    private final static String TAG = "UploadApplication";
    private final static boolean STRICT_MODE = true;

    private long getAPKModTime() {
        try {
            return getPackageManager().getPackageInfo(getPackageName(), 0).lastUpdateTime;
        } catch (NameNotFoundException e) {
            throw new RuntimeException(e);
        }
    }

    private void copyGoBinary() {
        long myTime = getAPKModTime();
        String dstFile = getBaseContext().getFilesDir().getAbsolutePath() + "/camput.bin";
        File f = new File(dstFile);
        Log.d(TAG, " My Time: " + myTime);
        Log.d(TAG, "Bin Time: " + f.lastModified());
        if (f.exists() && f.lastModified() > myTime) {
            Log.d(TAG, "Go binary modtime up-to-date.");
            return;
        }
        Log.d(TAG, "Go binary missing or modtime stale. Re-copying from APK.");
        try {
            InputStream is = getAssets().open("camput.arm");
            FileOutputStream fos = getBaseContext().openFileOutput("camput.bin.writing", MODE_PRIVATE);
            byte[] buf = new byte[8192];
            int offset;
            while ((offset = is.read(buf)) > 0) {
                fos.write(buf, 0, offset);
            }
            is.close();
            fos.flush();
            fos.close();

            String writingFilePath = dstFile + ".writing";
            Log.d(TAG, "wrote out " + writingFilePath);
            Runtime.getRuntime().exec("chmod 0777 " + writingFilePath);
            Log.d(TAG, "did chmod 0700 on " + writingFilePath);
            Runtime.getRuntime().exec("mv " + writingFilePath + " " + dstFile);
            f = new File(dstFile);
            f.setLastModified(myTime);
            Log.d(TAG, "set modtime of " + dstFile);
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
    }

    @Override
    public void onCreate() {
        super.onCreate();

        copyGoBinary();

        if (!STRICT_MODE) {
            Log.d(TAG, "Starting UploadApplication; release build.");
            return;
        }

        try {
            Runtime.getRuntime().exec("chmod 0755 " + getCacheDir().getAbsolutePath());
        } catch (IOException e) {
            Log.d(TAG, "failed to chmod cache dir");
        }

        try {
            Class strictmode = Class.forName("android.os.StrictMode");
            Log.d(TAG, "StrictMode class found.");
            Method method = strictmode.getMethod("enableDefaults");
            Log.d(TAG, "enableDefaults method found.");
            method.invoke(null);
        } catch (ClassNotFoundException e) {
        } catch (LinkageError e) {
        } catch (IllegalAccessException e) {
        } catch (NoSuchMethodException e) {
        } catch (SecurityException e) {
        } catch (java.lang.reflect.InvocationTargetException e) {
        }
    }

    public String getCamputVersion() {
        InputStream is = null;
        try {
            is = getAssets().open("camput-version.txt");
            BufferedReader br = new BufferedReader(new InputStreamReader(is, "UTF-8"));
            return br.readLine();
        } catch (IOException e) {
            return e.toString();
        } finally {
            if (is != null) {
                try {
                    is.close();
                } catch (IOException e) {
                }
            }
        }
    }
}
