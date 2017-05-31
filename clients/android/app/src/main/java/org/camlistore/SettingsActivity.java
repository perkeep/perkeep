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

import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

import android.app.AlertDialog;
import android.content.ComponentName;
import android.content.Context;
import android.content.DialogInterface;
import android.content.Intent;
import android.content.ServiceConnection;
import android.content.SharedPreferences;
import android.net.Uri;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.IBinder;
import android.os.RemoteException;
import android.preference.CheckBoxPreference;
import android.preference.EditTextPreference;
import android.preference.Preference;
import android.preference.Preference.OnPreferenceChangeListener;
import android.preference.PreferenceActivity;
import android.preference.PreferenceScreen;
import android.text.TextUtils;
import android.util.Log;
import android.widget.Toast;

import com.google.zxing.integration.android.IntentIntegrator;
import com.google.zxing.integration.android.IntentResult;

public class SettingsActivity extends PreferenceActivity {
    private static final String TAG = "SettingsActivity";

    private IUploadService mServiceStub = null;

    private EditTextPreference hostPref;
    private EditTextPreference trustedCertPref;
    private EditTextPreference usernamePref;
    private EditTextPreference passwordPref;
    private EditTextPreference devIPPref;
    private CheckBoxPreference autoPref;
    private PreferenceScreen autoOpts;
    private EditTextPreference maxCacheSizePref;

    private SharedPreferences mSharedPrefs;
    private Preferences mPrefs;

    private Map<CharSequence, String> prefToParam;

    private final ServiceConnection mServiceConnection = new ServiceConnection() {
        @Override
        public void onServiceConnected(ComponentName name, IBinder service) {
            mServiceStub = IUploadService.Stub.asInterface(service);
        }

        @Override
        public void onServiceDisconnected(ComponentName name) {
            mServiceStub = null;
        };
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        Map<CharSequence, String> m = new HashMap<CharSequence, String>();
        m.put(Preferences.HOST, "server");
        m.put(Preferences.TRUSTED_CERT, "certFingerprint");
        m.put(Preferences.USERNAME, "username");
        m.put(Preferences.PASSWORD, "password");
        m.put(Preferences.AUTO, "autoUpload");
        m.put(Preferences.MAX_CACHE_MB, "maxCacheSize");
        prefToParam = Collections.unmodifiableMap(m);

        getPreferenceManager().setSharedPreferencesName(Preferences.NAME);
        addPreferencesFromResource(R.xml.preferences);

        hostPref = (EditTextPreference) findPreference(Preferences.HOST);
        // TODO(mpl): popup window that proposes to automatically add the cert to
        // the prefs when we fail to dial an untrusted server (and only in that case).
        trustedCertPref = (EditTextPreference) findPreference(Preferences.TRUSTED_CERT);
        usernamePref = (EditTextPreference) findPreference(Preferences.USERNAME);
        passwordPref = (EditTextPreference) findPreference(Preferences.PASSWORD);
        autoPref = (CheckBoxPreference) findPreference(Preferences.AUTO);
        autoOpts = (PreferenceScreen) findPreference(Preferences.AUTO_OPTS);
        maxCacheSizePref = (EditTextPreference) findPreference(Preferences.MAX_CACHE_MB);
        devIPPref = (EditTextPreference) findPreference(Preferences.DEV_IP);

        mSharedPrefs = getSharedPreferences(Preferences.NAME, 0);
        mPrefs = new Preferences(mSharedPrefs);

        // Display defaults.
        maxCacheSizePref.setSummary(getString(
                R.string.settings_max_cache_size_summary, mPrefs.maxCacheMb()));

        OnPreferenceChangeListener onChange = new OnPreferenceChangeListener() {
            @Override
            public boolean onPreferenceChange(Preference pref, Object newValue) {
                final String key = pref.getKey();
                Log.v(TAG, "preference change for: " + key);

                // Note: newValue isn't yet persisted, but easiest to update the
                // UI here.
                String newStr = (newValue instanceof String) ? (String) newValue
                        : null;
                if (pref == hostPref) {
                    updateHostSummary(newStr);
                } else if (pref == trustedCertPref) {
                    updateTrustedCertSummary(newStr);
                } else if (pref == passwordPref) {
                    updatePasswordSummary(newStr);
                } else if (pref == usernamePref) {
                    updateUsernameSummary(newStr);
                } else if (pref == maxCacheSizePref) {
                    if (!updateMaxCacheSizeSummary(newStr))
                        return false;
                } else if (pref == devIPPref) {
                    updateDevIP(newStr);
                }
                return true; // yes, persist it
            }
        };
        hostPref.setOnPreferenceChangeListener(onChange);
        trustedCertPref.setOnPreferenceChangeListener(onChange);
        passwordPref.setOnPreferenceChangeListener(onChange);
        usernamePref.setOnPreferenceChangeListener(onChange);
        maxCacheSizePref.setOnPreferenceChangeListener(onChange);
        devIPPref.setOnPreferenceChangeListener(onChange);
    }

    /**
     * Receives the results from the custome QRPreference's call to the barcode scanner intent.
     * 
     * This is never called if the user doesn't have a zxing barcode scanner app installed.
     */
    @Override
    public void onActivityResult(int requestCode, int resultCode, Intent intent) {
        IntentResult scanResult = IntentIntegrator.parseActivityResult(requestCode, resultCode, intent);
        if (scanResult != null && scanResult.getContents() != null) {
            // handle scan result
            Log.v(TAG, "Scan result" + scanResult);
            Uri uri = Uri.parse(scanResult.getContents());
            confirmNewSettingsDialog(uri);
        } else {
            // else continue with any other code you need in the method
            Log.v(TAG, "No result");
        }
    }

    /**
     * confirmNewSettingsDialog will set preferences based on the parameters
     * in uri.
     *
     * It is expected the schema of uri is 'camli' and the host is 'settings'.
     * Uri parameters expected are server, certFingerprint, username,
     * autoUpload, maxCacheSize, and password
     */
    private final void confirmNewSettingsDialog(final Uri uri) {
        Log.v(TAG, "QR resolved to: " + uri);
        if (!(uri.getScheme().equals("camli") && uri.getHost().equals("settings"))) {
            Toast.makeText(this, "QR code not a camli://settings/ URL", Toast.LENGTH_LONG).show();
            return;
        }

        AlertDialog.Builder builder = new AlertDialog.Builder(this);
        StringBuilder confirmation = new StringBuilder();

        for (Map.Entry<CharSequence, String> pref : prefToParam.entrySet()) {
            confirmation.append(findPreference(pref.getKey()).getTitle());
            confirmation.append(": ");
            confirmation.append(uri.getQueryParameter(pref.getValue()));
            confirmation.append("\n");
        }

        builder.setMessage(confirmation.toString())
                .setTitle(R.string.settings_confirmation_dialog_title);

        builder.setPositiveButton(R.string.ok, new DialogInterface.OnClickListener() {
            @Override
            public void onClick(DialogInterface dialog, int id) {
                hostPref.setText(uri.getQueryParameter("server"));
                trustedCertPref.setText(uri.getQueryParameter("certFingerprint"));
                usernamePref.setText(uri.getQueryParameter("username"));
                String auto = uri.getQueryParameter("autoUpload");
                autoPref.setChecked(auto != null && auto.equals("1"));
                maxCacheSizePref.setText(uri.getQueryParameter("maxCacheSize"));
                // Password isn't a value that can be set on /ui/mobile.html.  It
                // seems like a security risk to do so.  If there's a smart way to do it,
                // I'm up for suggestions.  In the meantime, if a person manually
                // adds it to the QR code URL, use it (helpful during development)
                // -wathiede.
                passwordPref.setText(uri.getQueryParameter("password"));

                updatePreferenceSummaries();
            }
        });
        builder.setNegativeButton(R.string.cancel, null);

        builder.create().show();
    }

    private final SharedPreferences.OnSharedPreferenceChangeListener prefChangedHandler = new SharedPreferences.OnSharedPreferenceChangeListener() {
        @Override
        public void onSharedPreferenceChanged(SharedPreferences sp, String key) {
            if (Preferences.AUTO.equals(key)) {
                boolean val = mPrefs.autoUpload();
                updateAutoOpts(val);
                Log.d(TAG, "AUTO changed to " + val);
                if (mServiceStub != null) {
                    try {
                        mServiceStub.setBackgroundWatchersEnabled(val);
                    } catch (RemoteException e) {
                        // Ignore.
                    }
                }
            }

        }
    };

    @Override
    protected void onPause() {
        super.onPause();
        mSharedPrefs
                .unregisterOnSharedPreferenceChangeListener(prefChangedHandler);
        if (mServiceConnection != null) {
            unbindService(mServiceConnection);
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        updatePreferenceSummaries();
        mSharedPrefs
                .registerOnSharedPreferenceChangeListener(prefChangedHandler);
        bindService(new Intent(this, UploadService.class), mServiceConnection,
                Context.BIND_AUTO_CREATE);
    }

    private void updatePreferenceSummaries() {
        updateHostSummary(hostPref.getText());
        updateTrustedCertSummary(trustedCertPref.getText());
        updatePasswordSummary(passwordPref.getText());
        updateAutoOpts(autoPref.isChecked());
        updateMaxCacheSizeSummary(maxCacheSizePref.getText());
        updateUsernameSummary(usernamePref.getText());
        updateDevIP(devIPPref.getText());
    }

    private void updateDevIP(String value) {
        // The Brad-is-lazy shortcut: if the user enters "12", assumes
        // "10.0.0.12", or whatever
        // the current wifi connections's /24 is.
        if (!TextUtils.isEmpty(value) && !value.contains(".")) {
            WifiManager wifiManager = (WifiManager) getSystemService(WIFI_SERVICE);
            WifiInfo wifiInfo = wifiManager.getConnectionInfo();
            if (wifiInfo != null) {
                int ip = wifiInfo.getIpAddress();
                value = String.format("%d.%d.%d.", ip & 0xff, (ip >> 8) & 0xff,
                        (ip >> 16) & 0xff) + value;
                devIPPref.setText(value);
                mPrefs.setDevIP(value);
            }

        }
        boolean enabled = TextUtils.isEmpty(value);
        hostPref.setEnabled(enabled);
        trustedCertPref.setEnabled(enabled);
        usernamePref.setEnabled(enabled);
        passwordPref.setEnabled(enabled);
        if (!enabled) {
            devIPPref.setSummary("Using http://" + value
                    + ":3179 user/pass \"camlistore\", \"pass3179\"");
        } else {
            devIPPref.setSummary("(Dev-server IP to override settings above)");
        }
    }

    private void updatePasswordSummary(String value) {
        if (value != null && value.length() > 0) {
            passwordPref.setSummary("*********");
        } else {
            passwordPref.setSummary("<unset>");
        }
    }

    private void updateUsernameSummary(String value) {
        if (value != null && value.length() > 0) {
            usernamePref.setSummary(value);
        } else {
            usernamePref.setSummary("<unset>");
        }
    }

    private void updateHostSummary(String value) {
        if (value != null && value.length() > 0) {
            hostPref.setSummary(value);
        } else {
            hostPref.setSummary(getString(R.string.settings_host_summary));
        }
    }

    private void updateTrustedCertSummary(String value) {
        if (value != null && value.length() > 0) {
            trustedCertPref.setSummary(value);
        } else {
            trustedCertPref.setSummary("<unset; optional 20 hex SHA-256 prefix>");
        }
    }

    private void updateAutoOpts(boolean checked) {
        autoOpts.setEnabled(checked);
    }

    // Update the summary for the max cache size setting.
    // Returns true if the value is valid and should be persisted and false
    // otherwise.
    private boolean updateMaxCacheSizeSummary(String value) {
        try {
            int mb = Integer.parseInt(value);
            if (mb <= 0)
                return false;
            maxCacheSizePref.setSummary(getString(
                    R.string.settings_max_cache_size_summary, mb));
            return true;
        } catch (NumberFormatException e) {
            return false;
        }
    }

    // Convenience method.
    static void show(Context context) {
        final Intent intent = new Intent(context, SettingsActivity.class);
        context.startActivity(intent);
    }
}
