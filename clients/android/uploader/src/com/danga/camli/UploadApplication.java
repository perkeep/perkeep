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