package com.danga.camli;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.net.ConnectivityManager;
import android.net.NetworkInfo;
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
            NetworkInfo ni = intent.getParcelableExtra(ConnectivityManager.EXTRA_NETWORK_INFO);
            Log.d(TAG, "NetworkInfo: " + ni);

            // Nexus one, starting with Wifi, and then turning it off, and watching it flip back
            // to 3G:

            // D/WifiPowerReceiver(25298): connectivity extras: Bundle[{networkInfo=NetworkInfo: type: WIFI[], state: DISCONNECTED/DISCONNECTED, reason: (unspecified), extra: (none), roaming: false, failover: false, isAvailable: false, otherNetwork=NetworkInfo: type: mobile[HSDPA], state: CONNECTING/CONNECTING, reason: apnSwitched, extra: epc.tmobile.com, roaming: false, failover: true, isAvailable: true}]

            // D/WifiPowerReceiver(25298): connectivity extras: Bundle[{networkInfo=NetworkInfo: type: mobile[HSDPA], state: CONNECTED/CONNECTED, reason: apnSwitched, extra: epc.tmobile.com, roaming: false, failover: false, isAvailable: true, reason=apnSwitched, isFailover=true, extraInfo=epc.tmobile.com}]
            
            
            // On Droid, Wifi turning off, switching to 3G:
            
            // D/WifiPowerReceiver( 2443): connectivity extras: Bundle[{networkInfo=NetworkInfo: type: WIFI[], state: DISCONNECTED/DISCONNECTED, reason: (unspecified), extra: (none), roaming: false, failover: false, isAvailable: false, otherNetwork=NetworkInfo: type: mobile[CDMA - EvDo rev. A], state: CONNECTING/CONNECTING, reason: apnSwitched, extra: (none), roaming: false, failover: true, isAvailable: true}]

            // D/WifiPowerReceiver( 2443): connectivity extras: Bundle[{networkInfo=NetworkInfo: type: mobile[CDMA - EvDo rev. A], state: CONNECTED/CONNECTED, reason: apnSwitched, extra: (none), roaming: false, failover: false, isAvailable: true, isFailover=true, reason=apnSwitched}]
        }
    }
}
