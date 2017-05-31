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

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.net.ConnectivityManager;
import android.net.NetworkInfo;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;
import android.util.Log;

public class WifiPowerReceiver extends BroadcastReceiver {
    private static final String TAG = "WifiPowerReceiver";

    @Override
    public void onReceive(Context context, Intent intent) {
        String action = intent.getAction();
        Log.d(TAG, "intent: " + intent);
        if (Intent.ACTION_POWER_CONNECTED.equals(action)) {
            Intent cmd = new Intent(UploadService.INTENT_POWER_CONNECTED);
            cmd.setClass(context, UploadService.class);
            context.startService(cmd);
            return;
        }

        if (Intent.ACTION_POWER_DISCONNECTED.equals(action)) {
            Intent cmd = new Intent(UploadService.INTENT_POWER_DISCONNECTED);
            cmd.setClass(context, UploadService.class);
            context.startService(cmd);
        }

        if (ConnectivityManager.CONNECTIVITY_ACTION.equals(action)) {
            boolean wifi = onWifi(context);
            Log.d(TAG, "onWifi = " + wifi);
            Intent cmd = new Intent(wifi ? UploadService.INTENT_NETWORK_WIFI : UploadService.INTENT_NETWORK_NOT_WIFI);
            String ssid = getSSID(context);
            cmd.putExtra("SSID", ssid);
            Log.d(TAG, "extra ssid (chk)= " + cmd.getStringExtra("SSID"));
            cmd.setClass(context, UploadService.class);
            context.startService(cmd);
        }
    }

    public static boolean onPower(Context context) {
        // TODO Auto-generated method stub
        return false;
    }

    public static boolean onWifi(Context context) {
        NetworkInfo ni = ((ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE)).getActiveNetworkInfo();
        if (ni != null && ni.isConnected()
                && (ni.getType() == ConnectivityManager.TYPE_WIFI || ni.getType() == ConnectivityManager.TYPE_ETHERNET)) {
            return true;
        }
        return false;
    }

    public static String getSSID(Context context) {
        NetworkInfo ni = ((ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE)).getActiveNetworkInfo();
        if (ni != null && ni.isConnected()
                && (ni.getType() == ConnectivityManager.TYPE_WIFI || ni.getType() == ConnectivityManager.TYPE_ETHERNET)) {
            WifiManager wifiMgr = (WifiManager) context.getSystemService(Context.WIFI_SERVICE);
            if (wifiMgr != null) {
                WifiInfo wifiInfo = wifiMgr.getConnectionInfo();
                String ssid = wifiInfo.getSSID();
                if (ssid.startsWith("\"") && ssid.endsWith("\"")){
                    ssid = ssid.substring(1, ssid.length()-1);
                }
                return ssid;
            }
        }
        return "";
    }
}
