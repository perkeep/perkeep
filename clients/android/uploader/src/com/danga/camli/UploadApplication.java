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

package com.danga.camli;

import android.app.Application;
import android.util.Config;
import android.util.Log;

import java.lang.reflect.Method;

public class UploadApplication extends Application {
    private final static String TAG = "UploadApplication";
    private final static boolean STRICT_MODE = true;

    public void onCreate() {
        super.onCreate();
        if (!STRICT_MODE) {
            Log.d(TAG, "Starting UploadApplication; release build.");
            return;
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

}