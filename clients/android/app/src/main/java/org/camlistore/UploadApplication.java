/*
Copyright 2011 The Perkeep Authors

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

import java.io.IOException;
import java.io.InputStreamReader;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.Scanner;

import android.app.Application;
import android.util.Log;


public class UploadApplication extends Application {
    private final static String TAG = "UploadApplication";
    private final static boolean STRICT_MODE = true;

    @Override
    public void onCreate() {
        super.onCreate();

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
            Class<?> strictmode = Class.forName("android.os.StrictMode");
            Log.d(TAG, "StrictMode class found.");
            Method method = strictmode.getMethod("enableDefaults");
            Log.d(TAG, "enableDefaults method found.");
            method.invoke(null);
        } catch (ClassNotFoundException | LinkageError | IllegalAccessException | NoSuchMethodException | SecurityException | InvocationTargetException ignored) {
        }
    }

    private String getPkBin() {
        return getApplicationInfo().nativeLibraryDir + "/libpkput.so";
    }

    public String getPkPutVersion() {
        String prefix = getPkBin() + " version:";
        try {
            ProcessBuilder pb = new ProcessBuilder();
            pb.command(getPkBin(), "-version");
            pb.redirectErrorStream(true);
            Scanner scanner = new java.util.Scanner(new InputStreamReader(pb.start().getInputStream())).useDelimiter("\\A");
            String versionOutput = scanner.hasNext() ? scanner.next() : "";
            if (versionOutput.startsWith(prefix)) {
                return versionOutput.substring(prefix.length());
            }
            return versionOutput;
        } catch (IOException e) {
            return e.toString();
        }
    }
}
