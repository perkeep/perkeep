package org.camlistore;

import android.app.AlertDialog;
import android.content.Context;
import android.preference.Preference;
import android.util.AttributeSet;
import android.util.Log;

import com.google.zxing.integration.android.IntentIntegrator;

/**
 * QRPrefence implements a custom {@link Preference} for scanning barcodes.
 *
 * It will launch a barcode scanner intent configured for scanning QR codes using {@link IntentIntegrator}. If no barcode scanner app is installed {@link IntentIntegrator} will prompt the user to install one from the Google Play market.
 */
public class QRPreference extends Preference {
    private static final String TAG = "QRPreference";

    public QRPreference(Context context, AttributeSet attrs) {
        super(context, attrs);
        Log.v(TAG, "QRPreference");

    }

    @Override
    protected void onClick() {
        SettingsActivity activity = (SettingsActivity) this.getContext();
        IntentIntegrator integrator = new IntentIntegrator(activity);
        integrator.initiateScan(IntentIntegrator.QR_CODE_TYPES);
    }
}
