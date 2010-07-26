package com.danga.camli;

import android.app.AlarmManager;
import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.SystemClock;
import android.util.Log;

public class OnBootReceiver extends BroadcastReceiver {
    private static final String TAG = "Camli_OnBootReceiver";

    @Override
    public void onReceive(Context context, Intent intent) {
        Log.v(TAG, "onReceive on boot");
        AlarmManager alarmer = (AlarmManager) context.getSystemService(Context.ALARM_SERVICE);
        PendingIntent pendingIntent = PendingIntent.getBroadcast(context, 0, new Intent(context,
                OnAlarmReceiver.class), 0);

        alarmer.setInexactRepeating(AlarmManager.ELAPSED_REALTIME,
                SystemClock.elapsedRealtime() + 60000, AlarmManager.INTERVAL_HALF_HOUR,
                pendingIntent);

    }

}
